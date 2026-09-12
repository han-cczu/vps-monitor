package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

// adminToken 登录一次拿 JWT。
func (e *testEnv) adminToken(t *testing.T) string {
	t.Helper()
	resp, body := e.signIn(t, "admin", testPassword)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in status=%d body=%v", resp.StatusCode, body)
	}
	token, _ := body["accessToken"].(string)
	if token == "" {
		t.Fatalf("no accessToken: %v", body)
	}
	return token
}

// getRaw 发一个不带鉴权的 GET，返回状态码、Content-Type 和原始 body（静态下载接口不是 JSON）。
func (e *testEnv) getRaw(t *testing.T, path string) (int, string, string) {
	t.Helper()
	resp, err := e.srv.Client().Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(raw)
}

// newServerBody 是一份合法的创建请求体。
func newServerBody() map[string]any {
	return map[string]any{
		"name":              "hk-01",
		"region":            "hk", // 小写，服务端应归一成 HK
		"group_name":        "亚洲",
		"tags":              []string{"V4", " V6 ", "", "V4"}, // 去空白、丢空串、去重
		"sort_order":        10,
		"public_host":       "hk1.example.com",
		"price":             10.79,
		"currency":          "usd",
		"billing_cycle":     "month",
		"expire_at":         "2026-09-24",
		"auto_renew":        true,
		"traffic_limit":     4_000_000_000_000,
		"traffic_reset_day": 24,
		"traffic_mode":      "max",
		"bandwidth_label":   "1 Gbps",
		"note":              "备注",
	}
}

func (e *testEnv) createServer(t *testing.T, token string, body map[string]any) (int64, string, map[string]any) {
	t.Helper()
	resp, out := e.do(t, http.MethodPost, "/api/servers", token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", resp.StatusCode, out)
	}
	srv, _ := out["server"].(map[string]any)
	id, _ := srv["id"].(float64)
	agentToken, _ := out["token"].(string)
	if id == 0 || agentToken == "" {
		t.Fatalf("create 返回缺字段：%v", out)
	}
	return int64(id), agentToken, out
}

func TestServersRequireAuth(t *testing.T) {
	e := newTestEnv(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/servers"},
		{http.MethodPost, "/api/servers"},
		{http.MethodGet, "/api/servers/1"},
		{http.MethodPut, "/api/servers/1"},
		{http.MethodDelete, "/api/servers/1"},
		{http.MethodPost, "/api/servers/1/token"},
	}
	for _, c := range cases {
		resp, body := e.do(t, c.method, c.path, "", map[string]any{"name": "x"})
		if resp.StatusCode != http.StatusUnauthorized || body["message"] != "unauthorized" {
			t.Fatalf("%s %s: status=%d body=%v", c.method, c.path, resp.StatusCode, body)
		}
	}
}

