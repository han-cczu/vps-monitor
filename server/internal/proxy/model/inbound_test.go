package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrepareSettingsDefaultsPreserveAndRotate(t *testing.T) {
	for _, protocol := range []string{"vless", "shadowsocks", "hysteria2", "tuic"} {
		t.Run(protocol, func(t *testing.T) {
			original, err := PrepareSettings(protocol, nil, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			unchanged, err := PrepareSettings(protocol, []byte(`{}`), original, false)
			if err != nil || string(original) != string(unchanged) {
				t.Fatal("omitted fields rotated/reset")
			}
			changed, err := PrepareSettings(protocol, nil, original, true)
			if err != nil {
				t.Fatal(err)
			}
			if protocol != "tuic" && string(changed) == string(original) {
				t.Fatal("regeneration did not rotate")
			}
			if _, err = ParseSettings(protocol, changed); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestSettingsValidation(t *testing.T) {
	for _, c := range []struct{ protocol, raw string }{
		{"vless", `{"handshake_port":0}`}, {"vless", `{"handshake_server":"https://a.com"}`}, {"vless", `{"short_ids":[]}`}, {"vless", `{"short_ids":["abc"]}`},
		{"vless", `{"short_ids":["ab","ab"]}`}, {"vless", `{"public_key":"mismatch"}`}, {"vless", `{"private_key":"bad"}`},
		{"shadowsocks", `{"method":"aes-256-gcm"}`}, {"shadowsocks", `{"server_psk":"too-short"}`},
		{"hysteria2", `{"up_mbps":-1}`}, {"hysteria2", `{"obfs_enabled":true,"obfs_password":""}`},
		{"tuic", `{"congestion_control":"reno"}`}, {"tuic", `{"unexpected":"secret"}`}, {"tuic", `{"zero_rtt":null}`}, {"tuic", `[]`}, {"tuic", `   `}, {"unknown", `{}`},
	} {
		if _, err := PrepareSettings(c.protocol, []byte(c.raw), nil, false); err == nil {
			t.Errorf("accepted %s %s", c.protocol, c.raw)
		}
	}
	if _, err := Object([]byte(strings.Repeat(" ", 100)), 64); err == nil {
		t.Fatal("oversized object accepted")
	}
	valid, err := PrepareSettings("vless", []byte(`{"short_ids":["ab","1234567890abcdef"]}`), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var settings VlessSettings
	_ = json.Unmarshal(valid, &settings)
	if settings.HandshakePort != 443 {
		t.Fatal("missing default")
	}
}
func TestTransportConflicts(t *testing.T) {
	protocols := []string{"vless", "shadowsocks", "hysteria2", "tuic"}
	for _, a := range protocols {
		for _, b := range protocols {
			i := Inbound{ServerID: 1, Protocol: a, ListenPort: 443}
			j := Inbound{ServerID: 1, Protocol: b, ListenPort: 443}
			want := a == "shadowsocks" || b == "shadowsocks" || (a == "vless") == (b == "vless")
			if Conflicts(i, j) != want {
				t.Fatalf("wrong conflict %s/%s", a, b)
			}
			j.ServerID = 2
			if Conflicts(i, j) {
				t.Fatal("conflict across nodes")
			}
			j.ServerID = 1
			j.ListenPort = 444
			if Conflicts(i, j) {
				t.Fatal("conflict across ports")
			}
		}
	}
}
