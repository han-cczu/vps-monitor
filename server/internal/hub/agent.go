package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"vpsmon/proto"
	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

const (
	// agentReadTimeout 是两条消息之间的最长间隔。
	//
	// 连上后服务端就把 report_interval 压到 1 秒，正常情况下每秒都有 metrics；
	// 30 秒还没收到任何东西，这条连接基本可以判死。注意 coder/websocket 的 Read
	// 不会因为收到 ping 而返回，所以这个超时是纯粹的"数据帧沉默"计时。
	agentReadTimeout = 30 * time.Second

	// agentWriteTimeout 是单次下发的写超时。
	agentWriteTimeout = 5 * time.Second

	// agentSendBuffer 是每条连接的下发队列长度，满了说明对端已经不消费了，直接断开。
	agentSendBuffer = 64

	// agentMaxMessage 是允许 agent 发上来的单帧上限，和 agent 侧的读上限对齐。
	agentMaxMessage = 1 << 20

	// DefaultReportInterval 是连上后下发给 agent 的上报间隔（秒）。
	DefaultReportInterval = 1
)

// maxLoggedString 是写进日志的对端可控字符串的长度上限。
//
// 不截断的话，一个坏 agent 用 1 KB 的上行就能换来服务端上百 KB 的日志
// （单帧上限 1 MiB，每秒一条），磁盘被灌满只是时间问题。
const maxLoggedString = 200

// agentStore 是 AgentHub 需要的库能力。
type agentStore interface {
	FindServerByTokenHash(ctx context.Context, tokenHash string) (*store.Server, error)
	UpsertHostInfo(ctx context.Context, h store.HostInfo) error
}

// MetricsHook 在每条 metrics 落地到 Registry 之后被调用。
// 步骤 08 的分钟聚合、步骤 18 的流量结算挂在这里。
type MetricsHook func(serverID int64, m *proto.Metrics)

// AgentHub 处理 GET /api/agent/ws：agent 鉴权、单连接、消息分发。
type AgentHub struct {
	db  agentStore
	reg *Registry
	bus *Bus
	now func() time.Time

	mu    sync.Mutex
	conns map[int64]*agentConn

	hookMu sync.RWMutex
	hooks  []MetricsHook
	// onPing 由 ping 服务在装配时挂上；没挂时 ping 消息只记一条 WARN
	onPing func(serverID int64, raw []byte)
	// buildConfig 决定连上时下发什么 config；没设时只下发上报间隔
	buildConfig   func(serverID int64) any
	onCore        func(context.Context, int64, []byte)
	onHello       func(int64)
	onObservation func(context.Context, int64, string, []byte)
}

// agentConn 是一条 agent 连接。写统一走 send 通道，由 writeLoop 串行发出，
// 这样任意协程都能安全地 SendTo，不必和读循环抢 conn。
type agentConn struct {
	serverID        int64
	conn            *websocket.Conn
	send            chan []byte
	cancel          context.CancelFunc
	closed          chan struct{}
	once            sync.Once
	helloSeen       bool // protected by AgentHub.mu; periodic hello is not a reconnect
	proxySession    string
	proxyManagement string
}

// NewAgentHub 新建 agent 接入层。
func NewAgentHub(db agentStore, reg *Registry, bus *Bus) *AgentHub {
	return &AgentHub{
		db:    db,
		reg:   reg,
		bus:   bus,
		now:   time.Now,
		conns: map[int64]*agentConn{},
	}
}

// OnMetrics 注册一个 metrics 钩子。只在启动装配时调用，不做并发注册的承诺之外的保证。
func (h *AgentHub) OnMetrics(fn MetricsHook) {
	h.hookMu.Lock()
	h.hooks = append(h.hooks, fn)
	h.hookMu.Unlock()
}

// OnPing 注册 ping 结果的处理器。只在启动装配时调用。
func (h *AgentHub) OnPing(fn func(serverID int64, raw []byte)) {
	h.hookMu.Lock()
	h.onPing = fn
	h.hookMu.Unlock()
}

func (h *AgentHub) OnCore(fn func(context.Context, int64, []byte), hello func(int64)) {
	h.hookMu.Lock()
	defer h.hookMu.Unlock()
	h.onCore = fn
	h.onHello = hello
}

