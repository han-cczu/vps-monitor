package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"vpsmon/proto"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

const testToken = "test-agent-token"

// fakeTokens 是 ClientHub 要的 JWT 校验器：只认一个固定串。
type fakeTokens struct{ valid string }

func (f fakeTokens) Parse(token string) (auth.Principal, error) {
	if token != f.valid {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	return auth.Principal{ID: 1, Name: "admin", Role: auth.RoleAdmin}, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待「%s」超时", what)
}

func wsURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

// newAgentFixture 起一个只挂 agent hub 的测试服务端。
func newAgentFixture(t *testing.T) (*httptest.Server, *AgentHub, *Registry, *fakeStore, *Bus) {
	t.Helper()

	db := &fakeStore{
		servers: []store.Server{sampleServer()},
		byToken: map[string]store.Server{auth.HashAgentToken(testToken): sampleServer()},
	}
	reg := NewRegistry(db)
	bus := &Bus{}
	ah := NewAgentHub(db, reg, bus)

	srv := httptest.NewServer(ah)
	t.Cleanup(srv.Close)
	return srv, ah, reg, db, bus
}

// dialAgent 以 agent 的身份连上去。
func dialAgent(t *testing.T, srv *httptest.Server, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":   {"Bearer " + token},
			"X-Agent-Version": {"0.1.0-test"},
		},
	})
}

func readJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("读消息: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("解析消息 %s: %v", data, err)
	}
}

func writeJSONMsg(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("发送: %v", err)
	}
}

func TestAgentRejectsBadToken(t *testing.T) {
	srv, _, _, _, _ := newAgentFixture(t)

	for _, tc := range []struct{ name, token string }{
		{"错误 token", "wrong-token"},
		{"空 token", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, resp, err := dialAgent(t, srv, tc.token)
			if err == nil {
				t.Fatal("期望握手失败")
			}
			if resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("期望 401，得到 %v", resp)
			}
		})
	}
}

// TestAgentConnectGetsConfig 覆盖"连上就下发 report_interval"。
func TestAgentConnectGetsConfig(t *testing.T) {
	srv, _, _, _, _ := newAgentFixture(t)

	conn, _, err := dialAgent(t, srv, testToken)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer conn.CloseNow()

	var cfg proto.Config
	readJSON(t, conn, &cfg)

	if cfg.Type != proto.TypeConfig {
		t.Errorf("首条下发应当是 config，得到 %q", cfg.Type)
	}
	if cfg.ReportInterval != defaultReportInterval {
		t.Errorf("上报间隔应当是 %d，得到 %d", defaultReportInterval, cfg.ReportInterval)
	}
	if cfg.PingTasks == nil {
		t.Error("ping_tasks 要是空数组而不是 null，agent 侧直接遍历")
	}
}

func TestAgentHelloAndMetricsUpdateState(t *testing.T) {
	srv, _, reg, db, _ := newAgentFixture(t)

	conn, _, err := dialAgent(t, srv, testToken)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer conn.CloseNow()

	var cfg proto.Config
	readJSON(t, conn, &cfg) // 吃掉 config

	writeJSONMsg(t, conn, proto.Hello{
		Type:         proto.TypeHello,
		ProtoVersion: proto.Version,
		Version:      "0.1.0-test",
		Host: proto.HostInfo{
			Hostname: "node-a", OS: "Debian 12", Arch: "x86_64",
			Cores: 2, MemTotal: 1690000000, DiskTotal: 42000000000, IPv4: true,
		},
	})
	waitFor(t, "hello 落库", func() bool { return db.hostCount() > 0 })

	host := db.lastHost()
	if host.Hostname != "node-a" || host.Cores != 2 {
		t.Errorf("静态信息没落对：%+v", host)
	}
	if host.PublicIP == "" {
		t.Error("public_ip 应当由服务端从连接地址填上")
	}
	if host.AgentVersion != "0.1.0-test" {
		t.Errorf("agent_version 应当来自 hello / 请求头，得到 %q", host.AgentVersion)
	}

	writeJSONMsg(t, conn, sampleMetrics())
	waitFor(t, "metrics 入内存态", func() bool {
		st, ok := reg.Get(1)
		return ok && st.Latest != nil
	})

	st, _ := reg.Get(1)
	if !st.Online {
		t.Error("收到 metrics 后应当在线")
	}
	if st.Latest.CPU != 3.02 {
		t.Errorf("metrics 没存对：%+v", st.Latest)
	}
	if st.Host.Hostname != "node-a" {
		t.Errorf("hello 的静态信息应当也留在内存态，得到 %+v", st.Host)
	}
}

