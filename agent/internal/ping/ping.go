// Package ping 按服务端下发的任务做 ICMP / TCP 探测并回报结果。
//
// 每个任务一个协程，各自按自己的间隔跑；服务端改了任务就整体对齐一次
// （新增的启动、参数变了的重启、删掉的停掉），不需要重连也不需要重启 agent。
package ping

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"vpsmon/proto"
)

// ----------------------------------------------------------------------

const (
	// probeTimeout 是单次探测的上限。超过就算丢包。
	//
	// 3 秒对跨洲链路也够了：真实延迟到 300ms 就已经很差，等到 3 秒还没回来
	// 基本可以断定是丢了，而不是慢。
	probeTimeout = 3 * time.Second

	// 间隔的取值范围。太小会把探测本身变成负载，太大失去监控意义。
	minInterval = 10 * time.Second
	maxInterval = time.Hour

	// dnsWarnInterval 是「域名解析不了」这类错误的日志节流间隔。
	// 目标域名挂了的话，不节流就是每分钟一条 WARN 刷满 journald。
	dnsWarnInterval = 10 * time.Minute
)

// Result 是一次探测的结果。LatencyMS 为 nil 表示超时或失败。
type Result struct {
	TaskID    int64
	TS        int64
	LatencyMS *float64
}

// Scheduler 管理所有 ping 任务的执行。
type Scheduler struct {
	send func(Result)

	mu      sync.Mutex
	running map[int64]*runner

	// probe 与 jitter 可替换：测试里不真的发包，也不等那个随机的首次延迟。
	probe  func(ctx context.Context, task proto.PingTask) (*float64, error)
	jitter func(interval time.Duration) time.Duration
}

// runner 是一个任务的执行体。
type runner struct {
	task   proto.PingTask
	cancel context.CancelFunc
	done   chan struct{}
}

// New 新建调度器。send 在每次探测结束后被调用（包括丢包）。
func New(send func(Result)) *Scheduler {
	s := &Scheduler{
		send:    send,
		running: map[int64]*runner{},
	}
	s.probe = probe
	s.jitter = randomJitter
	return s
}

// randomJitter 把首次执行打散到 [0, interval)。
//
// 三个任务同时下发时，不打散的话它们会永远在同一秒发出去——探测本身变成了一个尖峰，
// 而且三条曲线的采样点完全重合，看不出哪条先抖。
func randomJitter(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(interval)))
}

// Apply 把当前运行的任务对齐到 tasks。
//
// 三种情况：新增的启动、参数变了的先停再启、不在列表里的停掉。
// 参数没变的**不动**——重启会丢掉它的计时相位，几十个任务同时重启还会让探测挤在同一秒。
func (s *Scheduler) Apply(ctx context.Context, tasks []proto.PingTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wanted := map[int64]proto.PingTask{}
	for _, t := range tasks {
		if err := validate(t); err != nil {
			slog.Warn("忽略非法的 ping 任务", "task_id", t.ID, "name", t.Name, "err", err)
			continue
		}
		wanted[t.ID] = t
	}

	// 停掉不再需要的，以及参数变了的
	for id, r := range s.running {
		next, keep := wanted[id]
		if keep && sameTask(r.task, next) {
			continue
		}
		r.cancel()
		<-r.done
		delete(s.running, id)
		if keep {
			slog.Info("ping 任务参数变更，重启", "task_id", id, "name", next.Name)
		} else {
			slog.Info("ping 任务已停止", "task_id", id, "name", r.task.Name)
		}
	}

	// 启动新增的
	for id, t := range wanted {
		if _, ok := s.running[id]; ok {
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		r := &runner{task: t, cancel: cancel, done: make(chan struct{})}
		s.running[id] = r
		go s.run(runCtx, r)
		slog.Info("ping 任务已启动",
			"task_id", id, "name", t.Name, "kind", t.Kind, "target", t.Target, "interval", t.Interval)
	}
}

// Stop 停掉全部任务。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, r := range s.running {
		r.cancel()
		<-r.done
		delete(s.running, id)
	}
}