func (h *AgentHub) OnObservation(fn func(context.Context, int64, string, []byte)) {
	h.hookMu.Lock()
	h.onObservation = fn
	h.hookMu.Unlock()
}

// Unknown/legacy Agents must upgrade before any managed-core writes are sent.
func (h *AgentHub) ProxyManagement(id int64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ac := h.conns[id]; ac != nil && ac.proxySession != "" && ac.proxyManagement != "" {
		return ac.proxyManagement
	}
	return "unknown"
}
func (h *AgentHub) CanManageProxy(id int64, install bool) bool {
	mode := h.ProxyManagement(id)
	return mode == "managed" || install && mode == "none"
}

func (h *AgentHub) ProxyObserveSupported(id int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	ac := h.conns[id]
	return ac != nil && ac.proxySession != ""
}

// SetConfigBuilder 设置「agent 连上时下发什么 config」。
//
// 每台节点收到的任务列表不一样（ping 任务可以指定作用范围），所以是按 serverID 组装。
func (h *AgentHub) SetConfigBuilder(fn func(serverID int64) any) {
	h.hookMu.Lock()
	h.buildConfig = fn
	h.hookMu.Unlock()
}

// ServeHTTP 处理 agent 的 WebSocket 接入。
func (h *AgentHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth.AgentMiddleware(h.db)(http.HandlerFunc(h.serveAuthenticated)).ServeHTTP(w, r)
}

func (h *AgentHub) serveAuthenticated(w http.ResponseWriter, r *http.Request) {
	s := auth.AgentFromContext(r.Context())

	publicIP := audit.ClientIP(r)
	agentVersion := r.Header.Get("X-Agent-Version")

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionContextTakeover,
		// agent 不是浏览器，不带 Origin，也不靠 Cookie 鉴权（用的是 Bearer token），
		// 同源策略在这里没有防护意义，反而可能被中间代理加的 Origin 头误伤。
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Warn("agent 连接升级失败", "server_id", s.ID, "err", err)
		return
	}
	conn.SetReadLimit(agentMaxMessage)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ac := &agentConn{
		serverID: s.ID,
		conn:     conn,
		send:     make(chan []byte, agentSendBuffer),
		cancel:   cancel,
		closed:   make(chan struct{}),
	}
	defer conn.CloseNow()

	h.register(ac)
	defer h.unregister(ac)

	if h.reg.MarkOnline(s.ID) {
		h.bus.Publish(Event{Kind: EventServerOnline, ServerID: s.ID, At: h.now()})
	}
	h.reg.Update(s.ID, func(st *ServerState) {
		st.PublicIP = publicIP
		if agentVersion != "" {
			st.AgentVersion = agentVersion
			st.AgentUpdateCapable = false
		}
	})

	slog.Info("agent connected",
		"server_id", s.ID, "name", s.Name, "public_ip", publicIP, "agent_version", agentVersion)

	go ac.writeLoop(ctx)

	// 连上先把运行参数下发过去，agent 收到后会按这个间隔上报、并把 ping 任务对齐。
	h.SendTo(s.ID, h.configFor(s.ID))

	err = h.readLoop(ctx, ac)
	slog.Info("agent disconnected", "server_id", s.ID, "name", s.Name, "err", err)
}

// readLoop 一直读到连接出错或超时。
//
// 断开时不把 Online 置 false：agent 重连、服务端重启都会短暂断开，
// 立刻置离线会让卡片闪。统一交给离线扫描按 LastSeen 判定。
func (h *AgentHub) readLoop(ctx context.Context, ac *agentConn) error {
	for {
		readCtx, cancel := context.WithTimeout(ctx, agentReadTimeout)
		_, data, err := ac.conn.Read(readCtx)
		cancel()
		if err != nil {
			return err
		}
		h.mu.Lock()
		current := h.conns[ac.serverID] == ac
		h.mu.Unlock()
		if !current {
			return context.Canceled
		}
		h.dispatch(ctx, ac.serverID, data)
	}
}