// TestAgentSecondConnectionKicksFirst 覆盖验收第 3 条：同一 token 起两个 agent，旧连接被踢。
func TestAgentSecondConnectionKicksFirst(t *testing.T) {
	srv, ah, _, _, _ := newAgentFixture(t)

	first, _, err := dialAgent(t, srv, testToken)
	if err != nil {
		t.Fatalf("第一条握手失败: %v", err)
	}
	defer first.CloseNow()

	var cfg proto.Config
	readJSON(t, first, &cfg)

	second, _, err := dialAgent(t, srv, testToken)
	if err != nil {
		t.Fatalf("第二条握手失败: %v", err)
	}
	defer second.CloseNow()
	readJSON(t, second, &cfg)

	// 旧连接应当被服务端关掉，理由是 superseded
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err = first.Read(ctx)
	if err == nil {
		t.Fatal("旧连接应当被踢掉")
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("期望收到关闭帧，得到 %v", err)
	}
	if ce.Reason != "superseded" {
		t.Errorf("关闭理由应当是 superseded，得到 %q", ce.Reason)
	}

	waitFor(t, "连接表只剩一条", func() bool { return ah.Connected(1) })
}

// TestAgentDisconnectOnTokenReset 覆盖重置 token 后踢掉在线 agent。
func TestAgentDisconnectOnTokenReset(t *testing.T) {
	srv, ah, _, _, _ := newAgentFixture(t)

	conn, _, err := dialAgent(t, srv, testToken)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer conn.CloseNow()

	var cfg proto.Config
	readJSON(t, conn, &cfg)
	waitFor(t, "连接挂上", func() bool { return ah.Connected(1) })

	if !ah.Disconnect(1, "token reset") {
		t.Fatal("Disconnect 应当报告断开了一条连接")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("连接应当已被关闭")
	}
	if ah.Disconnect(1, "again") {
		t.Error("没有连接时 Disconnect 应当返回 false")
	}
}

// TestAgentIgnoresUnknownAndBrokenMessages 保证坏消息不会带崩连接。
func TestAgentIgnoresUnknownAndBrokenMessages(t *testing.T) {
	srv, _, reg, _, _ := newAgentFixture(t)

	conn, _, err := dialAgent(t, srv, testToken)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer conn.CloseNow()

	var cfg proto.Config
	readJSON(t, conn, &cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, []byte("{ 这不是 JSON")); err != nil {
		t.Fatalf("发送坏消息: %v", err)
	}
	writeJSONMsg(t, conn, map[string]any{"type": "core.state", "running": true}) // 步骤 11 才处理
	writeJSONMsg(t, conn, sampleMetrics())

	waitFor(t, "坏消息之后仍能收 metrics", func() bool {
		st, ok := reg.Get(1)
		return ok && st.Latest != nil
	})
}

func TestOfflineScannerMarksStaleAndPublishes(t *testing.T) {
	reg := NewRegistry(&fakeStore{servers: []store.Server{sampleServer()}})
	bus := &Bus{}
	events := bus.Subscribe(4)

	now := time.Unix(1757660000, 0)
	reg.now = func() time.Time { return now }

	scanner := NewOfflineScanner(reg, bus)

	reg.MarkOnline(1)
	if got := scanner.scan(); len(got) != 0 {
		t.Fatalf("刚上报过不该判掉线，得到 %v", got)
	}

	now = now.Add(OfflineTimeout + time.Second)
	got := scanner.scan()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("超时后应当判掉线，得到 %v", got)
	}

	select {
	case ev := <-events:
		if ev.Kind != EventServerOffline || ev.ServerID != 1 {
			t.Errorf("事件不对：%+v", ev)
		}
	default:
		t.Fatal("应当收到掉线事件")
	}

	// 再扫一轮不该重复上报
	if got := scanner.scan(); len(got) != 0 {
		t.Errorf("已经离线的节点不该重复判定，得到 %v", got)
	}

	// 重新上报 → 恢复在线并发事件
	if !reg.MarkOnline(1) {
		t.Error("从离线恢复应当返回 true")
	}
	bus.Publish(Event{Kind: EventServerOnline, ServerID: 1, At: now})
	select {
	case ev := <-events:
		if ev.Kind != EventServerOnline {
			t.Errorf("期望上线事件，得到 %+v", ev)
		}
	default:
		t.Fatal("应当收到上线事件")
	}
}

