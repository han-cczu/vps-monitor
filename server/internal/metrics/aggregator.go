// Package metrics 把秒级上报聚合成分钟行、再降采样成小时行，并按保留期清理。
//
// 为什么不直接把每条 metrics 落库：十几台节点每秒各一条，一天就是一百多万行，
// 而面板真正要看的是曲线。在内存里按分钟聚合，落库量降到 1/60，
// 代价是进程重启会丢掉当前这一分钟还没写出去的桶——一分钟的曲线缺口，可以接受。
package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

// ----------------------------------------------------------------------

// bucket 是一台节点在某一分钟内的累加器。
//
// 三类语义：
//   - 平均（cpu、mem、swap、load、速率）：累加后除以 n
//   - 峰值（cpu、速率）：取最大
//   - 水位（disk、累计流量、连接数、进程数）：取这一分钟最后一条
//
// 水位类不能平均：磁盘用量、累计流量是单调的水位，平均出来的值既不是区间起点
// 也不是终点，画在曲线上会比真实值系统性偏低。
type bucket struct {
	ts int64
	n  int

	cpuSum, cpuMax       float64
	memSum, swapSum      int64
	load1, load5, load15 float64

	rxRateSum, txRateSum int64
	rxRateMax, txRateMax int64

	// 最后一条样本里的水位类字段
	diskUsed, rxTotal, txTotal int64
	tcp, udp, procs            int
}

func newBucket(ts int64) *bucket {
	return &bucket{ts: ts}
}

// add 把一条样本并进桶里。
func (b *bucket) add(m *proto.Metrics) {
	b.n++

	b.cpuSum += m.CPU
	if m.CPU > b.cpuMax {
		b.cpuMax = m.CPU
	}

	b.memSum += m.MemUsed
	b.swapSum += m.SwapUsed

	// load 是瞬时值，直接累加求平均
	b.load1 += m.Load[0]
	b.load5 += m.Load[1]
	b.load15 += m.Load[2]

	b.rxRateSum += m.Net.RxRate
	b.txRateSum += m.Net.TxRate
	if m.Net.RxRate > b.rxRateMax {
		b.rxRateMax = m.Net.RxRate
	}
	if m.Net.TxRate > b.txRateMax {
		b.txRateMax = m.Net.TxRate
	}

	// 水位类：后来的覆盖先前的，循环结束时留下的就是最后一条
	b.diskUsed = m.DiskUsed
	b.rxTotal = m.Net.RxTotal
	b.txTotal = m.Net.TxTotal
	b.tcp = m.TCP
	b.udp = m.UDP
	b.procs = m.Procs
}

// row 把桶结算成一行。n 为 0 时不该被调用。
func (b *bucket) row(serverID int64) store.MetricRow {
	n := float64(b.n)
	div := int64(b.n)

	return store.MetricRow{
		ServerID: serverID,
		TS:       b.ts,

		CPUAvg:   b.cpuSum / n,
		CPUMax:   b.cpuMax,
		MemUsed:  b.memSum / div,
		SwapUsed: b.swapSum / div,
		DiskUsed: b.diskUsed,
		Load1:    b.load1 / n,
		Load5:    b.load5 / n,
		Load15:   b.load15 / n,

		RxRateAvg: b.rxRateSum / div,
		RxRateMax: b.rxRateMax,
		TxRateAvg: b.txRateSum / div,
		TxRateMax: b.txRateMax,
		RxTotal:   b.rxTotal,
		TxTotal:   b.txTotal,

		TCP:   b.tcp,
		UDP:   b.udp,
		Procs: b.procs,

		Samples: b.n,
	}
}

// ----------------------------------------------------------------------

// minuteWriter 是 Aggregator 需要的落库能力。
type minuteWriter interface {
	InsertMinuteRows(ctx context.Context, rows []store.MetricRow) error
}