func TestServerCreateNormalizesAndReturnsTokenOnce(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.PublicURL = "https://panel.example.com" })
	token := e.adminToken(t)

	id, agentToken, created := e.createServer(t, token, newServerBody())

	srv, _ := created["server"].(map[string]any)
	if srv["region"] != "HK" {
		t.Fatalf("region=%v want HK", srv["region"])
	}
	if srv["currency"] != "USD" {
		t.Fatalf("currency=%v want USD", srv["currency"])
	}
	tags, _ := srv["tags"].([]any)
	if len(tags) != 2 || tags[0] != "V4" || tags[1] != "V6" {
		t.Fatalf("tags=%v want [V4 V6]", tags)
	}
	if srv["online"] != false || srv["last_seen"] != nil || srv["host"] != nil {
		t.Fatalf("实时字段占位不对：%v", srv)
	}
	if _, exists := srv["token"]; exists {
		t.Fatalf("server 对象里不该有 token：%v", srv)
	}
	if _, exists := srv["token_hash"]; exists {
		t.Fatalf("server 对象里不该有 token_hash：%v", srv)
	}

	wantCmd := "curl -fsSL https://panel.example.com/install.sh | bash -s -- " +
		"--server wss://panel.example.com/api/agent/ws --token " + agentToken
	if created["install_command"] != wantCmd {
		t.Fatalf("install_command=%v\nwant %v", created["install_command"], wantCmd)
	}

	// 明文 token 只在创建响应里出现一次：列表与详情都拿不到
	resp, list := e.do(t, http.MethodGet, "/api/servers", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d", resp.StatusCode)
	}
	rawList, _ := json.Marshal(list)
	if strings.Contains(string(rawList), agentToken) {
		t.Fatalf("列表里出现了明文 token：%s", rawList)
	}
	servers, _ := list["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("servers=%v", servers)
	}

	resp, one := e.do(t, http.MethodGet, "/api/servers/"+itoa(id), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status=%d", resp.StatusCode)
	}
	rawOne, _ := json.Marshal(one)
	if strings.Contains(string(rawOne), agentToken) {
		t.Fatalf("详情里出现了明文 token：%s", rawOne)
	}

	// 库里存的是哈希，且能按哈希查回来
	got, err := e.db.FindServerByTokenHash(context.Background(), auth.HashAgentToken(agentToken))
	if err != nil || got.ID != id {
		t.Fatalf("按 token 哈希查节点失败：%+v err=%v", got, err)
	}
	if got.TokenHash == agentToken {
		t.Fatal("库里存了明文 token")
	}
}

func TestServerTokenResetInvalidatesOld(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	id, oldAgentToken, _ := e.createServer(t, token, newServerBody())

	resp, out := e.do(t, http.MethodPost, "/api/servers/"+itoa(id)+"/token", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset status=%d body=%v", resp.StatusCode, out)
	}
	newAgentToken, _ := out["token"].(string)
	if newAgentToken == "" || newAgentToken == oldAgentToken {
		t.Fatalf("token 没换：%v", out)
	}
	if cmd, _ := out["install_command"].(string); !strings.Contains(cmd, newAgentToken) {
		t.Fatalf("install_command 里没有新 token：%v", cmd)
	}

	ctx := context.Background()
	if _, err := e.db.FindServerByTokenHash(ctx, auth.HashAgentToken(oldAgentToken)); err == nil {
		t.Fatal("旧 token 仍然有效")
	}
	if got, err := e.db.FindServerByTokenHash(ctx, auth.HashAgentToken(newAgentToken)); err != nil || got.ID != id {
		t.Fatalf("新 token 查不到节点：%+v err=%v", got, err)
	}

	resp, _ = e.do(t, http.MethodPost, "/api/servers/9999/token", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的节点 status=%d want 404", resp.StatusCode)
	}
}

func TestServerValidation(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)

	cases := []struct {
		name  string
		patch map[string]any
	}{
		{"名称为空", map[string]any{"name": "   "}},
		{"名称超长", map[string]any{"name": strings.Repeat("长", 65)}},
		{"地区不是国家码", map[string]any{"region": "china"}},
		{"重置日 40", map[string]any{"traffic_reset_day": 40}},
		{"重置日 -1", map[string]any{"traffic_reset_day": -1}},
		{"账单周期非法", map[string]any{"billing_cycle": "week"}},
		{"统计模式非法", map[string]any{"traffic_mode": "both"}},
		{"到期日格式错", map[string]any{"expire_at": "2026/09/24"}},
		{"到期日不存在", map[string]any{"expire_at": "2026-02-30"}},
		{"公网地址带协议", map[string]any{"public_host": "https://hk1.example.com"}},
		{"公网地址带端口", map[string]any{"public_host": "hk1.example.com:443"}},
		{"价格为负", map[string]any{"price": -1}},
		{"流量上限为负", map[string]any{"traffic_limit": -1}},
		{"标签过多", map[string]any{"tags": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}}},
		{"标签过长", map[string]any{"tags": []string{strings.Repeat("x", 17)}}},
		{"货币非法", map[string]any{"currency": "人民币"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := newServerBody()
			for k, v := range c.patch {
				body[k] = v
			}
			resp, out := e.do(t, http.MethodPost, "/api/servers", token, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v want 400", resp.StatusCode, out)
			}
			if msg, _ := out["message"].(string); msg == "" {
				t.Fatalf("没有错误提示：%v", out)
			}
		})
	}

	if n, err := e.db.CountServers(context.Background()); err != nil || n != 0 {
		t.Fatalf("非法请求不该建出节点：count=%d err=%v", n, err)
	}
}

