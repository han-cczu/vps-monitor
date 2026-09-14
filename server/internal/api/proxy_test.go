package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"vpsmon/server/internal/proxy"
)

type proxyNotice struct {
	ID     int64
	Reason string
}
type apiProxyNotifier struct{ ch chan proxyNotice }

func (n apiProxyNotifier) NodeChanged(id int64, reason string) { n.ch <- proxyNotice{id, reason} }

func TestProxyAPICRUDSecretsAndAuth(t *testing.T) {
	notifications := make(chan proxyNotice, 100)
	e := newTestEnv(t, func(d *Deps) { d.Proxy = proxy.New(d.DB, apiProxyNotifier{notifications}) })
	token := e.adminToken(t)
	node, agentToken, _ := e.createServer(t, token, newServerBody())
	base := fmt.Sprintf("/api/servers/%d", node)
	for _, route := range []struct{ method, path string }{
		{"GET", base + "/inbounds"}, {"POST", base + "/inbounds"}, {"GET", "/api/inbounds/1"}, {"PUT", "/api/inbounds/1"}, {"DELETE", "/api/inbounds/1"}, {"POST", "/api/inbounds/1/regenerate-keys"},
		{"GET", base + "/cert"}, {"POST", base + "/cert/regenerate"}, {"GET", base + "/advanced"}, {"PUT", base + "/advanced"}, {"GET", base + "/core"},
		{"GET", "/api/subscribers"}, {"POST", "/api/subscribers"}, {"GET", "/api/subscribers/1"}, {"PUT", "/api/subscribers/1"}, {"DELETE", "/api/subscribers/1"}, {"PUT", "/api/subscribers/1/assignments"},
		{"POST", "/api/subscribers/1/reset-token"}, {"POST", "/api/subscribers/1/regenerate-credentials"}, {"POST", "/api/subscribers/1/reset-usage"},
	} {
		for _, credential := range []string{"", agentToken} {
			resp, _ := e.do(t, route.method, route.path, credential, nil)
			if resp.StatusCode != 401 {
				t.Fatalf("unprotected %s %s", route.method, route.path)
			}
		}
	}
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		resp, out := e.do(t, method, path, token, body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d expected %d: %v", method, path, resp.StatusCode, want, out)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("sensitive route cacheable: %s", path)
		}
		return out
	}
	if call("GET", base+"/cert", nil, 200)["cert"] != nil {
		t.Fatal("unexpected certificate")
	}
	core := call("GET", base+"/core", nil, 200)["core"].(map[string]any)
	if core["installed_version"] != nil || core["core"] != "sing-box" || core["applied_revision"] != float64(0) {
		t.Fatalf("core defaults %v", core)
	}
	if a := call("GET", base+"/advanced", nil, 200)["advanced"].(map[string]any); len(a["extra_json"].(map[string]any)) != 0 {
		t.Fatal("advanced defaults")
	}
	vless := call("POST", base+"/inbounds", map[string]any{"protocol": "vless", "listen_port": 443}, 201)["inbound"].(map[string]any)
	vpath := fmt.Sprintf("/api/inbounds/%.0f", vless["id"])
	settings := vless["settings"].(map[string]any)
	private := settings["private_key"].(string)
	if vless["tag"] != "vless-443" || settings["public_key"] == "" || private == "" {
		t.Fatal("inbound defaults/keys absent")
	}
	call("POST", base+"/inbounds", map[string]any{"protocol": "hysteria2", "listen_port": 443, "settings": map[string]any{"obfs_enabled": true}}, 201)
	ss := call("POST", base+"/inbounds", map[string]any{"protocol": "shadowsocks", "listen_port": 8443}, 201)["inbound"].(map[string]any)
	sspath := fmt.Sprintf("/api/inbounds/%.0f", ss["id"])
	call("POST", base+"/inbounds", map[string]any{"protocol": "tuic", "listen_port": 8443}, 409)
	cert := call("GET", base+"/cert", nil, 200)["cert"].(map[string]any)
	if cert["sni"] != "www.bing.com" || cert["cert_pem"] == "" {
		t.Fatal("automatic certificate absent")
	}
	if _, ok := cert["key_pem"]; ok {
		t.Fatal("certificate API leaked private key")
	}
	if c := call("POST", base+"/cert/regenerate", map[string]any{"sni": "new.example.com"}, 200)["cert"].(map[string]any); c["fingerprint_sha256"] == cert["fingerprint_sha256"] {
		t.Fatal("certificate did not rotate")
	}
	updated := call("PUT", vpath, map[string]any{"enabled": false, "settings": map[string]any{"handshake_server": "new.example.com"}}, 200)["inbound"].(map[string]any)
	if updated["enabled"] != false || updated["settings"].(map[string]any)["private_key"] != private {
		t.Fatal("partial PUT rotated key")
	}
	call("GET", vpath, nil, 200)
	rotated := call("POST", vpath+"/regenerate-keys", nil, 200)["inbound"].(map[string]any)
	if rotated["settings"].(map[string]any)["private_key"] == private {
		t.Fatal("key did not rotate")
	}
	list := call("GET", base+"/inbounds", nil, 200)["inbounds"].([]any)
	if len(list) != 3 {
		t.Fatal("wrong inbound list")
	}
	call("PUT", base+"/advanced", map[string]any{"extra_json": map[string]any{"outbounds": []any{map[string]any{"type": "shadowsocks", "tag": "relay", "password": "advanced-secret"}}, "route": map[string]any{"final": "relay"}}}, 200)
	sub := call("POST", "/api/subscribers", map[string]any{"name": "family", "expire_at": "2028-02-29", "reset_day": 31}, 201)["subscriber"].(map[string]any)
	spath := fmt.Sprintf("/api/subscribers/%.0f", sub["id"])
	for _, key := range []string{"uuid", "password", "ss_user_key", "sub_token"} {
		if sub[key] == "" || sub[key] == nil {
			t.Fatalf("missing %s", key)
		}
	}
	assigned := call("PUT", spath+"/assignments", map[string]any{"inbound_ids": []any{vless["id"], ss["id"]}}, 200)["subscriber"].(map[string]any)
	if assigned["servers_count"] != float64(1) || len(assigned["assigned_inbounds"].([]any)) != 2 {
		t.Fatal("assignment enrichment incorrect")
	}
	summary := call("GET", "/api/subscribers", nil, 200)["subscribers"].([]any)[0].(map[string]any)
	for _, key := range []string{"uuid", "password", "ss_user_key", "sub_token"} {
		if _, exists := summary[key]; exists {
			t.Fatalf("list leaked %s", key)
		}
	}
	rotatedSub := call("POST", spath+"/regenerate-credentials", nil, 200)["subscriber"].(map[string]any)
	for _, key := range []string{"uuid", "password", "ss_user_key"} {
		if rotatedSub[key] == sub[key] {
			t.Fatalf("did not rotate %s", key)
		}
	}
	newToken := call("POST", spath+"/reset-token", nil, 200)["subscriber"].(map[string]any)
	if newToken["sub_token"] == sub["sub_token"] || newToken["uuid"] != rotatedSub["uuid"] {
		t.Fatal("wrong token reset scope")
	}
	call("POST", spath+"/reset-usage", nil, 200)
	changed := call("PUT", spath, map[string]any{"enabled": false, "expire_at": nil}, 200)["subscriber"].(map[string]any)
	if changed["expire_at"] != nil || changed["enabled"] != false {
		t.Fatal("subscriber partial update")
	}
	call("GET", spath, nil, 200)
	call("DELETE", sspath, nil, 204)
	call("PUT", spath+"/assignments", map[string]any{"inbound_ids": []any{}}, 200)
	call("DELETE", spath, nil, 204)
	call("GET", spath, nil, 404)
	call("GET", "/api/servers/999999/core", nil, 404)
	call("GET", "/api/servers/999999/cert", nil, 404)
	entries, err := e.db.ListAudit(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, entry := range entries {
		actions[entry.Action] = true
		if strings.HasPrefix(entry.Action, "subscriber.") || strings.HasPrefix(entry.Action, "inbound.") || strings.HasPrefix(entry.Action, "cert.") || entry.Action == "node_advanced.update" {
			if entry.Actor != "admin" || entry.IP == "" {
				t.Fatal("audit actor/IP missing")
			}
			for _, payload := range []string{entry.Before, entry.After} {
				if payload == "" {
					continue
				}
				if strings.Contains(payload, private) || strings.Contains(payload, sub["password"].(string)) || strings.Contains(payload, "advanced-secret") {
					t.Fatal("audit secret leakage")
				}
				var v any
				if json.Unmarshal([]byte(payload), &v) != nil {
					t.Fatal("invalid audit JSON")
				}
				assertRedacted(t, v)
			}
		}
	}
	for _, action := range []string{"inbound.create", "inbound.update", "inbound.regenerate_keys", "inbound.delete", "cert.generate", "cert.regenerate", "subscriber.create", "subscriber.update", "subscriber.regenerate_credentials", "subscriber.reset_token", "subscriber.reset_usage", "subscriber.delete", "assignment.update", "node_advanced.update"} {
		if !actions[action] {
			t.Fatalf("missing audit %s", action)
		}
	}
	if len(notifications) < 12 {
		t.Fatalf("missing node change notifications: %d", len(notifications))
	}
}