// dispatch 按消息类型分发。解不开或类型未知都只记日志，不断开连接——
// 一条坏消息不该让整台节点掉线。
func (h *AgentHub) dispatch(ctx context.Context, serverID int64, raw []byte) {
	var env proto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		slog.Warn("agent 消息无法解析", "server_id", serverID, "err", err, "bytes", len(raw))
		return
	}

	switch env.Type {
	case proto.TypeHello:
		h.handleHello(ctx, serverID, raw)
	case proto.TypeMetrics:
		h.handleMetrics(serverID, raw)
	case proto.TypeProxyObservation:
		h.mu.Lock()
		session := ""
		if ac := h.conns[serverID]; ac != nil {
			session = ac.proxySession
		}
		h.mu.Unlock()
		h.hookMu.RLock()
		handler := h.onObservation
		h.hookMu.RUnlock()
		if session != "" && handler != nil {
			handler(ctx, serverID, session, raw)
		}
	case proto.TypeCoreState, proto.TypeCoreStats, proto.TypeCoreLogs:
		if env.Type != proto.TypeCoreState && !h.CanManageProxy(serverID, false) {
			return
		}
		h.hookMu.RLock()
		handler := h.onCore
		h.hookMu.RUnlock()
		if handler != nil {
			handler(ctx, serverID, raw)
		}
	case proto.TypePing:
		h.hookMu.RLock()
		onPing := h.onPing
		h.hookMu.RUnlock()
		if onPing == nil {
			slog.Warn("收到 ping 结果但没有处理器", "server_id", serverID)
			return
		}
		onPing(serverID, raw)
	case proto.TypeError:
		var e proto.Error
		if err := json.Unmarshal(raw, &e); err != nil {
			slog.Warn("agent error 消息解析失败", "server_id", serverID, "err", err)
			return
		}
		slog.Warn("agent 回报执行失败", "server_id", serverID,
			"op", truncate(e.Op), "message", truncate(e.Message))
	default:
		// core.*（11/13）到对应步骤再注册处理器。
		slog.Warn("agent 消息类型暂未处理", "server_id", serverID, "type", truncate(env.Type))
	}
}

func (h *AgentHub) handleHello(ctx context.Context, serverID int64, raw []byte) {
	var hello proto.Hello
	if err := json.Unmarshal(raw, &hello); err != nil {
		slog.Warn("hello 解析失败", "server_id", serverID, "err", err)
		return
	}
	if hello.ProtoVersion != proto.Version {
		slog.Warn("agent 协议版本不一致",
			"server_id", serverID, "agent", hello.ProtoVersion, "server", proto.Version)
	}
	capable := false
	if hello.ProtoVersion == proto.Version && len(hello.Capabilities) <= 16 {
		for _, cap := range hello.Capabilities {
			if cap == proto.ProxyObserveCapability {
				capable = true
			}
		}
	}
	h.mu.Lock()
	negotiated := false
	if ac := h.conns[serverID]; ac != nil {
		ac.proxyManagement = "unknown"
		if capable {
			if hello.ProxyManagement == "managed" || hello.ProxyManagement == "external" || hello.ProxyManagement == "none" {
				ac.proxyManagement = hello.ProxyManagement
			}
			if ac.proxySession == "" {
				var nonce [16]byte
				if _, err := rand.Read(nonce[:]); err == nil {
					ac.proxySession = hex.EncodeToString(nonce[:])
					negotiated = true
				}
			}
		} else {
			ac.proxySession = ""
		}
	}
	h.mu.Unlock()
	if negotiated {
		h.SendTo(serverID, h.configFor(serverID))
	}

	var publicIP string
	if !h.reg.UpdateExisting(serverID, func(st *ServerState) {
		st.Host = hello.Host
		st.AgentUpdateCapable = false
		if hello.ProtoVersion == proto.Version && len(hello.Capabilities) <= 16 {
			for _, cap := range hello.Capabilities {
				if cap == proto.TypeAgentUpdate {
					st.AgentUpdateCapable = true
				}
			}
		}
		if hello.Version != "" {
			st.AgentVersion = hello.Version
		}
		publicIP = st.PublicIP
	}) {
		// 内存态没了只有一种可能：节点在这条消息在途时被删了。
		slog.Warn("收到已删除节点的 hello，丢弃", "server_id", serverID)
		return
	}

	st, _ := h.reg.Get(serverID)
	err := h.db.UpsertHostInfo(ctx, store.HostInfo{
		ServerID:     serverID,
		Hostname:     hello.Host.Hostname,
		OS:           hello.Host.OS,
		Kernel:       hello.Host.Kernel,
		Arch:         hello.Host.Arch,
		CPUModel:     hello.Host.CPUModel,
		Cores:        hello.Host.Cores,
		MemTotal:     hello.Host.MemTotal,
		DiskTotal:    hello.Host.DiskTotal,
		BootTime:     hello.Host.BootTime,
		IPv4:         hello.Host.IPv4,
		IPv6:         hello.Host.IPv6,
		PublicIP:     publicIP,
		AgentVersion: st.AgentVersion,
	})
	if err != nil {
		slog.Error("写入节点静态信息失败", "server_id", serverID, "err", err)
	}
	h.hookMu.RLock()
	onHello := h.onHello
	h.hookMu.RUnlock()
	h.mu.Lock()
	first := false
	if ac := h.conns[serverID]; ac != nil && !ac.helloSeen {
		ac.helloSeen = true
		first = true
	}
	h.mu.Unlock()
	if onHello != nil && first {
		onHello(serverID)
	}
}

