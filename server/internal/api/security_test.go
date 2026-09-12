package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/config"
)

// middlewareWrap 复刻 requestLogger 对响应 writer 的包装，让 recoverer 能看出响应是否已开始写。
func middlewareWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(middleware.NewWrapResponseWriter(w, r.ProtoMajor), r)
	})
}

// postWithHeaders 发一个带自定义请求头的 JSON POST。
func (e *testEnv) postWithHeaders(t *testing.T, path string, headers map[string]string, body any) (*http.Response, map[string]any) {
	t.Helper()

	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if len(payload) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(payload, &parsed)
	}
	return resp, parsed
}

func badLogin(ip string) map[string]string {
	return map[string]string{
		"True-Client-IP":  ip,
		"X-Real-IP":       ip,
		"X-Forwarded-For": ip,
	}
}

// 直连模式下，任何来路不明的 IP 头都不能用来绕开登录限速。
// chi 的 RealIP 会无条件采信 True-Client-IP / X-Real-IP，正是因此被官方弃用；
// 这里锁住"每次换一个伪造 IP 也照样被锁"的行为。
func TestSignInRateLimitIgnoresSpoofedHeaders(t *testing.T) {
	e := newTestEnv(t) // 默认零值 = 直连

	for i := 1; i <= auth.DefaultMaxFailures; i++ {
		ip := fmt.Sprintf("10.9.9.%d", i)
		resp, _ := e.postWithHeaders(t, "/api/auth/sign-in", badLogin(ip),
			map[string]string{"username": "admin", "password": "wrong"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d (spoofed %s): status=%d want 401", i, ip, resp.StatusCode)
		}
	}

	// 再换一个全新的伪造 IP，仍然应该被锁住
	resp, body := e.postWithHeaders(t, "/api/auth/sign-in", badLogin("203.0.113.77"),
		map[string]string{"username": "admin", "password": testPassword})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("spoofed header bypassed the rate limit: status=%d body=%v", resp.StatusCode, body)
	}
}

// 审计里的 IP 也不能被请求头污染。
func TestAuditIPIgnoresSpoofedHeaders(t *testing.T) {
	e := newTestEnv(t)

	resp, _ := e.postWithHeaders(t, "/api/auth/sign-in", badLogin("198.51.100.5"),
		map[string]string{"username": "admin", "password": testPassword})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: status=%d", resp.StatusCode)
	}

	entries, err := e.db.ListAudit(context.Background(), 10, 0)
	if err != nil || len(entries) == 0 {
		t.Fatalf("audit: %v entries=%d", err, len(entries))
	}
	if got := entries[0].IP; got != "127.0.0.1" {
		t.Fatalf("audit ip=%q want 127.0.0.1 (spoofed header must be ignored)", got)
	}
}

// 配了可信代理层数之后，X-Forwarded-For 才生效，且不同客户端 IP 各算各的。
func TestTrustedProxyUsesForwardedFor(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.TrustedProxies = config.TrustedProxies{Count: 1}
	})

	// httptest 客户端直连，所以 XFF 里只要有 1 项，右数第 1 个就是它
	victim := map[string]string{"X-Forwarded-For": "1.2.3.4"}
	for i := 1; i <= auth.DefaultMaxFailures; i++ {
		resp, _ := e.postWithHeaders(t, "/api/auth/sign-in", victim,
			map[string]string{"username": "admin", "password": "wrong"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d want 401", i, resp.StatusCode)
		}
	}
	resp, _ := e.postWithHeaders(t, "/api/auth/sign-in", victim,
		map[string]string{"username": "admin", "password": testPassword})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked client: status=%d want 429", resp.StatusCode)
	}

	// 另一个客户端 IP 不受牵连
	resp, body := e.postWithHeaders(t, "/api/auth/sign-in", map[string]string{"X-Forwarded-For": "5.6.7.8"},
		map[string]string{"username": "admin", "password": testPassword})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("other client locked out too: status=%d body=%v", resp.StatusCode, body)
	}

	entries, _ := e.db.ListAudit(context.Background(), 10, 0)
	if len(entries) == 0 || entries[0].IP != "5.6.7.8" {
		t.Fatalf("audit ip=%v want 5.6.7.8", entries)
	}
}

// 改密码接口的旧密码猜测同样要限速。
func TestChangePasswordRateLimited(t *testing.T) {
	e := newTestEnv(t)

	_, body := e.signIn(t, "admin", testPassword)
	token, _ := body["accessToken"].(string)
	if token == "" {
		t.Fatal("no token")
	}

	for i := 1; i <= auth.DefaultMaxFailures; i++ {
		resp, b := e.do(t, http.MethodPost, "/api/auth/password", token,
			map[string]string{"oldPassword": "wrong-old", "newPassword": "a-brand-new-password"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("attempt %d: status=%d want 400 body=%v", i, resp.StatusCode, b)
		}
	}

	// 锁上之后，连正确的旧密码也换不了
	resp, b := e.do(t, http.MethodPost, "/api/auth/password", token,
		map[string]string{"oldPassword": testPassword, "newPassword": "a-brand-new-password"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("not rate limited: status=%d body=%v", resp.StatusCode, b)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}

	// 密码确实没被改掉
	if resp, _ := e.signIn(t, "admin", testPassword); resp.StatusCode != http.StatusOK {
		t.Fatalf("original password broken: %d", resp.StatusCode)
	}
}

// panic 要变成统一格式的 JSON 500，而不是 chi 默认的空 body + 彩色堆栈。
func TestRecovererWritesJSON(t *testing.T) {
	h := recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/whatever", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type=%q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not json: %q", rec.Body.String())
	}
	if body["message"] != "服务器内部错误" {
		t.Fatalf("message=%q", body["message"])
	}
}

// 已经开始写响应之后再 panic，不应该二次写入（避免 superfluous WriteHeader）。
func TestRecovererKeepsPartialResponse(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"partial":true}`))
		panic("boom after write")
	})
	// recoverer 要能认出被 chi 包装过的 writer
	h := middlewareWrap(recoverer(inner))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/whatever", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status=%d want 418 (already written)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"partial":true`) {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// 静态资源与 index.html 的缓存策略：带 hash 的 assets 一年 immutable，index.html 不缓存。
func TestStaticCacheHeaders(t *testing.T) {
	e := newTestEnv(t)

	resp, _ := e.do(t, http.MethodGet, "/index.html", "", nil)
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index.html cache-control=%q want no-cache", cc)
	}

	// 仓库里内嵌的是占位 index.html，没有 assets/ 目录；
	// 没命中的路径必须回退成 index.html（no-cache），不能带 immutable。
	resp, _ = e.do(t, http.MethodGet, "/assets/does-not-exist.js", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("missing asset: status=%d want 200 (SPA fallback)", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "public, max-age=31536000, immutable" {
		t.Fatal("SPA fallback must not be served with an immutable cache header")
	}
}