func assertRedacted(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			switch key {
			case "private_key", "key_pem", "server_psk", "obfs_password", "uuid", "password", "ss_user_key", "sub_token":
				if value != "***" {
					t.Fatalf("audit %s not redacted", key)
				}
			}
			assertRedacted(t, value)
		}
	case []any:
		for _, item := range x {
			assertRedacted(t, item)
		}
	}
}

func TestProxyRejectsMalformedAndManagedFields(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	node, _, _ := e.createServer(t, token, newServerBody())
	base := fmt.Sprintf("/api/servers/%d", node)
	for _, c := range []struct {
		path string
		body any
	}{
		{base + "/inbounds", map[string]any{"protocol": "vless", "listen_port": 0}},
		{base + "/inbounds", map[string]any{"protocol": "vless", "listen_port": 65536}},
		{base + "/inbounds", map[string]any{"protocol": "vless", "listen_port": 443, "tag": "manual"}},
		{base + "/inbounds", map[string]any{"protocol": "vless", "listen_port": 443, "settings": nil}},
		{base + "/inbounds", map[string]any{"protocol": "tuic", "listen_port": 443, "settings": map[string]any{"private_key": "secret"}}},
		{base + "/cert/regenerate", map[string]any{"sni": "https://example.com"}},
		{"/api/subscribers", map[string]any{"name": "valid", "uuid": "client-controlled"}},
		{"/api/subscribers", map[string]any{"name": "valid", "traffic_used": 100}},
		{"/api/subscribers", map[string]any{"name": "valid", "expire_at": "2026-02-30"}},
		{"/api/subscribers", map[string]any{"name": "valid", "reset_day": 32}},
		{"/api/subscribers", map[string]any{"name": "valid", "traffic_limit": -1}},
		{"/api/subscribers", map[string]any{"name": nil}},
	} {
		resp, _ := e.do(t, "POST", c.path, token, c.body)
		if resp.StatusCode != 400 {
			t.Fatalf("accepted invalid body on %s: %d", c.path, resp.StatusCode)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{"name":"ok","enabled":null}`, `{"name":"` + strings.Repeat("x", maxBodyBytes) + `"}`} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/subscribers", strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("invalid JSON accepted: %d", resp.StatusCode)
		}
	}
}