func (h *AgentHub) handleMetrics(serverID int64, raw []byte) {
	var m proto.Metrics
	if err := json.Unmarshal(raw, &m); err != nil {
		slog.Warn("metrics 解析失败", "server_id", serverID, "err", err)
		return
	}

	// 在线判定一律用服务端收到的时刻，不用 m.TS——节点时钟不一定准。
	wasOffline := false
	if !h.reg.UpdateExisting(serverID, func(st *ServerState) {
		wasOffline = !st.Online
		st.Online = true
		st.LastSeen = h.now()
		st.Latest = &m
	}) {
		slog.Warn("收到已删除节点的 metrics，丢弃", "server_id", serverID)
		return
	}
	if wasOffline {
		h.bus.Publish(Event{Kind: EventServerOnline, ServerID: serverID, At: h.now()})
	}

	h.hookMu.RLock()
	hooks := h.hooks
	h.hookMu.RUnlock()
	for _, fn := range hooks {
		// 每个钩子拿自己的副本：Registry 里存的那份要保证"只替换不原地改"
		// （Get 返回的状态副本和它共享指针），不能交给步骤 08 / 18 的外部代码随手改。
		cp := m
		fn(serverID, &cp)
	}
}

// configFor 组装下发给某台节点的 config。没挂 buildConfig 时退化成「只设上报间隔」。
func (h *AgentHub) configFor(serverID int64) any {
	h.hookMu.RLock()
	build := h.buildConfig
	h.hookMu.RUnlock()

	if build != nil {
		if cfg := build(serverID); cfg != nil {
			return cfg
		}
	}
	return proto.Config{
		Type:           proto.TypeConfig,
		ReportInterval: DefaultReportInterval,
		PingTasks:      []proto.PingTask{},
	}
}

// register 挂上新连接；同一台节点已有连接时先把旧的踢掉（一台机器只该有一个 agent）。
func (h *AgentHub) register(ac *agentConn) {
	h.mu.Lock()
	old := h.conns[ac.serverID]
	h.conns[ac.serverID] = ac
	h.mu.Unlock()

	if old != nil {
		slog.Info("踢掉同一节点的旧连接", "server_id", ac.serverID)
		old.close(websocket.StatusNormalClosure, "superseded")
	}
}

// unregister 在连接退出时摘掉它并关闭。
func (h *AgentHub) unregister(ac *agentConn) {
	h.detach(ac)
	ac.close(websocket.StatusNormalClosure, "")
}

// detach 把连接从表里摘掉。
//
// 只在表里挂着的就是自己时才删：被踢掉的旧连接退出时，
// 表里挂的已经是新连接，不能顺手把新连接删掉。
func (h *AgentHub) detach(ac *agentConn) {
	h.mu.Lock()
	if h.conns[ac.serverID] == ac {
		delete(h.conns, ac.serverID)
	}
	h.mu.Unlock()
}

