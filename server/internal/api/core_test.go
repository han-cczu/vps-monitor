package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"vpsmon/proto"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/proxy"
)

func TestCoreAPIWebSocketLifecycle(t *testing.T) {
	var rec *proxy.Reconciler
	var realtime *hub.Hub
	e := newTestEnv(t, func(d *Deps) {
		realtime = hub.New(d.DB, d.Tokens)
		rec = proxy.NewReconciler(d.DB, proxy.ReconcilerOptions{Agents: realtime.Agents, Debounce: time.Hour, LogTimeout: 100 * time.Millisecond, Check: func(context.Context, string, []byte) error { return nil }, Artifact: func(context.Context, string, string) (corefiles.Artifact, error) {
			return corefiles.Artifact{SHA256: strings.Repeat("a", 64)}, nil
		}})
		t.Cleanup(rec.Close)
		realtime.Agents.OnCore(rec.Handle, rec.OnAgentHello)
		realtime.Registry.SetCoreSource(rec.SnapshotFor)
		d.Hub = realtime
		d.Reconciler = rec
		d.Proxy = proxy.New(d.DB, rec)
	})
	token := e.adminToken(t)
	id, agentToken, _ := e.createServer(t, token, newServerBody())
	base := fmt.Sprintf("/api/servers/%d/core", id)
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		resp, out := e.do(t, method, path, token, body)
		if resp.StatusCode != want || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d expected %d: %v", method, path, resp.StatusCode, want, out)
		}
		return out
	}
	for _, route := range []struct{ method, path string }{{"POST", "/install"}, {"POST", "/restart"}, {"POST", "/apply"}, {"GET", "/logs"}, {"GET", "/revisions"}, {"GET", "/revisions/1"}, {"POST", "/rollback/1"}} {
		for _, credential := range []string{"", agentToken} {
			resp, _ := e.do(t, route.method, base+route.path, credential, nil)
			if resp.StatusCode != 401 {
				t.Fatal("core route authentication missing", route)
			}
		}
	}
	call("POST", base+"/install", nil, 409)
	call("GET", base+"/logs", nil, 409)
	call("POST", base+"/apply", nil, 409)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := e.db.SetSetting(ctx, corefiles.CurrentKey, "v1.14.0"); err != nil {
		t.Fatal(err)
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + agentToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	send := func(v any) {
		t.Helper()
		if err := wsjson.Write(ctx, conn, v); err != nil {
			t.Fatal(err)
		}
	}
	read := func(v any) {
		t.Helper()
		if err := wsjson.Read(ctx, conn, v); err != nil {
			t.Fatal(err)
		}
	}
	var config proto.Config
	read(&config)
	send(proto.Hello{Type: proto.TypeHello, ProtoVersion: proto.Version, Capabilities: []string{proto.ProxyObserveCapability}, ProxyManagement: "none", Host: proto.HostInfo{Arch: "x86_64"}})
	read(&config)
	if config.ProxyObserveSession == "" {
		t.Fatal("observation session missing")
	}
	send(proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", Firewall: "none"})
	wait := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !fn() {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for core state")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(func() bool { h, err := e.db.GetHostInfo(ctx, id); return err == nil && h.Arch == "x86_64" })
	inboundIDs := []any{}
	for index, protocol := range []string{"vless", "shadowsocks", "hysteria2", "tuic"} {
		item := call("POST", fmt.Sprintf("/api/servers/%d/inbounds", id), map[string]any{"protocol": protocol, "listen_port": 24440 + index}, 201)["inbound"].(map[string]any)
		inboundIDs = append(inboundIDs, item["id"])
	}
	sub := call("POST", "/api/subscribers", map[string]any{"name": "integration"}, 201)["subscriber"].(map[string]any)
	subPath := fmt.Sprintf("/api/subscribers/%.0f", sub["id"])
	call("PUT", subPath+"/assignments", map[string]any{"inbound_ids": inboundIDs}, 200)
	applied := call("POST", base+"/apply", nil, 200)
	if applied["revision"].(map[string]any)["revision"] != float64(1) {
		t.Fatal("wrong first revision")
	}
	install := call("POST", base+"/install", nil, 202)
	var action proto.CoreAction
	read(&action)
	if action.Action != "install" || action.File != "sing-box-linux-amd64" || action.ReqID != install["req_id"] {
		t.Fatal("install not delivered")
	}
	send(proto.Hello{Type: proto.TypeHello, ProtoVersion: proto.Version, Capabilities: []string{proto.ProxyObserveCapability}, ProxyManagement: "managed", Host: proto.HostInfo{Arch: "x86_64"}})
	send(proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: "v1.14.0", Firewall: "none", ReqID: action.ReqID})
	var apply proto.CoreApply
	read(&apply)
	if apply.Revision != 1 || len(apply.Ports) != 5 {
		t.Fatal("wrong complete four-protocol config")
	}
	acknowledge := func(a proto.CoreApply) {
		send(proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: a.Version, Running: true, AppliedRevision: a.Revision, ConfigSHA256: a.ConfigSHA256, Listening: a.Ports, Firewall: "none", ReqID: a.ReqID})
	}
	acknowledge(apply)
	wait(func() bool { v, err := rec.View(ctx, id); return err == nil && !v.Pending && v.Running })
	snapshot := realtime.Registry.SnapshotFrame(ctx)
	b, _ := json.Marshal(snapshot)
	if !strings.Contains(string(b), `"installed":true`) || !strings.Contains(string(b), `"users":1`) {
		t.Fatal("core summary not in browser snapshot")
	}
	send(proto.CoreStats{Type: proto.TypeCoreStats, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%.0f", sub["id"]), Up: 100, Down: 50 << 20}}, Inbounds: []proto.Counter{{Name: "ss-24441", Up: 100, Down: 50 << 20}}})
	wait(func() bool { return len(rec.Stats.Inbounds(id)) == 1 })
	if err := rec.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got := call("GET", subPath, nil, 200)["subscriber"].(map[string]any)
	if got["traffic_used"] != float64(100+50<<20) {
		t.Fatal("WS stats not accounted", got["traffic_used"])
	}
	list := call("GET", base+"/revisions", nil, 200)["revisions"].([]any)
	if _, ok := list[0].(map[string]any)["config_json"]; ok {
		t.Fatal("list leaked config")
	}
	call("GET", base+"/revisions/1", nil, 200)
	call("GET", base+"/revisions/0", nil, 400)
	call("GET", base+"/revisions/999", nil, 404)
	call("POST", base+"/restart", nil, 202)
	read(&action)
	send(proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: apply.Version, Running: true, AppliedRevision: apply.Revision, ConfigSHA256: apply.ConfigSHA256, Listening: apply.Ports, Firewall: "none", ReqID: action.ReqID})
	logDone := make(chan map[string]any, 1)
	go func() { logDone <- call("GET", base+"/logs?lines=20", nil, 200) }()
	var logs proto.CoreLogsReq
	read(&logs)
	send(proto.CoreLogs{Type: proto.TypeCoreLogs, Kind: "error", ReqID: logs.ReqID, Text: "test log\n"})
	if (<-logDone)["text"] != "test log\n" {
		t.Fatal("logs response mismatch")
	}
	call("GET", base+"/logs?lines=0", nil, 400)
	rolled := call("POST", base+"/rollback/1", nil, 200)["revision"].(map[string]any)
	read(&apply)
	if rolled["revision"] != float64(2) || apply.Revision != 2 {
		t.Fatal("rollback not new revision")
	}
	acknowledge(apply)
	wait(func() bool { v, err := rec.View(ctx, id); return err == nil && !v.Pending })
	entries, err := e.db.ListAudit(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Action, "core.") && (strings.Contains(entry.After, sub["password"].(string)) || strings.Contains(entry.After, "private_key")) {
			t.Fatal("revision audit leaked credentials")
		}
	}
	if resp, _ := e.do(t, "DELETE", fmt.Sprintf("/api/servers/%d", id), token, nil); resp.StatusCode != 204 {
		t.Fatal("node deletion failed")
	}
	call("GET", base, nil, 404)
}
