package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/updates"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func updateResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestUpdateCheckAuthenticationAndLocalVersions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "VERSION"), []byte("v0.2.0-observe21"), 0600); err != nil {
		t.Fatal(err)
	}
	e := newTestEnv(t, func(d *Deps) { d.DataDir = dir; d.Version = "v0.2.0-custom" })
	for _, endpoint := range [][2]string{{"GET", "/api/updates"}, {"POST", "/api/updates/check"}} {
		r, _ := e.do(t, endpoint[0], endpoint[1], "", nil)
		if r.StatusCode != 401 {
			t.Fatalf("unprotected %v: %d", endpoint, r.StatusCode)
		}
	}
	r, b := e.do(t, "GET", "/api/updates", e.adminToken(t), nil)
	if r.StatusCode != 200 || b["state"] != "unchecked" || b["panel_version"] != "v0.2.0-custom" || b["agent_version"] != "v0.2.0-observe21" || b["panel_status"] != "unknown" || b["latest"] != nil || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("incorrect local snapshot: %d %v", r.StatusCode, b)
	}
}

// 端到端：登录后发起检查只查询公开发布信息，不下载、不安装、也不下发探针升级。
func TestUpdateCheckReportsVersionsWithoutTouchingAgents(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := []byte("agent binary fixture")
	if err := os.WriteFile(filepath.Join(dir, "agent", "vps-agent-linux-amd64"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "VERSION"), []byte("v0.2.0"), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	requested := []string{}
	calls := 0
	client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		requested = append(requested, r.URL.String())
		if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected upstream request %s", r.URL)
		}
		if r.URL.Query().Get("page") == "2" {
			return updateResponse(200, `[{"tag_name":"v0.0.9"}]`), nil
		}
		resp := updateResponse(200, `[
			{"tag_name":"v9.9.9","prerelease":true},
			{"tag_name":"v9.9.9-rc.1"},
			{"tag_name":"sing-box-v9.9.9"},
			{"tag_name":"v0.9.9","draft":false,"published_at":"2026-09-15T00:00:00Z"}]`)
		resp.Header.Set("Link", `<https://api.github.com/repos/owner/repo/releases?page=2>; rel="next"`)
		return resp, nil
	})}
	checker, err := updates.NewWithClient("owner/repo", client)
	if err != nil {
		t.Fatal(err)
	}

	var h *hub.Hub
	e := newTestEnv(t, func(d *Deps) {
		h = hub.New(d.DB, d.Tokens)
		d.Hub = h
		d.DataDir = dir
		d.Version = "v0.2.2"
		d.Updates = checker
	})
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())
	h.Registry.Update(id, func(st *hub.ServerState) {
		st.Online = true
		st.AgentVersion = "v0.2.0"
		st.AgentUpdateCapable = true
		st.Host.Arch = "x86_64"
	})

	resp, body := e.do(t, "POST", "/api/updates/check", token, nil)
	if resp.StatusCode != 200 || body["state"] != "ok" {
		t.Fatalf("check failed: %d %v", resp.StatusCode, body)
	}
	if body["latest"].(map[string]any)["version"] != "v0.9.9" || body["panel_status"] != "available" || body["agent_status"] != "available" || body["panel_version"] != "v0.2.2" || body["agent_version"] != "v0.2.0" {
		t.Fatalf("wrong versions or status: %v", body)
	}
	if body["repository"] != "owner/repo" || !strings.Contains(body["latest"].(map[string]any)["url"].(string), "/releases/tag/v0.9.9") {
		t.Fatalf("wrong source: %v", body)
	}
	for _, forbidden := range []string{"queued", "skipped", "installed", "downloaded"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("check response claims an action: %v", body)
		}
	}

	// 打开页面与随后的重复检查都走缓存，不再打上游。
	if _, cached := e.do(t, "GET", "/api/updates", token, nil); cached["state"] != "ok" || cached["latest"].(map[string]any)["version"] != "v0.9.9" {
		t.Fatalf("cached snapshot: %v", cached)
	}
	e.do(t, "POST", "/api/updates/check", token, nil)
	mu.Lock()
	got, seen := calls, append([]string(nil), requested...)
	mu.Unlock()
	if got != 2 {
		t.Fatalf("upstream calls: %d %v", got, seen)
	}
	for _, u := range seen {
		if !strings.HasPrefix(u, "https://api.github.com/repos/owner/repo/releases?") {
			t.Fatalf("unexpected upstream url %s", u)
		}
	}

	// 探针产物与节点状态都不因检查而改变。
	raw, err := os.ReadFile(filepath.Join(dir, "agent", "vps-agent-linux-amd64"))
	if err != nil || string(raw) != string(binary) {
		t.Fatalf("agent binary changed: %q %v", raw, err)
	}
	if version, err := os.ReadFile(filepath.Join(dir, "agent", "VERSION")); err != nil || string(version) != "v0.2.0" {
		t.Fatalf("agent version file changed: %q %v", version, err)
	}
	st, ok := h.Registry.Get(id)
	if !ok || !st.Online || st.AgentVersion != "v0.2.0" {
		t.Fatalf("registry changed: %+v", st)
	}
	entries, err := e.db.ListAudit(context.Background(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Action, "agent.") {
			t.Fatalf("check dispatched an agent action: %+v", entry)
		}
	}
}

// 自定义后缀版本即使拿到最新稳定版也不能被排序，只能报无法比较。
func TestUpdateCheckKeepsCustomAgentVersionUnranked(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "VERSION"), []byte("dev"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: updateTransport(func(*http.Request) (*http.Response, error) {
		return updateResponse(200, `[{"tag_name":"v0.2.2"}]`), nil
	})}
	checker, err := updates.NewWithClient("owner/repo", client)
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEnv(t, func(d *Deps) { d.DataDir = dir; d.Version = "v0.2.2"; d.Updates = checker })
	_, body := e.do(t, "POST", "/api/updates/check", e.adminToken(t), nil)
	if body["state"] != "ok" || body["agent_version"] != "dev" || body["agent_status"] != "unknown" || body["panel_status"] != "current" {
		t.Fatalf("custom versions ranked: %v", body)
	}
}