// SendTo 给一台节点下发消息。节点没连着返回 false。
//
// 下发队列满了说明对端堵死了，直接断开连接让它重连，而不是阻塞调用方。
func (h *AgentHub) SendTo(serverID int64, v any) bool {
	h.mu.Lock()
	ac := h.conns[serverID]
	mode := "unknown"
	session := ""
	if ac != nil {
		mode = ac.proxyManagement
		session = ac.proxySession
	}
	h.mu.Unlock()

	if ac == nil {
		return false
	}

	// Every config update retains the negotiated session (including ping edits).
	switch cfg := v.(type) {
	case proto.Config:
		cfg.ProxyObserveSession = session
		v = cfg
	case *proto.Config:
		cp := *cfg
		cp.ProxyObserveSession = session
		v = cp
	}
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("下发消息序列化失败", "server_id", serverID, "err", err)
		return false
	}
	var command struct {
		Type   string `json:"type"`
		Action string `json:"action"`
	}
	if json.Unmarshal(data, &command) != nil {
		return false
	}
	switch command.Type {
	case proto.TypeCoreAction, proto.TypeCoreApply, proto.TypeCoreLogs:
		if session == "" || mode != "managed" && !(mode == "none" && command.Type == proto.TypeCoreAction && command.Action == "install") {
			return false
		}
	case proto.TypeProxyRefresh:
		if session == "" {
			return false
		}
	}

	select {
	case ac.send <- data:
		return true
	default:
		slog.Warn("下发队列已满，断开该连接", "server_id", serverID)
		ac.close(websocket.StatusPolicyViolation, "send buffer full")
		return false
	}
}

// Disconnect 断开一台节点当前的 agent 连接，返回是否真的断了一条。
//
// 重置 token 时用：鉴权只发生在握手那一刻，不主动踢掉的话，
// 拿着已作废 token 连上来的 agent 能一直连到自己断开为止。
func (h *AgentHub) Disconnect(serverID int64, reason string) bool {
	h.mu.Lock()
	ac := h.conns[serverID]
	h.mu.Unlock()

	if ac == nil {
		return false
	}

	// 先摘表再关：关闭是异步的（要等对端回 close 帧），这期间不能再让
	// SendTo / Connected 把这条已经作废的连接当成可用的。
	h.detach(ac)
	ac.close(websocket.StatusNormalClosure, reason)
	return true
}

// Broadcast 给每个在线 agent 各自组装一条消息并下发，返回发出去的条数。
//
// ping 任务增删改之后用它推送新的 config：每台节点收到的任务列表不一样
// （任务可以指定作用范围），所以是「每连接调一次 build」而不是发同一份。
func (h *AgentHub) Broadcast(build func(serverID int64) any) int {
	h.mu.Lock()
	ids := make([]int64, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	h.mu.Unlock()

	sent := 0
	for _, id := range ids {
		if msg := build(id); msg != nil && h.SendTo(id, msg) {
			sent++
		}
	}
	return sent
}

// Connected 报告一台节点当前是否有活着的 agent 连接。
func (h *AgentHub) Connected(serverID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[serverID] != nil
}

// writeLoop 串行发出 send 队列里的消息。
func (ac *agentConn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ac.closed:
			return
		case data := <-ac.send:
			writeCtx, cancel := context.WithTimeout(ctx, agentWriteTimeout)
			err := ac.conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("下发失败，断开连接", "server_id", ac.serverID, "err", err)
				}
				ac.close(websocket.StatusInternalError, "write failed")
				return
			}
		}
	}
}

// close 关闭连接，重复调用安全。
//
// 关闭动作放在单独的协程里：coder/websocket 的 Close 会等对端回一个 close 帧
// （最长几秒），踢旧连接时调用方是新连接的握手路径，不能被这一等卡住——
// 卡住的直接后果是新 agent 迟迟收不到 config。
// cancel 也要等 Close 返回再调：提前取消会让 handler 立刻退出并 CloseNow，
// 把还没发出去的 close 帧连同 socket 一起拆掉，对端只能看到一个 EOF。
func (ac *agentConn) close(code websocket.StatusCode, reason string) {
	ac.once.Do(func() {
		close(ac.closed)
		go func() {
			_ = ac.conn.Close(code, reason)
			ac.cancel()
		}()
	})
}

// truncate 把对端可控的字符串截到可以安全写进日志的长度。
func truncate(s string) string {
	if len(s) <= maxLoggedString {
		return s
	}
	return s[:maxLoggedString] + "…（已截断）"
}
