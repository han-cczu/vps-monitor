package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"vpsmon/proto"
)

// wsURL 把 httptest 的 http:// 地址换成 ws://。
func wsURL(httpURL string) string {
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}

func testHello() proto.Hello {
	return proto.Hello{
		Type:         proto.TypeHello,
		ProtoVersion: proto.Version,
		Version:      "test",
		Host:         proto.HostInfo{Hostname: "test-host", Cores: 2},
	}
}

func TestClientHandshakeAndMessages(t *testing.T) {
	var (
		gotAuth    string
		gotVersion string
		helloCh    = make(chan proto.Hello, 1)
		metricsCh  = make(chan proto.Metrics, 1)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Agent-Version")

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		// 第一条必须是 hello
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var hello proto.Hello
		if err := json.Unmarshal(raw, &hello); err != nil {
			t.Errorf("hello 解析失败: %v", err)
			return
		}
		helloCh <- hello

		// 下发一条 config
		cfg, _ := json.Marshal(proto.Config{Type: proto.TypeConfig, ReportInterval: 5})
		if err := conn.Write(ctx, websocket.MessageText, cfg); err != nil {
			return
		}

		// 等 agent 发上来的 metrics
		_, raw, err = conn.Read(ctx)
		if err != nil {
			return
		}
		var m proto.Metrics
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("metrics 解析失败: %v", err)
			return
		}
		metricsCh <- m

		<-ctx.Done()
	}))
	defer srv.Close()

	received := make(chan string, 4)
	client := New(Options{
		Server:  wsURL(srv.URL),
		Token:   "token-abc",
		Version: "test",
		Hello:   testHello,
		OnMessage: func(msgType string, _ []byte) {
			received <- msgType
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)

	select {
	case hello := <-helloCh:
		if hello.Type != proto.TypeHello || hello.Host.Hostname != "test-host" || hello.ProtoVersion != proto.Version {
			t.Fatalf("hello 内容不对：%+v", hello)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没收到 hello")
	}

	if gotAuth != "Bearer token-abc" {
		t.Errorf("Authorization=%q", gotAuth)
	}
	if gotVersion != "test" {
		t.Errorf("X-Agent-Version=%q", gotVersion)
	}

	select {
	case msgType := <-received:
		if msgType != proto.TypeConfig {
			t.Fatalf("收到的消息类型=%q want config", msgType)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没收到服务端下发的 config")
	}

	// 连上之后 Send 应该成功
	if err := client.Send(proto.Metrics{Type: proto.TypeMetrics, TS: 1}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case m := <-metricsCh:
		if m.Type != proto.TypeMetrics || m.TS != 1 {
			t.Fatalf("服务端收到的 metrics 不对：%+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("服务端没收到 metrics")
	}
}

func TestSendWithoutConnection(t *testing.T) {
	client := New(Options{Server: "ws://127.0.0.1:1/api/agent/ws", Hello: testHello})

	if client.Connected() {
		t.Fatal("还没连就报 Connected")
	}
	err := client.Send(proto.Metrics{Type: proto.TypeMetrics})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestReconnectsAfterServerClose(t *testing.T) {
	var connects atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connects.Add(1)
		// 收下 hello 就把连接关掉，模拟服务端重启
		_, _, _ = conn.Read(r.Context())
		conn.Close(websocket.StatusGoingAway, "bye")
	}))
	defer srv.Close()

	client := New(Options{Server: wsURL(srv.URL), Token: "t", Version: "test", Hello: testHello})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)

	// 第一次退避是 1 秒，两次连接大约 1–2 秒内发生
	deadline := time.After(8 * time.Second)
	for connects.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("没有重连，connects=%d", connects.Load())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestSessionReportsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := New(Options{Server: wsURL(srv.URL), Token: "bad", Version: "test", Hello: testHello})

	lived, err := client.session(context.Background())
	if err == nil {
		t.Fatal("401 应该报错")
	}
	if lived != 0 {
		t.Errorf("没连上，lived 应为 0，得到 %v", lived)
	}
	// 错误信息要说清楚是 token 的问题，不然用户看着一串握手错误无从下手
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("错误信息里没提 token：%v", err)
	}
}

func TestDispatchIgnoresGarbage(t *testing.T) {
	var mu sync.Mutex
	var got []string

	client := New(Options{
		Hello: testHello,
		OnMessage: func(msgType string, _ []byte) {
			mu.Lock()
			got = append(got, msgType)
			mu.Unlock()
		},
	})

	client.dispatch([]byte("not json"))
	client.dispatch([]byte(`{"no_type": 1}`))
	client.dispatch([]byte(`{"type": "config", "report_interval": 3}`))

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != proto.TypeConfig {
		t.Fatalf("只该分发合法消息，得到 %v", got)
	}
}