// Aggregator 把 hub 收到的每条 metrics 累加进当前分钟的桶，每分钟落一次库。
type Aggregator struct {
	db  minuteWriter
	now func() time.Time

	mu      sync.Mutex
	current map[int64]*bucket   // server_id → 当前分钟的桶
	pending map[int64][]*bucket // server_id → 已经切走、等着落库的桶
}

// New 新建聚合器。
func New(db minuteWriter) *Aggregator {
	return &Aggregator{
		db:      db,
		now:     time.Now,
		current: map[int64]*bucket{},
		pending: map[int64][]*bucket{},
	}
}

// OnMetrics 挂到 hub 的 metrics 钩子上。
//
// 用服务端收到的时刻分桶，不用 m.TS——节点时钟不准会让曲线整段平移，
// 而这里关心的是「面板看到的时间线」。
func (a *Aggregator) OnMetrics(serverID int64, m *proto.Metrics) {
	ts := a.now().Unix() / 60 * 60

	a.mu.Lock()
	defer a.mu.Unlock()

	cur, ok := a.current[serverID]
	if !ok || cur.ts != ts {
		// 跨分钟了：把上一分钟的桶挪到待写队列。注意不要在这里落库——
		// 这个函数在 hub 的读循环里同步调用，卡住它就等于卡住这台节点的上报。
		if ok && cur.n > 0 {
			a.pending[serverID] = append(a.pending[serverID], cur)
		}
		cur = newBucket(ts)
		a.current[serverID] = cur
	}

	cur.add(m)
}

// Flush 把待写队列和所有已经过期的当前桶写进库。
//
// 每分钟第 2 秒调一次：那时上一分钟的桶要么已经被新样本挤进待写队列，
// 要么因为节点掉线还留在 current 里——后者靠「ts 早于当前分钟」判出来。
func (a *Aggregator) Flush(ctx context.Context) error {
	nowTS := a.now().Unix() / 60 * 60

	a.mu.Lock()
	rows := make([]store.MetricRow, 0, len(a.current)+len(a.pending))

	for serverID, buckets := range a.pending {
		for _, b := range buckets {
			rows = append(rows, b.row(serverID))
		}
		delete(a.pending, serverID)
	}

	for serverID, b := range a.current {
		if b.ts < nowTS && b.n > 0 {
			rows = append(rows, b.row(serverID))
			delete(a.current, serverID)
		}
	}
	a.mu.Unlock()

	if len(rows) == 0 {
		return nil
	}

	if err := a.db.InsertMinuteRows(ctx, rows); err != nil {
		return err
	}
	slog.Debug("分钟指标已落库", "rows", len(rows))
	return nil
}

// Run 每分钟跑一次 Flush，直到 ctx 结束；退出前再落一次库。
//
// 对齐到每分钟第 2 秒：等 2 秒是给「跨分钟的那条样本」留出到达时间，
// 不然每次都会把上一分钟的最后一两条样本落到下一分钟的桶里。
func (a *Aggregator) Run(ctx context.Context) {
	for {
		wait := untilNextFlush(a.now())

		select {
		case <-ctx.Done():
			// 关停时把手里的桶写掉，别白丢一分钟数据。用独立的 ctx：
			// 传进来的那个已经取消了，拿它去写库会直接失败。
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := a.Flush(flushCtx); err != nil {
				slog.Error("关停时落库失败", "err", err)
			}
			cancel()
			return

		case <-time.After(wait):
			if err := a.Flush(ctx); err != nil {
				slog.Error("分钟指标落库失败", "err", err)
			}
		}
	}
}

// flushOffset 是每分钟内的落库时刻。
const flushOffset = 2 * time.Second

// untilNextFlush 返回距下一个「整分 + flushOffset」还有多久。
// 先算本分钟的那个时刻，已经过去了才推到下一分钟——直接从下一分钟起算的话，
// 在 10:00:01 启动会白等到 10:01:02，平白多丢一分钟。
func untilNextFlush(now time.Time) time.Duration {
	next := now.Truncate(time.Minute).Add(flushOffset)
	if !next.After(now) {
		next = next.Add(time.Minute)
	}
	return next.Sub(now)
}
