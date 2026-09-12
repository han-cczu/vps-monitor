package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
	"vpsmon/server/web"
)

const (
	testSecret   = "0123456789abcdef0123456789abcdef"
	testPassword = "initial-password-1"
)

type testEnv struct {
	srv *httptest.Server
	db  *store.DB
}

func newTestEnv(t *testing.T, opts ...func(*Deps)) *testEnv {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "vm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(context.Background(), "admin", hash); err != nil {
		t.Fatal(err)
	}

	tokens, err := auth.NewTokens(testSecret, auth.TokenTTL)
	if err != nil {
		t.Fatal(err)
	}

	deps := Deps{
		DB:      db,
		Tokens:  tokens,
		Limiter: auth.NewLimiter(auth.DefaultMaxFailures, auth.DefaultWindow, auth.DefaultLockout),
		Version: "test",
		Web:     web.Handler(),
	}
	for _, o := range opts {
		o(&deps)
	}

	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)

	return &testEnv{srv: srv, db: db}
}

func (e *testEnv) do(t *testing.T, method, path, token string, body any) (*http.Response, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("%s %s: bad json %q: %v", method, path, raw, err)
		}
	}
	return resp, parsed
}

func (e *testEnv) signIn(t *testing.T, username, password string) (*http.Response, map[string]any) {
	t.Helper()
	return e.do(t, http.MethodPost, "/api/auth/sign-in", "", map[string]string{"username": username, "password": password})
}

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	resp, body := e.do(t, http.MethodGet, "/api/health", "", nil)
	if resp.StatusCode != 200 || body["ok"] != true || body["version"] != "test" {
		t.Fatalf("status=%d body=%v", resp.StatusCode, body)
	}
}

func TestSignInSuccessAndMe(t *testing.T) {
	e := newTestEnv(t)

	resp, body := e.signIn(t, "admin", testPassword)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%v", resp.StatusCode, body)
	}
	token, _ := body["accessToken"].(string)
	if token == "" {
		t.Fatalf("no accessToken: %v", body)
	}
	user, _ := body["user"].(map[string]any)
	if user["displayName"] != "admin" || user["email"] != "admin" || user["role"] != "admin" || user["photoURL"] != nil {
		t.Fatalf("user=%v", user)
	}
	if _, ok := user["id"].(float64); !ok {
		t.Fatalf("user.id missing: %v", user)
	}
	if exp, _ := body["expiresAt"].(float64); int64(exp) < time.Now().Add(11*time.Hour).Unix() {
		t.Fatalf("expiresAt too early: %v", body["expiresAt"])
	}

	// starter 的 email 字段名也接受
	resp, _ = e.do(t, http.MethodPost, "/api/auth/sign-in", "", map[string]string{"email": "admin", "password": testPassword})
	if resp.StatusCode != 200 {
		t.Fatalf("email alias: status=%d", resp.StatusCode)
	}

	// /me
	resp, body = e.do(t, http.MethodGet, "/api/auth/me", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("me: status=%d body=%v", resp.StatusCode, body)
	}
	if u, _ := body["user"].(map[string]any); u["email"] != "admin" {
		t.Fatalf("me user=%v", body)
	}

	// 审计里有 sign_in
	entries, _ := e.db.ListAudit(context.Background(), 10, 0)
	found := false
	for _, en := range entries {
		if en.Action == "auth.sign_in" && en.Actor == "admin" && en.IP == "127.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no auth.sign_in audit entry: %+v", entries)
	}
}

