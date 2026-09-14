package sub

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

func testProxies(t *testing.T) []Proxy {
	t.Helper()
	out := []Proxy{}
	for i, protocol := range []string{"vless", "shadowsocks", "hysteria2", "tuic"} {
		raw, err := model.PrepareSettings(protocol, json.RawMessage(`{}`), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, Proxy{Name: []string{"yes", "a: b", "双引号\" 与换行\n", "on"}[i], Protocol: protocol, Host: "2001:db8::1", Port: 443 + i, Inbound: model.Inbound{Protocol: protocol, Settings: raw}, Sub: store.Subscriber{UUID: "uuid", Password: "user-password", SSUserKey: "user-key"}, Cert: &certs.Cert{SNI: "example.com", FingerprintSHA256: "aabb"}})
	}
	return out
}
func TestClashProtocolsQuotedSecretsAndTemplate(t *testing.T) {
	proxies := testProxies(t)
	body, err := RenderClash(proxies, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	entries := doc["proxies"].([]any)
	if len(entries) != 4 {
		t.Fatal("expected four protocols")
	}
	for i, entry := range entries {
		v := entry.(map[string]any)
		if v["name"] != proxies[i].Name || v["server"] != "2001:db8::1" {
			t.Fatal("quoted values changed")
		}
		if i >= 2 && (v["fingerprint"] != "aabb" || v["skip-cert-verify"] != false) {
			t.Fatal("pin missing or verification disabled")
		}
	}
	ss := entries[1].(map[string]any)
	var ssSettings model.ShadowsocksSettings
	_ = json.Unmarshal(proxies[1].Inbound.Settings, &ssSettings)
	if ss["password"] != ssSettings.ServerPSK+":user-key" {
		t.Fatal("SS2022 multiuser password incorrect")
	}
	if strings.Contains(string(body), "private-key") || strings.Contains(string(body), "private_key") {
		t.Fatal("server key leaked")
	}
	for _, bad := range []string{"proxies: []", "{{PROXIES}}\ngroups: [{{PROXY_NAMES}}\n", "{{PROXIES}}\n---\nproxies: [{{PROXY_NAMES}}]", "{{PROXIES}}\nproxies: [{{PROXY_NAMES}}]"} {
		if ValidateClashTemplate(bad) == nil {
			t.Fatal("invalid template accepted")
		}
	}
	if err = ValidateClashTemplate(DefaultClashTemplate); err != nil {
		t.Fatal(err)
	}
	provider, err := RenderClashProvider(nil)
	if err != nil || strings.TrimSpace(string(provider)) != "proxies: []" {
		t.Fatalf("empty provider %q %v", provider, err)
	}
}
