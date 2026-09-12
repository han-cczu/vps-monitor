package hub

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// errUnexpectedFrame 表示浏览器的首帧不是 auth。
var errUnexpectedFrame = errors.New("hub: 首帧不是 auth")

// errAuthTimeout 表示浏览器连上后没在时限内发出 auth 首帧。
var errAuthTimeout = errors.New("hub: 等待 auth 首帧超时")

// Store 是 hub 需要的全部库能力，*store.DB 实现它。
type Store interface {
	configLister
	agentStore
}

// Hub 把四个部件装在一起：注册表、agent 接入、浏览器接入、离线扫描。
//
// 所有对外方法都容忍 nil 接收者——api 包的单元测试不装 hub，
// 让它们继续按"没有实时状态"的样子跑，不必为此改一堆测试装配。
type Hub struct {
	Registry *Registry
	Agents   *AgentHub
	Clients  *ClientHub
	Bus      *Bus

	scanner *OfflineScanner
}

// New 装配一个 hub。tokens 用来校验浏览器首帧里的 JWT。
func New(db Store, tokens tokenParser) *Hub {
	bus := &Bus{}
	reg := NewRegistry(db)

	return &Hub{
		Registry: reg,
		Agents:   NewAgentHub(db, reg, bus),
		Clients:  NewClientHub(reg, tokens),
		Bus:      bus,
		scanner:  NewOfflineScanner(reg, bus),
	}
}

// Run 启动离线扫描与快照广播，阻塞到 ctx 结束。
func (h *Hub) Run(ctx context.Context) {
	if h == nil {
		return
	}

	// 先用库里已有的静态信息预热内存态：不预热的话，服务端刚重启、agent 还没重连的
	// 那几秒里快照的 cores / mem.total 全是 0，而 REST 同时返回着真值。
	if err := h.Registry.WarmHostInfo(ctx); err != nil {
		slog.Error("预热节点静态信息失败", "err", err)
	}

	done := make(chan struct{})
	go func() {
		h.scanner.Run(ctx)
		close(done)
	}()

	h.Clients.Broadcast(ctx)
	<-done
}

// AgentHandler 返回 GET /api/agent/ws 的处理器。
func (h *Hub) AgentHandler() http.Handler { return h.Agents }

// ClientHandler 返回 GET /api/ws 的处理器。
func (h *Hub) ClientHandler() http.Handler { return h.Clients }

// Status 返回节点的在线状态与最后上报时刻，供 REST 合并。
func (h *Hub) Status(serverID int64) (bool, *int64) {
	if h == nil {
		return false, nil
	}
	return h.Registry.Status(serverID)
}

// InvalidateConfig 让快照里的静态配置缓存失效，节点增删改后调用。
func (h *Hub) InvalidateConfig() {
	if h == nil {
		return
	}
	h.Registry.InvalidateConfig()
}

// Disconnect 断开一台节点当前的 agent 连接（重置 token、删除节点时调用）。
func (h *Hub) Disconnect(serverID int64, reason string) {
	if h == nil {
		return
	}
	h.Agents.Disconnect(serverID, reason)
}

// Remove 清掉一台节点的内存态，节点被删除时调用。
func (h *Hub) Remove(serverID int64) {
	if h == nil {
		return
	}
	h.Registry.Remove(serverID)
}
