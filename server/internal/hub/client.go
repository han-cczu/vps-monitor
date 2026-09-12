package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"vpsmon/server/internal/auth"
)

const (
	// TypeAuth 是浏览器连上后必须发的第一帧。
	//
	// 浏览器的 WebSocket API 没法自定义请求头，带不了 Authorization，
	// 所以 JWT 走首帧而不是 header。放在 URL 查询串里更糟——那会进访问日志。
	TypeAuth = "auth"

	// clientAuthTimeout 是等待首帧 auth 的上限。
	clientAuthTimeout = 5 * time.Second

	// closeUnauthorized 是鉴权失败时的关闭码（设计方案 §4.4 指定 4001）。
	closeUnauthorized = websocket.StatusCode(4001)

	// broadcastInterval 是快照广播周期。
	broadcastInterval = time.Second

	// clientWriteTimeout 是单次广播的写超时，超时即踢掉该连接。
	clientWriteTimeout = time.Second

	// clientSendBuffer 是每个浏览器连接的发送队列长度。
	// 快照是全量的，积压两帧以上没有意义，短队列反而能让慢客户端早点被踢掉。
	clientSendBuffer = 4

	// clientMaxMessage 是浏览器能发上来的单帧上限：只有一条 auth，给 8 KiB 绰绰有余。
	clientMaxMessage = 8 << 10
)

// tokenParser 是 ClientHub 需要的鉴权能力（*auth.Tokens 实现它）。
type tokenParser interface {
	Parse(token string) (auth.Principal, error)
}

// authFrame 是浏览器的首帧。
type authFrame struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// ClientHub 处理 GET /api/ws：浏览器接入与每秒快照广播。
type ClientHub struct {
	reg    *Registry
	tokens tokenParser

	// authTimeout 默认是 clientAuthTimeout，测试里调短避免干等 5 秒。
	authTimeout time.Duration

	mu    sync.RWMutex
	conns map[*clientConn]struct{}

	// frames 统计广播帧数（= 序列化次数），验收"两个客户端每秒只序列化一次"用。
	frames atomic.Int64
}

type clientConn struct {
	conn   *websocket.Conn
	send   chan []byte
	cancel context.CancelFunc
	once   sync.Once
	closed chan struct{}
}

// NewClientHub 新建浏览器接入层。
func NewClientHub(reg *Registry, tokens tokenParser) *ClientHub {
	return &ClientHub{
		reg:         reg,
		tokens:      tokens,
		authTimeout: clientAuthTimeout,
		conns:       map[*clientConn]struct{}{},
	}
}

// ServeHTTP 处理浏览器的 WebSocket 接入。
func (h *ClientHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 这里用默认的同源校验（Origin 的 host 必须等于请求的 Host）：
	// 生产是同源下发前端，开发时 Vite 代理 /api/ws 没开 changeOrigin，
	// Host 与 Origin 都是 :8080，两种情况都能过。
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		slog.Warn("浏览器连接升级失败", "err", err)
		return
	}
	conn.SetReadLimit(clientMaxMessage)

	p, err := h.authenticate(conn)
	if err != nil {
		slog.Info("浏览器鉴权失败，按 4001 关闭", "err", err)
		// 收尾整个交给协程，handler 立刻返回，原因有两条：
		//   1. Close 要等对端回 close 帧，而首帧超时时那个读协程还占着 readMu，
		//      同步调用会把 handler 钉满 5 秒（审查实测：加上等待首帧共 10 秒）。
		//      /api/ws 在鉴权之前就完成了升级，这条路径对谁都敞开。
		//   2. handler 一返回，net/http 会取消请求 ctx、defer 也会 CloseNow，
		//      两者都会把还没发出去的 4001 连同 socket 一起拆掉。
		go func() {
			_ = conn.Close(closeUnauthorized, "unauthorized")
			conn.CloseNow()
		}()
		return
	}

	// 鉴权过了才建 ctx：上面那条失败路径不能挂在请求 ctx 上。
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer conn.CloseNow()

	cc := &clientConn{
		conn:   conn,
		send:   make(chan []byte, clientSendBuffer),
		cancel: cancel,
		closed: make(chan struct{}),
	}
	h.add(cc)
	defer h.remove(cc)

	slog.Info("client connected", "user", p.Name, "clients", h.Count())
	defer func() { slog.Info("client disconnected", "user", p.Name) }()

	go cc.writeLoop(ctx)

	// 连上先推一帧，不用等下一个整秒——打开页面立刻有数据。
	if payload, err := json.Marshal(h.reg.SnapshotFrame(ctx)); err == nil {
		cc.trySend(payload)
	}

	// 读循环只为了感知对端关闭：浏览器除了首帧 auth 不再发任何东西，
	// 收到什么都丢掉（留给后续步骤扩展订阅指令）。
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