func TestSignInRejectsBadCredentialsAndInput(t *testing.T) {
	e := newTestEnv(t)

	resp, body := e.signIn(t, "admin", "wrong-password")
	if resp.StatusCode != 401 || body["message"] != "用户名或密码错误" {
		t.Fatalf("wrong password: status=%d body=%v", resp.StatusCode, body)
	}
	resp, body = e.signIn(t, "nobody", testPassword)
	if resp.StatusCode != 401 || body["message"] != "用户名或密码错误" {
		t.Fatalf("unknown user: status=%d body=%v", resp.StatusCode, body)
	}
	resp, _ = e.signIn(t, "", "")
	if resp.StatusCode != 400 {
		t.Fatalf("empty: status=%d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/auth/sign-in", strings.NewReader("{not json"))
	raw, _ := e.srv.Client().Do(req)
	raw.Body.Close()
	if raw.StatusCode != 400 {
		t.Fatalf("bad json: status=%d", raw.StatusCode)
	}
}

func TestSignInRateLimit(t *testing.T) {
	e := newTestEnv(t)

	for i := 1; i <= auth.DefaultMaxFailures; i++ {
		resp, _ := e.signIn(t, "admin", "wrong")
		if resp.StatusCode != 401 {
			t.Fatalf("attempt %d: status=%d want 401", i, resp.StatusCode)
		}
	}

	// 第 6 次起 429，连正确密码也不行
	resp, body := e.signIn(t, "admin", testPassword)
	if resp.StatusCode != 429 {
		t.Fatalf("locked: status=%d body=%v", resp.StatusCode, body)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Fatal("missing Retry-After")
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "尝试次数过多") {
		t.Fatalf("message=%q", msg)
	}
}

func TestMeRequiresToken(t *testing.T) {
	e := newTestEnv(t)

	resp, body := e.do(t, http.MethodGet, "/api/auth/me", "", nil)
	if resp.StatusCode != 401 || body["message"] != "unauthorized" {
		t.Fatalf("no token: status=%d body=%v", resp.StatusCode, body)
	}
	resp, _ = e.do(t, http.MethodGet, "/api/auth/me", "garbage", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("bad token: status=%d", resp.StatusCode)
	}
	resp, _ = e.do(t, http.MethodPost, "/api/auth/password", "", map[string]string{"oldPassword": "a", "newPassword": "b"})
	if resp.StatusCode != 401 {
		t.Fatalf("password without token: status=%d", resp.StatusCode)
	}
}

func TestChangePassword(t *testing.T) {
	e := newTestEnv(t)

	_, body := e.signIn(t, "admin", testPassword)
	token := body["accessToken"].(string)

	cases := []struct {
		name   string
		old    string
		new    string
		status int
	}{
		{"wrong old", "nope", "a-long-enough-password", 400},
		{"too short", testPassword, "short", 400},
		{"same as old", testPassword, testPassword, 400},
		{"ok", testPassword, "brand-new-password-2", 204},
	}
	for _, c := range cases {
		resp, b := e.do(t, http.MethodPost, "/api/auth/password", token, map[string]string{"oldPassword": c.old, "newPassword": c.new})
		if resp.StatusCode != c.status {
			t.Fatalf("%s: status=%d want %d body=%v", c.name, resp.StatusCode, c.status, b)
		}
	}

	// 旧密码失效、新密码可用
	if resp, _ := e.signIn(t, "admin", testPassword); resp.StatusCode != 401 {
		t.Fatalf("old password still works: %d", resp.StatusCode)
	}
	if resp, _ := e.signIn(t, "admin", "brand-new-password-2"); resp.StatusCode != 200 {
		t.Fatalf("new password rejected: %d", resp.StatusCode)
	}

	// 审计
	entries, _ := e.db.ListAudit(context.Background(), 20, 0)
	found := false
	for _, en := range entries {
		if en.Action == "auth.password_change" && en.Actor == "admin" && en.TargetType == "user" && en.IP == "127.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no auth.password_change audit entry: %+v", entries)
	}
}

func TestNotFoundAndSPAFallback(t *testing.T) {
	e := newTestEnv(t)

	resp, body := e.do(t, http.MethodGet, "/api/nope", "", nil)
	if resp.StatusCode != 404 || body["message"] != "not found" {
		t.Fatalf("api 404: status=%d body=%v", resp.StatusCode, body)
	}
	// 已注册路径用错方法：405，不需要 token
	resp, _ = e.do(t, http.MethodDelete, "/api/auth/me", "", nil)
	if resp.StatusCode != 405 {
		t.Fatalf("method not allowed: status=%d", resp.StatusCode)
	}

	for _, p := range []string{"/", "/dashboard/xxx", "/auth/sign-in", "/index.html"} {
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+p, nil)
		r, err := e.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(raw), "<!doctype html>") {
			t.Fatalf("%s: status=%d ct=%q", p, r.StatusCode, r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: cache-control=%q", p, r.Header.Get("Cache-Control"))
		}
	}
}
