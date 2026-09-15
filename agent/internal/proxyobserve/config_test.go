package proxyobserve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"vpsmon/proto"
)

func TestConfigAllowlistJSONCAndUnknownProtocol(t *testing.T) {
	raw := []byte(`{
// manager comment
"url":"https://example.test/a//b/*c*/",
"inbounds":[{"type":"anytls","tag":"public","listen":"::","listen_port":1234,"users":[{"name":"secret-user","password":"secret-password"}],"tls":{"enabled":true,"key":"PRIVATE KEY"}},
{"type":"future-protocol","tag":"550e8400-e29b-41d4-a716-446655440000","listen_port":3456,},],}`)
	r, err := parseConfig("sing-box", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Inbounds) != 2 || r.Inbounds[0].Users == nil || *r.Inbounds[0].Users != 1 || !r.Inbounds[0].TLS || r.Inbounds[1].Protocol != "future-protocol" {
		t.Fatalf("wrong projection: %+v", r.Inbounds)
	}
	b, _ := json.Marshal(r.Inbounds)
	for _, secret := range []string{"secret-user", "secret-password", "PRIVATE KEY", "550e8400-e29b-41d4-a716-446655440000"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("secret leaked")
		}
	}
	clean, err := jsonc(raw)
	if err != nil || !strings.Contains(string(clean), "https://example.test/a//b/*c*/") {
		t.Fatal("URL damaged")
	}
	for _, bad := range []string{`{"inbounds":[`, `/* unfinished`, `"not an object"`} {
		if _, err := parseConfig("sing-box", []byte(bad)); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
func TestXrayManagementInboundExcluded(t *testing.T) {
	r, err := parseConfig("xray", []byte(`{"api":{"tag":"api","services":["StatsService"]},"inbounds":[{"tag":"api","listen":"127.0.0.1","port":62789,"protocol":"dokodemo-door"},{"tag":"proxy","port":23356,"protocol":"hysteria","settings":{"clients":[{"password":"hidden"}]},"streamSettings":{"network":"hysteria","security":"tls"}}]}`))
	if err != nil || len(r.Inbounds) != 1 || r.StatsAddress != "127.0.0.1:62789" || r.Inbounds[0].Protocol != "hysteria" {
		t.Fatalf("wrong Xray projection: %+v %v", r, err)
	}
}
func TestReadStableBoundsAndPages(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte("abcd"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStable(p, 3); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err := readStable(dir, 100); err == nil {
		t.Fatal("directory accepted")
	}
	items := []proto.ObservedInstance{{ID: "test", Core: "xray", Inbounds: make([]proto.ObservedInbound, 130)}}
	pages := observationPages(items, "session", 9, true, 1)
	if len(pages) != 3 || pages[2].Page != 2 || len(pages[2].Instances[0].Inbounds) != 2 {
		t.Fatal("bad pagination")
	}
	for _, p := range pages {
		b, _ := json.Marshal(p)
		if len(b) > proto.ProxyMaxFrame {
			t.Fatal("frame limit")
		}
	}
}
func TestPersistentIdentityAndBindingValidation(t *testing.T) {
	o, err := New(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := o.identity("xray/custom/unit/namespace")
	n, err := New(o.options)
	if err != nil || n.identity("xray/custom/unit/namespace") != id {
		t.Fatal("identity changed on restart")
	}
	if err := (Config{Bindings: []Binding{{Core: "xray", Binary: "/opt/custom", ConfigPaths: []string{"/opt/config.json"}}}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{Bindings: []Binding{{Core: "xray", Binary: "/opt/custom", ConfigPaths: []string{"relative"}}}}).Validate(); err == nil {
		t.Fatal("relative binding accepted")
	}
}