// Running 返回当前在跑的任务数，测试与排障用。
func (s *Scheduler) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// run 是一个任务的循环。
func (s *Scheduler) run(ctx context.Context, r *runner) {
	defer close(r.done)

	interval := clampInterval(r.task.Interval)

	// 首次执行随机延迟 0–interval，理由见 randomJitter
	if wait := s.jitter(interval); wait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastWarn time.Time
	for {
		s.once(ctx, r.task, &lastWarn)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// once 做一次探测并上报。
func (s *Scheduler) once(ctx context.Context, task proto.PingTask, lastWarn *time.Time) {
	if ctx.Err() != nil {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	latency, err := s.probe(probeCtx, task)
	cancel()

	if ctx.Err() != nil {
		return // 任务已经被停掉，结果没有意义
	}

	if err != nil {
		// 失败一律按丢包上报——对面板来说「连不上」和「超时」是同一件事。
		// 但日志要节流：目标域名挂了的话，不节流就是每分钟一条刷满 journald。
		if time.Since(*lastWarn) >= dnsWarnInterval {
			slog.Warn("ping 探测失败", "task_id", task.ID, "name", task.Name, "target", task.Target, "err", err)
			*lastWarn = time.Now()
		}
		latency = nil
	}

	s.send(Result{TaskID: task.ID, TS: time.Now().Unix(), LatencyMS: latency})
}

// ----------------------------------------------------------------------

// probe 按任务类型做一次探测，返回毫秒延迟。
func probe(ctx context.Context, task proto.PingTask) (*float64, error) {
	switch task.Kind {
	case "tcp":
		return probeTCP(ctx, task.Target)
	default:
		return probeICMP(ctx, task.Target)
	}
}

// probeICMP 发一个 ICMP echo。
//
// SetPrivileged(true)：Linux 上 agent 以 root 跑，用原始套接字。
// 非特权模式（UDP）在不少发行版上需要额外调 net.ipv4.ping_group_range，
// 装机脚本管不到那一层，所以直接要求 root——反正 agent 本来就是 root 跑的。
func probeICMP(ctx context.Context, target string) (*float64, error) {
	// DNS 也必须受单次探测的 deadline 和任务取消控制。
	address := target
	if net.ParseIP(target) == nil {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("解析目标 %s: %w", target, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("目标 %s 没有可用地址", target)
		}
		address = ips[0].String()
	}
	pinger, err := probing.NewPinger(address)
	if err != nil {
		return nil, fmt.Errorf("解析目标 %s: %w", target, err)
	}

	pinger.Count = 1
	pinger.Timeout = probeTimeout
	pinger.SetPrivileged(true)

	if err := pinger.RunWithContext(ctx); err != nil {
		return nil, err
	}

	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return nil, fmt.Errorf("目标 %s 无响应", target)
	}
	return msPtr(stats.AvgRtt), nil
}

// probeTCP 连一次 TCP 并立刻关掉，用握手耗时当延迟。
//
// 给那些禁了 ICMP 出站的机房用。数值上会比 ICMP 略高（多一个握手往返），
// 但趋势是一样的，而且能同时验证「那个端口是通的」。
func probeTCP(ctx context.Context, target string) (*float64, error) {
	var d net.Dialer

	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start)
	_ = conn.Close()

	return msPtr(elapsed), nil
}

// msPtr 把时长转成保留一位小数的毫秒。
func msPtr(d time.Duration) *float64 {
	ms := float64(d.Microseconds()) / 1000
	ms = float64(int64(ms*10+0.5)) / 10
	return &ms
}

// ----------------------------------------------------------------------

// validate 检查任务参数。服务端已经校验过一遍，这里是防「服务端版本更新、
// 下发了 agent 不认识的东西」——宁可忽略单个任务，也不要让整批下发失败。
func validate(t proto.PingTask) error {
	if t.ID <= 0 {
		return fmt.Errorf("任务 ID 不合法: %d", t.ID)
	}
	if t.Target == "" {
		return fmt.Errorf("目标为空")
	}
	switch t.Kind {
	case "icmp", "":
	case "tcp":
		if _, _, err := net.SplitHostPort(t.Target); err != nil {
			return fmt.Errorf("tcp 目标必须是 host:port，得到 %q", t.Target)
		}
	default:
		return fmt.Errorf("未知的探测类型 %q", t.Kind)
	}
	return nil
}

// sameTask 判断两个任务的执行参数是否一致。名字变了不用重启——它只影响面板显示。
func sameTask(a, b proto.PingTask) bool {
	return a.Target == b.Target && a.Kind == b.Kind && a.Interval == b.Interval
}

// clampInterval 把间隔夹到合理范围。
func clampInterval(seconds int) time.Duration {
	d := time.Duration(seconds) * time.Second
	if d < minInterval {
		return minInterval
	}
	if d > maxInterval {
		return maxInterval
	}
	return d
}
