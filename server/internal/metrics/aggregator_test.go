package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

// ----------------------------------------------------------------------

// fakeWriter 记下每次落库的行。
type fakeWriter struct {
	mu   sync.Mutex
	rows []store.MetricRow
	err  error
}

func (f *fakeWriter) InsertMinuteRows(_ context.Context, rows []store.MetricRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, rows...)
	return nil
}

func (f *fakeWriter) all() []store.MetricRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.MetricRow(nil), f.rows...)
}

// sample 造一条 metrics。只填测试关心的字段。
func sample(cpu float64, mem, disk, rxRate, rxTotal int64, tcp int) *proto.Metrics {
	return &proto.Metrics{
		Type:     proto.TypeMetrics,
		CPU:      cpu,
		MemUsed:  mem,
		DiskUsed: disk,
		Load:     [3]float64{cpu / 10, cpu / 20, cpu / 40},
		Net:      proto.NetStat{RxRate: rxRate, TxRate: rxRate * 2, RxTotal: rxTotal, TxTotal: rxTotal * 2},
		TCP:      tcp,
		Procs:    100 + tcp,
	}
}

// newTestAggregator 造一个时钟可控的聚合器。
func newTestAggregator() (*Aggregator, *fakeWriter, *time.Time) {
	db := &fakeWriter{}
	now := time.Unix(1_800_000_000, 0) // 整分：1800000000 % 60 == 0

	a := New(db)
	a.now = func() time.Time { return now }
	return a, db, &now
}

// ----------------------------------------------------------------------

// TestBucketAvgMaxLast 覆盖三类聚合语义：平均、峰值、取最后一条。
func TestBucketAvgMaxLast(t *testing.T) {
	a, db, now := newTestAggregator()

	// 同一分钟内三条样本
	a.OnMetrics(1, sample(10, 100, 1000, 10, 5000, 5))
	a.OnMetrics(1, sample(30, 300, 2000, 50, 6000, 7))
	a.OnMetrics(1, sample(20, 200, 3000, 30, 7000, 9))

	// 跨到下一分钟并落库
	*now = now.Add(time.Minute)
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows := db.all()
	if len(rows) != 1 {
		t.Fatalf("期望 1 行，得到 %d 行", len(rows))
	}
	r := rows[0]

	if r.ServerID != 1 || r.TS != 1_800_000_000 {
		t.Errorf("行标识不对: server=%d ts=%d", r.ServerID, r.TS)
	}
	if r.Samples != 3 {
		t.Errorf("samples 应当是 3，得到 %d", r.Samples)
	}

	// 平均
	if r.CPUAvg != 20 {
		t.Errorf("cpu_avg 应当是 (10+30+20)/3=20，得到 %v", r.CPUAvg)
	}
	if r.MemUsed != 200 {
		t.Errorf("mem_used 应当是 200，得到 %d", r.MemUsed)
	}
	if r.RxRateAvg != 30 {
		t.Errorf("rx_rate_avg 应当是 30，得到 %d", r.RxRateAvg)
	}

	// 峰值
	if r.CPUMax != 30 {
		t.Errorf("cpu_max 应当是 30，得到 %v", r.CPUMax)
	}
	if r.RxRateMax != 50 {
		t.Errorf("rx_rate_max 应当是 50，得到 %d", r.RxRateMax)
	}
	if r.TxRateMax != 100 {
		t.Errorf("tx_rate_max 应当是 100，得到 %d", r.TxRateMax)
	}

	// 取最后一条（水位类）
	if r.DiskUsed != 3000 {
		t.Errorf("disk_used 应当取最后一条 3000，得到 %d", r.DiskUsed)
	}
	if r.RxTotal != 7000 {
		t.Errorf("rx_total 应当取最后一条 7000，得到 %d", r.RxTotal)
	}
	if r.TCP != 9 || r.Procs != 109 {
		t.Errorf("tcp/procs 应当取最后一条 9/109，得到 %d/%d", r.TCP, r.Procs)
	}
}

