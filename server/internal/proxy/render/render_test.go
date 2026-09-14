package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/keys"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

var update = flag.Bool("update", false, "update reviewed render golden fixtures")

func fixture(t *testing.T) Input {
	t.Helper()
	pemPath, keyPath := filepath.Join("testdata", "cert.pem"), filepath.Join("testdata", "key.pem")
	if _, err := os.Stat(pemPath); errors.Is(err, os.ErrNotExist) && *update {
		c, err := certs.Generate("www.bing.com", time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll("testdata", 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(pemPath, []byte(c.CertPEM), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(keyPath, []byte(c.KeyPEM), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cert, err := os.ReadFile(pemPath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Server: store.Server{ID: 1}, Version: "v1.14.0", UsersByInbound: map[int64][]store.Subscriber{}, Cert: &certs.Cert{ServerID: 1, CertPEM: string(cert), KeyPEM: string(key)}, Extra: json.RawMessage(`{}`)}
	private := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	public, err := keys.PublicKey(private)
	if err != nil {
		t.Fatal(err)
	}
	settings := []any{model.VlessSettings{HandshakeServer: "www.microsoft.com", HandshakePort: 443, PrivateKey: private, PublicKey: public, ShortIDs: []string{"0123456789abcdef"}}, model.ShadowsocksSettings{Method: "2022-blake3-aes-128-gcm", ServerPSK: "AQEBAQEBAQEBAQEBAQEBAQ=="}, model.Hysteria2Settings{ObfsEnabled: true, ObfsPassword: "fixture-obfs", UpMbps: 100, DownMbps: 100}, model.TuicSettings{CongestionControl: "bbr"}}
	for index, protocol := range []string{"vless", "shadowsocks", "hysteria2", "tuic"} {
		id := int64(index + 1)
		port := []int{24443, 28388, 28443, 28444}[index]
		raw, _ := json.Marshal(settings[index])
		in.Inbounds = append(in.Inbounds, model.Inbound{ID: id, ServerID: 1, Protocol: protocol, ListenPort: port, Tag: model.Tag(protocol, port), Enabled: true, Settings: raw})
		in.UsersByInbound[id] = []store.Subscriber{{ID: 1, Enabled: true, AutoDisabled: "none", UUID: "00000000-0000-4000-8000-000000000001", Password: "fixture-password", SSUserKey: "AgICAgICAgICAgICAgICAg=="}}
	}
	return in
}
func cases(t *testing.T) map[string]Input {
	in := fixture(t)
	out := map[string]Input{"all": in}
	for index, name := range []string{"vless", "shadowsocks", "hysteria2", "tuic"} {
		single := in
		single.Inbounds = []model.Inbound{in.Inbounds[index]}
		out[name] = single
	}
	empty := in
	empty.UsersByInbound = map[int64][]store.Subscriber{}
	out["empty-users"] = empty
	none := in
	none.Inbounds = nil
	out["no-inbounds"] = none
	advanced := in
	advanced.Extra = json.RawMessage(`{"dns":{"servers":[{"type":"udp","tag":"dns","server":"1.1.1.1"}]},"outbounds":[{"type":"direct","tag":"secondary"}],"route":{"rules":[{"domain":["example.com"],"action":"route","outbound":"secondary"}],"final":"secondary"}}`)
	out["advanced"] = advanced
	return out
}
func TestRenderGoldens(t *testing.T) {
	for name, in := range cases(t) {
		t.Run(name, func(t *testing.T) {
			out, err := Render(in)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name+".golden.json")
			if *update {
				if err = os.WriteFile(path, append(slices.Clone(out.Config), '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimSpace(want), out.Config) {
				t.Fatal("render differs from reviewed golden", path)
			}
			var compact bytes.Buffer
			if err = json.Compact(&compact, out.Config); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(compact.Bytes())
			if hex.EncodeToString(sum[:]) != out.SHA256 {
				t.Fatal("hash differs from Agent compact-JSON hash")
			}
			inspected, err := Inspect(out.Config)
			if err != nil || !slices.Equal(inspected.Ports, out.Ports) || !slices.Equal(inspected.UserNames, out.UserNames) {
				t.Fatal("stored revision inspection mismatch", err)
			}
			if name == "empty-users" && !strings.Contains(string(out.Config), `"managed": true`) {
				t.Fatal("empty SS users must not fall back to shared-key authentication")
			}
		})
	}
}
func TestRenderValidationAndOrder(t *testing.T) {
	in := fixture(t)
	out, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(in.Inbounds)
	again, err := Render(in)
	if err != nil || !bytes.Equal(out.Config, again.Config) {
		t.Fatal("input ordering changed config")
	}
	in = fixture(t)
	in.Cert = nil
	if _, err := Render(in); err == nil {
		t.Fatal("missing certificate accepted")
	}
	for _, extra := range []string{`{"outbounds":[{"type":"direct","tag":"direct"}]}`, `{"inbounds":[]}`, `{"experimental":{}}`, `{"log":{}}`, `{"route":{"rules":null}}`} {
		in = fixture(t)
		in.Extra = json.RawMessage(extra)
		if _, err := Render(in); err == nil {
			t.Fatal("unsafe extra accepted", extra)
		}
	}
	in = fixture(t)
	in.Inbounds[0].ListenPort = 10085
	in.Inbounds[0].Tag = "vless-10085"
	if _, err := Render(in); err == nil {
		t.Fatal("stats listener collision accepted")
	}
	in = fixture(t)
	in.Inbounds[0].Enabled = false
	for id, users := range in.UsersByInbound {
		users[0].AutoDisabled = "quota"
		in.UsersByInbound[id] = users
	}
	out, err = Render(in)
	if err != nil || len(out.UserNames) != 0 || len(out.Ports) != 4 {
		t.Fatal("disabled inbound/user included", err)
	}
}
func TestRealCoreChecks(t *testing.T) {
	binary := os.Getenv("SING_BOX_BIN")
	if binary == "" {
		t.Skip("set SING_BOX_BIN to run the pinned real Linux binary")
	}
	for name, in := range cases(t) {
		t.Run(name, func(t *testing.T) {
			out, err := Render(in)
			if err != nil {
				t.Fatal(err)
			}
			if err = Check(context.Background(), binary, out.Config); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := Check(context.Background(), binary, []byte(`{"unknown_field":true}`)); !errors.Is(err, ErrCheck) {
		t.Fatal("invalid config passed", err)
	}
}
func TestCheckMissing(t *testing.T) {
	if err := Check(context.Background(), filepath.Join(t.TempDir(), "absent"), []byte(`{}`)); !errors.Is(err, ErrNoLocalCore) {
		t.Fatal(err)
	}
}
