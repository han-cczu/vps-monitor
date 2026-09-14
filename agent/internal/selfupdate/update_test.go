package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"vpsmon/proto"
)

func fixture(t *testing.T) (*Updater, proto.AgentUpdate, *int) {
	t.Helper()
	payload := "fake new executable"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/vps-agent-linux-amd64" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "vps-agent")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	u, _ := New(strings.Replace(server.URL, "http://", "ws://", 1)+"/api/agent/ws?token=never", "v1.0.0", nil)
	u.binary = path
	u.goos = "linux"
	u.arch = "amd64"
	u.runVersion = func(context.Context, string) (string, error) { return "v1.1.0", nil }
	calls := 0
	u.replaceProcess = func(string) error { calls++; return nil }
	h := sha256.Sum256([]byte(payload))
	a := proto.AgentUpdate{Type: proto.TypeAgentUpdate, Version: "v1.1.0", File: "vps-agent-linux-amd64", SHA256: hex.EncodeToString(h[:])}
	return u, a, &calls
}
func TestUpdateVerificationReplacementAndRollback(t *testing.T) {
	for _, kind := range []string{"success", "hash", "version", "version-error", "too-large", "restart", "downgrade", "path", "arch"} {
		t.Run(kind, func(t *testing.T) {
			u, a, calls := fixture(t)
			switch kind {
			case "hash":
				a.SHA256 = strings.Repeat("0", 64)
			case "version":
				u.runVersion = func(context.Context, string) (string, error) { return "v1.9.9", nil }
			case "version-error":
				u.runVersion = func(context.Context, string) (string, error) { return "", errors.New("exec failed") }
			case "too-large":
				u.maxSize = 2
			case "restart":
				u.replaceProcess = func(string) error { (*calls)++; return errors.New("exec failure") }
			case "downgrade":
				a.Version = "v0.9.0"
			case "path":
				a.File = "../bin/sh"
			case "arch":
				a.File = "vps-agent-linux-arm64"
			}
			err := u.apply(context.Background(), a)
			raw, _ := os.ReadFile(u.binary)
			if kind == "success" {
				if err != nil || string(raw) == "old" || *calls != 1 {
					t.Fatalf("%s %d %v", raw, *calls, err)
				}
				backup, _ := os.ReadFile(u.binary + ".bak")
				if string(backup) != "old" {
					t.Fatal("backup missing")
				}
			} else {
				if err == nil || string(raw) != "old" {
					t.Fatalf("old binary not retained %s %v", raw, err)
				}
				if kind != "restart" && *calls != 0 {
					t.Fatal("unverified binary executed")
				}
			}
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(u.binary), ".vps-agent-*-*"))
			if len(leftovers) != 0 {
				t.Fatal(leftovers)
			}
		})
	}
}
func TestRejectMalformedOldAndConcurrentCommands(t *testing.T) {
	u, a, _ := fixture(t)
	for _, raw := range []string{`{`, `{"type":"exec"}`, `{"type":"agent.update","file":"../../evil"}`, `{"type":"agent.update","extra":"x"}`, strings.Repeat("x", 2049)} {
		if err := u.Handle([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	data, _ := json.Marshal(a)
	if err := u.Handle(data); err != nil {
		t.Fatal(err)
	}
	if err := u.Handle(data); err == nil {
		t.Fatal("concurrent update queued")
	}
	for _, server := range []string{"ws://public.example/api/agent/ws", "wss://u:p@panel.example/ws", "file:///tmp"} {
		if _, err := downloadURL(server, a.File); err == nil {
			t.Fatal(server)
		}
	}
}
func TestRedirectAndCancellationRetainOld(t *testing.T) {
	u, a, calls := fixture(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	u.server = strings.Replace(redirect.URL, "http://", "ws://", 1)
	if err := u.apply(context.Background(), a); err == nil {
		t.Fatal("redirect accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := u.apply(ctx, a); err == nil {
		t.Fatal("cancellation ignored")
	}
	raw, _ := os.ReadFile(u.binary)
	if string(raw) != "old" || *calls != 0 {
		t.Fatal(fmt.Sprint("old binary lost", *calls))
	}
}
