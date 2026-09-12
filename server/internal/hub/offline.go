package hub

import (
	"context"
	"log/slog"
	"time"
)

const (
	// OfflineScanInterval 是离线扫描的周期。
	OfflineScanInterval = 5 * time.Second

	// OfflineTimeout 是判定离线的沉默时长：超过这么久没收到 metrics 就算掉线。
	//
	// agent 每秒上报一次，15 秒相当于连丢 15 帧。设计方案 §6.2 要求 15–20 秒内
	// 看到状态变化，扫描周期 5 秒 + 超时 15 秒最坏 20 秒，正好卡在上限。
	OfflineTimeout = 15 * time.Second
)

// OfflineScanner 周期性地把长时间没上报的节点判为离线。
//
// 为什么不在连接断开时立刻置离线：agent 重连、服务端滚动重启都会让连接短暂中断，
// 立刻翻状态会让卡片闪烁、告警乱响。统一按"最后一次上报距今多久"判定，
// 重连快的根本不会被判掉线。
type OfflineScanner struct {
	reg      *Registry
	bus      *Bus
	interval time.Duration
	timeout  time.Duration
}

// NewOfflineScanner 新建扫描器，用默认的周期与超时。
func NewOfflineScanner(reg *Registry, bus *Bus) *OfflineScanner {
	return &OfflineScanner{
		reg:      reg,
		bus:      bus,
		interval: OfflineScanInterval,
		timeout:  OfflineTimeout,
	}
}

// Run 一直扫描到 ctx 结束。
func (s *OfflineScanner) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan()
		}
	}
}

// scan 跑一轮判定，返回这一轮新掉线的节点 ID。
func (s *OfflineScanner) scan() []int64 {
	offline := s.reg.MarkStale(s.timeout)
	now := s.reg.now()

	for _, id := range offline {
		slog.Info("节点掉线", "server_id", id, "timeout", s.timeout.String())
		s.bus.Publish(Event{Kind: EventServerOffline, ServerID: id, At: now})
	}
	return offline
}
