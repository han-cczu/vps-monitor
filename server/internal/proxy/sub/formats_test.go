package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/keys"
)

func TestSubscriptionSingboxAndURI(t *testing.T) {
	proxies := testProxies(t)
	cert, err := certs.Generate("example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i := range proxies {
		proxies[i].Cert = &cert
	}
	body, err := RenderSingbox(proxies)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err = json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Outbounds) != 4 {
		t.Fatal("missing outbound")
	}
	for _, i := range []int{2, 3} {
		tls := data.Outbounds[i]["tls"].(map[string]any)
		if tls["insecure"] != false || tls["certificate"] == nil {
			t.Fatal("certificate verification missing")
		}
	}
	if strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatal("private certificate key exported")
	}
	encoded, err := RenderURI(proxies)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 4 {
		t.Fatal("missing URI")
	}
	for i, line := range lines {
		u, err := url.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		if u.Fragment != proxies[i].Name || u.Hostname() != "2001:db8::1" {
			t.Fatal("URI names/IPv6 changed")
		}
		if i == 1 {
			password, _ := u.User.Password()
			if !strings.HasSuffix(password, ":user-key") {
				t.Fatal("SS user key encoding incorrect")
			}
		}
		if i == 2 && (u.Query().Get("pinSHA256") == "" || u.Query().Get("insecure") != "1") {
			t.Fatal("HY2 pin contract")
		}
		if i == 3 && u.Query().Get("allow_insecure") != "1" {
			t.Fatal("TUIC URI compatibility flag missing")
		}
	}
	empty, err := RenderSingbox(nil)
	if err != nil || !strings.Contains(string(empty), `"outbounds": []`) {
		t.Fatal("empty singbox")
	}
	empty, err = RenderURI(nil)
	if err != nil || len(empty) != 0 {
		t.Fatal("disabled URI not empty")
	}
}
func TestSubscriptionSingboxCoreCheck(t *testing.T) {
	binary := os.Getenv("VM_TEST_SINGBOX")
	if binary == "" {
		t.Skip("set VM_TEST_SINGBOX for a real local core check")
	}
	proxies := testProxies(t)
	cert, err := certs.Generate("example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i := range proxies {
		proxies[i].Cert = &cert
		proxies[i].Sub.UUID = keys.UUID()
		proxies[i].Sub.SSUserKey = keys.PSK16()
	}
	body, err := RenderSingbox(proxies)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "client.json")
	if err = os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "check", "-c", file).CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box client check failed: %v (%d diagnostic bytes)", err, len(output))
	}
}
