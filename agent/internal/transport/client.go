// Package transport 负责 agent 与服务端之间的 WebSocket 连接：拨号、鉴权、心跳、断线重连。
//
// 设计上连接与采集解耦：采集循环只管按间隔产出 metrics 并调用 Send，
// 没连上就丢弃当前这一帧（累计流量在计数器里，不会因此丢失）。
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"vpsmon/proto"
)

// ErrNotConnected 表示当前没有可用连接，这一帧被丢弃。
var ErrNotConnected = errors.New("transport: 未连接")

const (
	dialTimeout     = 15 * time.Second
	writeTimeout    = 5 * time.Second
	pingInterval    = 20 * time.Second
	pingTimeout     = 10 * time.Second
	helloInterval   = 5 * time.Minute // 静态信息定期重发，服务端幂等更新
	maxMessageBytes = 1 << 20         // 服务端下发的 core.apply 最大也就几十 KB

	minBackoff = time.Second
	maxBackoff = 60 * time.Second

	// 连接活过这个时长才算“连上了”，退避才归零。
	// 否则遇到“能连上但立刻被关”（token 刚被撤、服务端在重启）会变成每秒重连一次。
	stableConnection = 30 * time.Second
)

// Options 是新建客户端需要的参数。
type Options struct {
	Server  string // wss://panel.example.com/api/agent/ws
	Token   string
	Version string // agent 版本，放在 hello 与请求头里

	// Hello 每次连上（以及每 5 分钟）被调用一次，返回要上报的静态信息。
	// 做成回调而不是固定值：重连时机器可能已经换了内核或加了内存。
	Hello func() proto.Hello

	// OnMessage 收到服务端消息时调用，msgType 已经解出来了。
	OnMessage func(msgType string, raw []byte)
}

// Client 是一个自动重连的 WebSocket 客户端。
type Client struct {
	opts Options

	mu   sync.Mutex
	conn *websocket.Conn
}

// New 按 opts 新建客户端，不会立刻连接（连接在 Run 里）。
func New(opts Options) *Client {
	return &Client{opts: opts}
}

// Run 一直跑到 ctx 结束：连接 → 发 hello → 收发 → 断开 → 退避重连。
//
// 退避从 1 秒翻倍到 60 秒封顶，连上后归零。日志按退避间隔打，不会刷屏。
func (c *Client) Run(ctx context.Context) {
	backoff := minBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		lived, err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if lived >= stableConnection {
			backoff = minBackoff
		}

		slog.Warn("连接断开，准备重连", "err", err, "lived", lived.Truncate(time.Second).String(), "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session 跑完一次连接的生命周期，返回这条连接活了多久（没连上是 0），
// 用来决定重连退避要不要归零。
func (c *Client) session(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	conn, resp, err := websocket.Dial(dialCtx, c.opts.Server, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":   {"Bearer " + c.opts.Token},
			"X-Agent-Version": {c.opts.Version},
		},
		CompressionMode: websocket.CompressionContextTakeover,
	})
	dialCancel()
	if err != nil {
		// 401 是配置问题，重试多少次都没用，日志要说清楚
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return 0, fmt.Errorf("服务端拒绝了 token（401），检查 /etc/vps-agent/config.yaml 里的 token: %w", err)
		}
		return 0, err
	}
	defer conn.CloseNow()

	connectedAt := time.Now()

	conn.SetReadLimit(maxMessageBytes)
	c.setConn(conn)
	defer c.setConn(nil)

	slog.Info("已连上服务端", "server", c.opts.Server)

	if err := c.write(ctx, conn, c.opts.Hello()); err != nil {
		return time.Since(connectedAt), fmt.Errorf("发送 hello: %w", err)
	}

	go c.pingLoop(ctx, conn)
	go c.helloLoop(ctx, conn)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return time.Since(connectedAt), err
		}
		c.dispatch(data)
	}
}

// dispatch 解出消息类型后交给 OnMessage。解不开就记 WARN 丢弃，不断开连接。
func (c *Client) dispatch(raw []byte) {
	var env proto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		slog.Warn("收到无法解析的消息", "err", err, "bytes", len(raw))
		return
	}
	if env.Type == "" {
		slog.Warn("收到没有 type 的消息", "bytes", len(raw))
		return
	}
	if c.opts.OnMessage != nil {
		c.opts.OnMessage(env.Type, raw)
	}
}

// pingLoop 定期发 WebSocket ping。超时说明链路已经死了（NAT 超时、对端假死），
// 关掉连接让 Run 去重连。
func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("心跳超时，断开连接", "err", err)
				}
				conn.CloseNow()
				return
			}
		}
	}
}

// helloLoop 每 5 分钟重发一次静态信息。
func (c *Client) helloLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(helloInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.write(ctx, conn, c.opts.Hello()); err != nil {
				if ctx.Err() == nil {
					slog.Warn("重发 hello 失败", "err", err)
				}
				return
			}
		}
	}
}

// Send 把 v 序列化成 JSON 发出去。没连上返回 ErrNotConnected，调用方丢弃这一帧即可。
func (c *Client) Send(v any) error {
	conn := c.currentConn()
	if conn == nil {
		return ErrNotConnected
	}
	return c.write(context.Background(), conn, v)
}

// Connected 报告当前是否有可用连接。
func (c *Client) Connected() bool {
	return c.currentConn() != nil
}

func (c *Client) write(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("序列化消息: %w", err)
	}

	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *Client) currentConn() *websocket.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}