// TestBucketRollsOverAtMinuteBoundary 覆盖跨分钟切换：两分钟出两行，各自独立。
func TestBucketRollsOverAtMinuteBoundary(t *testing.T) {
	a, db, now := newTestAggregator()

	a.OnMetrics(1, sample(10, 100, 1000, 10, 5000, 1))
	a.OnMetrics(1, sample(10, 100, 1000, 10, 5000, 1))

	*now = now.Add(time.Minute)
	a.OnMetrics(1, sample(80, 800, 9000, 80, 9000, 3))

	*now = now.Add(time.Minute)
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows := db.all()
	if len(rows) != 2 {
		t.Fatalf("期望 2 行，得到 %d 行", len(rows))
	}

	first, second := rows[0], rows[1]
	if first.TS >= second.TS {
		first, second = second, first
	}
	if second.TS-first.TS != 60 {
		t.Errorf("两行应当相差 60 秒，得到 %d", second.TS-first.TS)
	}
	if first.Samples != 2 || second.Samples != 1 {
		t.Errorf("样本数应当是 2 / 1，得到 %d / %d", first.Samples, second.Samples)
	}
	if first.CPUAvg != 10 || second.CPUAvg != 80 {
		t.Errorf("两分钟的 cpu 应当各自独立（10 / 80），得到 %v / %v", first.CPUAvg, second.CPUAvg)
	}
}

// TestFlushKeepsCurrentMinute 保证正在累加的那一分钟不会被提前写出去。
func TestFlushKeepsCurrentMinute(t *testing.T) {
	a, db, _ := newTestAggregator()

	a.OnMetrics(1, sample(10, 100, 1000, 10, 5000, 1))

	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(db.all()); n != 0 {
		t.Fatalf("当前分钟还没结束，不该落库，却写了 %d 行", n)
	}
}

// TestFlushWritesStaleBucketOfOfflineNode 覆盖「节点掉线」这条路径：
// 没有新样本来挤掉旧桶，靠 Flush 自己按时间判出来。
func TestFlushWritesStaleBucketOfOfflineNode(t *testing.T) {
	a, db, now := newTestAggregator()

	a.OnMetrics(1, sample(10, 100, 1000, 10, 5000, 1))
	// 节点从此不再上报
	*now = now.Add(3 * time.Minute)

	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows := db.all()
	if len(rows) != 1 {
		t.Fatalf("掉线节点的最后一分钟也该落库，得到 %d 行", len(rows))
	}
	if rows[0].TS != 1_800_000_000 {
		t.Errorf("ts 应当是样本到达时那一分钟，得到 %d", rows[0].TS)
	}

	// 再 Flush 一次不该重复写
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(db.all()); n != 1 {
		t.Errorf("重复 Flush 不该再写，累计 %d 行", n)
	}
}

// TestMultipleServersAreIndependent 保证不同节点互不串桶。
func TestMultipleServersAreIndependent(t *testing.T) {
	a, db, now := newTestAggregator()

	a.OnMetrics(1, sample(10, 100, 1000, 10, 5000, 1))
	a.OnMetrics(2, sample(90, 900, 9000, 90, 9000, 9))
	a.OnMetrics(1, sample(30, 300, 1500, 30, 5500, 2))

	*now = now.Add(time.Minute)
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	byServer := map[int64]store.MetricRow{}
	for _, r := range db.all() {
		byServer[r.ServerID] = r
	}
	if len(byServer) != 2 {
		t.Fatalf("期望两台节点各一行，得到 %d", len(byServer))
	}
	if byServer[1].CPUAvg != 20 || byServer[1].Samples != 2 {
		t.Errorf("节点 1 应当是 avg=20 samples=2，得到 %v / %d", byServer[1].CPUAvg, byServer[1].Samples)
	}
	if byServer[2].CPUAvg != 90 || byServer[2].Samples != 1 {
		t.Errorf("节点 2 应当是 avg=90 samples=1，得到 %v / %d", byServer[2].CPUAvg, byServer[2].Samples)
	}
}

// TestUntilNextFlush 覆盖「本分钟内的触发点不该被跳过」。
func TestUntilNextFlush(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want time.Duration
	}{
		{"整分刚过", time.Unix(1_800_000_000, 0), 2 * time.Second},
		{"已过触发点", time.Unix(1_800_000_003, 0), 59 * time.Second},
		{"分钟中段", time.Unix(1_800_000_030, 0), 32 * time.Second},
		{"下一分钟前一秒", time.Unix(1_800_000_059, 0), 3 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := untilNextFlush(tc.at); got != tc.want {
				t.Errorf("untilNextFlush(%v) = %v，期望 %v", tc.at.Unix(), got, tc.want)
			}
		})
	}
}

func TestUntilNextRollup(t *testing.T) {
	hour := time.Unix(1_800_000_000, 0).Truncate(time.Hour)

	tests := []struct {
		name string
		at   time.Time
		want time.Duration
	}{
		{"整点", hour, 5 * time.Minute},
		{"第 5 分钟整", hour.Add(5 * time.Minute), time.Hour},
		{"第 10 分钟", hour.Add(10 * time.Minute), 55 * time.Minute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := untilNextRollup(tc.at); got != tc.want {
				t.Errorf("untilNextRollup = %v，期望 %v", got, tc.want)
			}
		})
	}
}