// authenticate 读首帧并校验 JWT。超时、格式不对、token 无效都返回错误。
//
// 这里不能用 context.WithTimeout 套 Read：coder/websocket 在 Read 的 ctx 过期时
// 会直接把连接拆掉，于是"5 秒没发 auth"的对端只会看到 EOF，收不到约定的 4001。
// 改成自己计时，超时后连接还活着，调用方能把 4001 正常发出去。
//
// 读协程也不能挂在请求 ctx 上：handler 返回时 net/http 会取消它，同样会拆掉连接。
// 连接最终一定会被关闭（成功路径 CloseNow，失败路径异步 Close），协程随之退出。
func (h *ClientHub) authenticate(conn *websocket.Conn) (auth.Principal, error) {
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		_, data, err := conn.Read(context.Background())
		done <- readResult{data, err}
	}()

	timer := time.NewTimer(h.authTimeout)
	defer timer.Stop()

	var data []byte
	select {
	case <-timer.C:
		return auth.Principal{}, errAuthTimeout
	case res := <-done:
		if res.err != nil {
			return auth.Principal{}, res.err
		}
		data = res.data
	}

	var frame authFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return auth.Principal{}, err
	}
	if frame.Type != TypeAuth {
		return auth.Principal{}, errUnexpectedFrame
	}
	return h.tokens.Parse(frame.Token)
}

// Broadcast 每秒把全量快照推给所有在线客户端，一直跑到 ctx 结束。
//
// 一帧只序列化一次，再分发给每条连接——十几台节点、几个客户端的规模下，
// 序列化才是成本，写 socket 不是。
func (h *ClientHub) Broadcast(ctx context.Context) {
	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if h.Count() == 0 {
				continue // 没人看就不查库、不序列化
			}
			payload, err := json.Marshal(h.reg.SnapshotFrame(ctx))
			if err != nil {
				slog.Error("快照序列化失败", "err", err)
				continue
			}
			h.frames.Add(1)
			h.fanout(payload)
		}
	}
}

// Frames 返回累计广播帧数（序列化次数）。
func (h *ClientHub) Frames() int64 { return h.frames.Load() }

// Count 返回当前在线的浏览器连接数。
func (h *ClientHub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

func (h *ClientHub) fanout(payload []byte) {
	h.mu.RLock()
	conns := make([]*clientConn, 0, len(h.conns))
	for cc := range h.conns {
		conns = append(conns, cc)
	}
	h.mu.RUnlock()

	for _, cc := range conns {
		cc.trySend(payload)
	}
}

func (h *ClientHub) add(cc *clientConn) {
	h.mu.Lock()
	h.conns[cc] = struct{}{}
	h.mu.Unlock()
}

func (h *ClientHub) remove(cc *clientConn) {
	h.mu.Lock()
	delete(h.conns, cc)
	h.mu.Unlock()
	cc.close()
}

// trySend 把一帧放进发送队列；队列满说明这个客户端跟不上，直接断开。
func (cc *clientConn) trySend(payload []byte) {
	select {
	case cc.send <- payload:
	case <-cc.closed:
	default:
		slog.Warn("客户端消费不过来，断开连接")
		cc.close()
	}
}

func (cc *clientConn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-cc.closed:
			return
		case payload := <-cc.send:
			writeCtx, cancel := context.WithTimeout(ctx, clientWriteTimeout)
			err := cc.conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.Debug("广播写失败，断开连接", "err", err)
				}
				cc.close()
				return
			}
		}
	}
}

func (cc *clientConn) close() {
	cc.once.Do(func() {
		close(cc.closed)
		_ = cc.conn.Close(websocket.StatusNormalClosure, "")
		cc.cancel()
	})
}