func TestServerUpdateAndAudit(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	id, agentToken, _ := e.createServer(t, token, newServerBody())

	body := newServerBody()
	body["name"] = "hk-01-renamed"
	body["expire_at"] = nil
	body["auto_renew"] = false
	body["tags"] = []string{}

	resp, out := e.do(t, http.MethodPut, "/api/servers/"+itoa(id), token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status=%d body=%v", resp.StatusCode, out)
	}
	srv, _ := out["server"].(map[string]any)
	if srv["name"] != "hk-01-renamed" || srv["expire_at"] != nil || srv["auto_renew"] != false {
		t.Fatalf("更新后的字段不对：%v", srv)
	}
	if updated, _ := srv["updated_at"].(float64); updated == 0 {
		t.Fatalf("updated_at 没填：%v", srv)
	}

	// token 不受更新影响
	if _, err := e.db.FindServerByTokenHash(context.Background(), auth.HashAgentToken(agentToken)); err != nil {
		t.Fatalf("更新后 token 失效了：%v", err)
	}

	resp, _ = e.do(t, http.MethodPut, "/api/servers/9999", token, body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的节点 status=%d want 404", resp.StatusCode)
	}

	// 删除并检查审计
	resp, _ = e.do(t, http.MethodDelete, "/api/servers/"+itoa(id), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d want 204", resp.StatusCode)
	}
	resp, _ = e.do(t, http.MethodGet, "/api/servers/"+itoa(id), token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删除后还能查到：status=%d", resp.StatusCode)
	}

	entries, err := e.db.ListAudit(context.Background(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"server.create": false, "server.update": false, "server.delete": false}
	for _, entry := range entries {
		if _, ok := want[entry.Action]; ok {
			want[entry.Action] = true
			if entry.TargetType != "server" || entry.TargetID != itoa(id) {
				t.Fatalf("审计目标不对：%+v", entry)
			}
			if entry.Actor != "admin" {
				t.Fatalf("审计 actor=%q want admin", entry.Actor)
			}
		}
		if strings.Contains(entry.Before+entry.After, agentToken) {
			t.Fatalf("审计里出现了明文 token：%+v", entry)
		}
		if strings.Contains(entry.Before+entry.After, "token_hash") {
			t.Fatalf("审计里出现了 token_hash：%+v", entry)
		}
	}
	for action, seen := range want {
		if !seen {
			t.Fatalf("审计里缺 %s：%+v", action, entries)
		}
	}
}

func TestServerTokenResetAudited(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())

	if resp, _ := e.do(t, http.MethodPost, "/api/servers/"+itoa(id)+"/token", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset status=%d", resp.StatusCode)
	}

	entries, err := e.db.ListAudit(context.Background(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Action == "server.token_reset" {
			if entry.Before != "" || entry.After != "" {
				t.Fatalf("token 重置的审计不该带前后值：%+v", entry)
			}
			return
		}
	}
	t.Fatalf("审计里缺 server.token_reset：%+v", entries)
}

func TestServerDeleteCascadesHostInfo(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())

	ctx := context.Background()
	if err := e.db.UpsertHostInfo(ctx, store.HostInfo{ServerID: id, Hostname: "hk-01", Cores: 4}); err != nil {
		t.Fatal(err)
	}

	// host info 有了之后，详情接口应该带上
	_, one := e.do(t, http.MethodGet, "/api/servers/"+itoa(id), token, nil)
	srv, _ := one["server"].(map[string]any)
	host, _ := srv["host"].(map[string]any)
	if host == nil || host["hostname"] != "hk-01" {
		t.Fatalf("详情里没带 host：%v", srv)
	}

	// 列表接口同样
	_, list := e.do(t, http.MethodGet, "/api/servers", token, nil)
	servers, _ := list["servers"].([]any)
	first, _ := servers[0].(map[string]any)
	if h, _ := first["host"].(map[string]any); h == nil || h["cores"] != float64(4) {
		t.Fatalf("列表里没带 host：%v", first)
	}

	if resp, _ := e.do(t, http.MethodDelete, "/api/servers/"+itoa(id), token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatal("delete 失败")
	}
	if _, err := e.db.GetHostInfo(ctx, id); err == nil {
		t.Fatal("host info 没被级联删除")
	}
}