// TestBusPublishNeverBlocks 保证慢订阅者不会卡住离线扫描。
func TestBusPublishNeverBlocks(t *testing.T) {
	bus := &Bus{}
	bus.Subscribe(1) // 订阅了但从不消费

	done := make(chan struct{})
	go func() {
		for range 100 {
			bus.Publish(Event{Kind: EventServerOffline, ServerID: 1})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢订阅者卡住了")
	}
}

// newClientFixture 起一个只挂 client hub 的测试服务端。
func newClientFixture(t *testing.T) (*httptest.Server, *ClientHub, *Registry) {
	t.Helper()

	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)
	ch := NewClientHub(reg, fakeTokens{valid: "good-jwt"})
	ch.authTimeout = 200 * time.Millisecond // 不在测试里干等 5 秒

	srv := httptest.NewServer(ch)
	t.Cleanup(srv.Close)
	return srv, ch, reg
}

func dialClient(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("浏览器握手失败: %v", err)
	}
	return conn
}

// TestClientClosedWithoutAuthFrame 覆盖验收第 5 条的后半句：不发 auth 会被断开。
func TestClientClosedWithoutAuthFrame(t *testing.T) {
	srv, _, _ := newClientFixture(t)

	conn := dialClient(t, srv)
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("不发 auth 应当被关闭")
	}
	if code := websocket.CloseStatus(err); code != closeUnauthorized {
		t.Errorf("期望关闭码 4001，得到 %v（err=%v）", code, err)
	}
}

func TestClientRejectsBadAuthFrame(t *testing.T) {
	srv, _, _ := newClientFixture(t)

	for _, tc := range []struct {
		name  string
		frame any
	}{
		{"token 不对", authFrame{Type: TypeAuth, Token: "bad"}},
		{"首帧不是 auth", map[string]any{"type": "hello"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := dialClient(t, srv)
			defer conn.CloseNow()

			writeJSONMsg(t, conn, tc.frame)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			_, _, err := conn.Read(ctx)
			if err == nil {
				t.Fatal("应当被关闭")
			}
			if code := websocket.CloseStatus(err); code != closeUnauthorized {
				t.Errorf("期望关闭码 4001，得到 %v", code)
			}
		})
	}
}

// TestClientReceivesSnapshotImmediately 覆盖验收第 5 条：auth 之后能收到 snapshot。
func TestClientReceivesSnapshotImmediately(t *testing.T) {
	srv, _, reg := newClientFixture(t)

	reg.Update(1, func(st *ServerState) {
		st.Online = true
		st.LastSeen = time.Unix(1757660000, 0)
		st.Latest = sampleMetrics()
	})

	conn := dialClient(t, srv)
	defer conn.CloseNow()

	writeJSONMsg(t, conn, authFrame{Type: TypeAuth, Token: "good-jwt"})

	var snap Snapshot
	readJSON(t, conn, &snap)

	if snap.Type != TypeSnapshot {
		t.Errorf("期望 snapshot，得到 %q", snap.Type)
	}
	if len(snap.Servers) != 1 || snap.Servers[0].ID != 1 {
		t.Fatalf("快照内容不对：%+v", snap.Servers)
	}
	if !snap.Servers[0].Online || snap.Servers[0].CPU != 3.02 {
		t.Errorf("快照没带上实时状态：%+v", snap.Servers[0])
	}
}

// TestBroadcastSerializesOnceForTwoClients 覆盖验收第 6 条。
func TestBroadcastSerializesOnceForTwoClients(t *testing.T) {
	srv, ch, _ := newClientFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Broadcast(ctx)

	conns := make([]*websocket.Conn, 2)
	for i := range conns {
		conns[i] = dialClient(t, srv)
		defer conns[i].CloseNow()

		writeJSONMsg(t, conns[i], authFrame{Type: TypeAuth, Token: "good-jwt"})

		var snap Snapshot
		readJSON(t, conns[i], &snap) // 连上立刻推的那一帧
	}
	waitFor(t, "两个客户端都挂上", func() bool { return ch.Count() == 2 })

	before := ch.Frames()

	// 两个客户端各自再收一帧广播
	for _, conn := range conns {
		var snap Snapshot
		readJSON(t, conn, &snap)
		if snap.Type != TypeSnapshot {
			t.Fatalf("期望 snapshot，得到 %q", snap.Type)
		}
	}

	if delta := ch.Frames() - before; delta > 2 {
		t.Errorf("两个客户端各收一帧，序列化次数不该超过 2，实际 %d", delta)
	}
}

// TestBroadcastSkipsWorkWithoutClients 保证没人看时不查库、不序列化。
func TestBroadcastSkipsWorkWithoutClients(t *testing.T) {
	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)
	ch := NewClientHub(reg, fakeTokens{valid: "good-jwt"})

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	ch.Broadcast(ctx)

	if ch.Frames() != 0 {
		t.Errorf("没有客户端时不该广播，实际 %d 帧", ch.Frames())
	}
	if db.listCount() != 0 {
		t.Errorf("没有客户端时不该查库，实际 %d 次", db.listCount())
	}
}
