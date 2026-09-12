package collector

import (
	"log/slog"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"vpsmon/proto"
)

// diskCacheTTL 是磁盘用量的缓存时间。disk.Usage 要 statfs 每个挂载点，
// 在网络文件系统上能卡几十毫秒，而容量变化根本用不着秒级。
const diskCacheTTL = 10 * time.Second

// Sampler 持有上一次的 CPU 时间片与网卡计数，用差值算 CPU 占用与网络速率。
//
// 每个 agent 进程一个实例，Sample 可以被并发调用（内部加锁）。
type Sampler struct {
	excludeIfaces []string
	diskMounts    []string

	mu       sync.Mutex
	lastCPU  cpu.TimesStat
	lastNet  ifaceTotals
	lastTime time.Time
	hasLast  bool

	diskTotal    int64
	diskUsed     int64
	diskCachedAt time.Time
}

// NewSampler 新建采样器。exclude 是不计入流量的网卡通配，mounts 是磁盘统计的挂载点。
func NewSampler(exclude, mounts []string) *Sampler {
	return &Sampler{
		excludeIfaces: append([]string(nil), exclude...),
		diskMounts:    append([]string(nil), mounts...),
	}
}

// Sample 采集一次动态指标。
//
// 第一次调用没有上一次快照，CPU 与网络速率记 0（累计值仍然是真的）——
// 这是差值算法的固有代价，秒级上报下只影响第一帧。
func (s *Sampler) Sample() proto.Metrics {
	now := time.Now()

	m := proto.Metrics{
		Type: proto.TypeMetrics,
		TS:   now.Unix(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := 0.0
	if s.hasLast {
		elapsed = now.Sub(s.lastTime).Seconds()
	}

	// CPU：两次时间片差值
	if times, err := cpu.Times(false); err == nil && len(times) > 0 {
		cur := times[0]
		if s.hasLast {
			m.CPU = cpuPercent(s.lastCPU, cur)
		}
		s.lastCPU = cur
	} else if err != nil {
		slog.Warn("采集 CPU 时间片失败", "err", err)
	}

	// 网络：累计值 + 速率
	if totals, err := readIfaceTotals(s.excludeIfaces); err == nil {
		m.Net.RxTotal = int64(totals.rx)
		m.Net.TxTotal = int64(totals.tx)
		if s.hasLast {
			m.Net.RxRate = rate(s.lastNet.rx, totals.rx, elapsed)
			m.Net.TxRate = rate(s.lastNet.tx, totals.tx, elapsed)
		}
		s.lastNet = totals
	} else {
		slog.Warn("采集网卡计数失败", "err", err)
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemUsed = int64(vm.Used)
	} else {
		slog.Warn("采集内存用量失败", "err", err)
	}
	if sw, err := mem.SwapMemory(); err == nil {
		m.SwapUsed = int64(sw.Used)
	} else {
		slog.Warn("采集 swap 用量失败", "err", err)
	}

	m.DiskUsed = s.diskUsedCached(now)

	if avg, err := load.Avg(); err == nil {
		m.Load = [3]float64{avg.Load1, avg.Load5, avg.Load15}
	} else {
		slog.Warn("采集负载失败", "err", err)
	}

	conns := readConnStats()
	m.TCP = conns.tcp
	m.UDP = conns.udp
	m.Procs = readProcCount()

	if up, err := host.Uptime(); err == nil {
		m.Uptime = int64(up)
	}

	s.lastTime = now
	s.hasLast = true
	return m
}

// DiskTotal 返回缓存里的磁盘总量（Sample 或 CollectHost 之后才有值）。
func (s *Sampler) DiskTotal() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diskTotal
}

// diskUsedCached 按 diskCacheTTL 缓存磁盘用量。调用方必须已持有锁。
func (s *Sampler) diskUsedCached(now time.Time) int64 {
	if !s.diskCachedAt.IsZero() && now.Sub(s.diskCachedAt) < diskCacheTTL {
		return s.diskUsed
	}
	total, used := diskTotals(s.diskMounts)
	s.diskTotal, s.diskUsed, s.diskCachedAt = total, used, now
	return used
}

// cpuPercent 按两次 CPU 时间片算占用百分比（0–100）。
//
// Guest / GuestNice 不单独计入：Linux 把它们同时记在 User / Nice 里，加上去会重复。
func cpuPercent(prev, cur cpu.TimesStat) float64 {
	prevTotal := cpuTotal(prev)
	curTotal := cpuTotal(cur)
	deltaTotal := curTotal - prevTotal
	if deltaTotal <= 0 {
		return 0
	}

	prevIdle := prev.Idle + prev.Iowait
	curIdle := cur.Idle + cur.Iowait
	deltaIdle := curIdle - prevIdle
	if deltaIdle < 0 {
		deltaIdle = 0
	}

	pct := (deltaTotal - deltaIdle) / deltaTotal * 100
	switch {
	case pct < 0:
		return 0
	case pct > 100:
		return 100
	default:
		return round2(pct)
	}
}

func cpuTotal(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