func TestAgentFileServing(t *testing.T) {
	dir := t.TempDir()
	e := newTestEnv(t, func(d *Deps) { d.DataDir = dir })

	// 文件还没放进来：404，而且是纯文本（调用方是 curl | bash）
	status, ctype, body := e.getRaw(t, "/install.sh")
	if status != http.StatusNotFound {
		t.Fatalf("status=%d want 404", status)
	}
	if !strings.HasPrefix(ctype, "text/plain") {
		t.Fatalf("content-type=%q", ctype)
	}
	if strings.Contains(body, "<html") {
		t.Fatalf("404 回了 HTML：%q", body)
	}

	// 放进来之后能下载
	agentDir := filepath.Join(dir, agentDirName)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho install\n"
	if err := os.WriteFile(filepath.Join(agentDir, "install.sh"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ctype, body = e.getRaw(t, "/install.sh")
	if status != http.StatusOK || body != script {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if !strings.HasPrefix(ctype, "text/x-shellscript") {
		t.Fatalf("content-type=%q", ctype)
	}

	// /agent/{file} 走同一份文件
	if status, _, body = e.getRaw(t, "/agent/install.sh"); status != http.StatusOK || body != script {
		t.Fatalf("status=%d body=%q", status, body)
	}

	// 白名单之外一律 404，包括目录穿越
	for _, p := range []string{
		"/agent/vm.db",
		"/agent/jwt.secret",
		"/agent/..%2Fvm.db",
		"/agent/%2e%2e%2fvm.db",
		"/agent/install.sh.bak",
		"/agent/",
	} {
		if status, _, _ = e.getRaw(t, p); status != http.StatusNotFound {
			t.Fatalf("%s: status=%d want 404", p, status)
		}
	}
}

func TestAgentDownloadsArePublic(t *testing.T) {
	dir := t.TempDir()
	e := newTestEnv(t, func(d *Deps) { d.DataDir = dir })

	agentDir := filepath.Join(dir, agentDirName)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "vps-agent-linux-amd64"), []byte("ELF..."), 0o600); err != nil {
		t.Fatal(err)
	}

	// 不带任何 token 也能下（agent 装机时还没有凭据）
	status, ctype, body := e.getRaw(t, "/agent/vps-agent-linux-amd64")
	if status != http.StatusOK || body != "ELF..." {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if ctype != "application/octet-stream" {
		t.Fatalf("content-type=%q", ctype)
	}
}

func TestInstallCommandFallsBackToRequestHost(t *testing.T) {
	e := newTestEnv(t) // 没设 PublicURL
	token := e.adminToken(t)
	_, agentToken, created := e.createServer(t, token, newServerBody())

	cmd, _ := created["install_command"].(string)
	host := strings.TrimPrefix(e.srv.URL, "http://")
	wantPrefix := "curl -fsSL http://" + host + "/install.sh | bash -s -- --server ws://" + host + "/api/agent/ws --token "
	if cmd != wantPrefix+agentToken {
		t.Fatalf("install_command=%q\nwant %q", cmd, wantPrefix+agentToken)
	}
}

func TestWSURL(t *testing.T) {
	cases := map[string]string{
		"https://panel.example.com": "wss://panel.example.com",
		"http://127.0.0.1:9000":     "ws://127.0.0.1:9000",
		"panel.example.com":         "panel.example.com",
	}
	for in, want := range cases {
		if got := wsURL(in); got != want {
			t.Fatalf("wsURL(%q)=%q want %q", in, got, want)
		}
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
