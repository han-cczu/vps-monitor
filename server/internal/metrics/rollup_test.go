package metrics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

// ----------------------------------------------------------------------

// 用整点做基准，算出来的 ts 都好对。
const baseHour = int64(1_800_000_000) // 恰好是整分，且 1800000000 % 3600 == 0

func openDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "vm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// 指标表有外键指向 servers，先建一台
	if _, err := db.CreateServer(ctx,
		store.ServerInput{Name: "n1", Currency: "CNY", BillingCycle: "month", TrafficResetDay: 1, TrafficMode: "sum"},
		auth.HashAgentToken("t1")); err != nil {
		t.Fatal(err)
	}
	return db
}

func newRollup(db *store.DB, now int64) *Rollup {
	r := NewRollup(db)
	r.now = func() time.Time { return time.Unix(now, 0) }
	return r
}

// ----------------------------------------------------------------------

// TestRollupAggregatesCorrectly 是降采样语义的核心用例：
// 平均取平均、峰值取最大、水位取区间内最后一行。
//
// 「水位取最后一行」特意用递增的 disk_used 与 rx_total 来验——如果实现退化成
// 「跟着某个 MAX() 的裸列」，取到的会是 cpu_max 最大那一行（这里故意安排成第二行），
// 而不是 ts 最大那一行。
func TestRollupAggregatesCorrectly(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	rows := []store.MetricRow{
		{ServerID: 1, TS: baseHour, CPUAvg: 10, CPUMax: 20, MemUsed: 100, RxRateAvg: 10, RxRateMax: 15,
			DiskUsed: 1000, RxTotal: 5000, TxTotal: 500, TCP: 1, UDP: 1, Procs: 101, Samples: 60},
		// 第二行的 cpu_max 最大，但它不是最后一行
		{ServerID: 1, TS: baseHour + 60, CPUAvg: 30, CPUMax: 90, MemUsed: 300, RxRateAvg: 30, RxRateMax: 99,
			DiskUsed: 2000, RxTotal: 6000, TxTotal: 600, TCP: 2, UDP: 2, Procs: 102, Samples: 60},
		{ServerID: 1, TS: baseHour + 120, CPUAvg: 20, CPUMax: 25, MemUsed: 200, RxRateAvg: 20, RxRateMax: 22,
			DiskUsed: 3000, RxTotal: 7000, TxTotal: 700, TCP: 3, UDP: 3, Procs: 103, Samples: 60},
	}
	if err := db.InsertMinuteRows(ctx, rows); err != nil {
		t.Fatal(err)
	}

	n, err := db.RollupHours(ctx, baseHour, baseHour+3600)
	if err != nil {
		t.Fatalf("RollupHours: %v", err)
	}
	if n != 1 {
		t.Fatalf("期望写入 1 行小时行，得到 %d", n)
	}

	got, err := db.QueryMetrics(ctx, store.TableMetricsHour, 1, baseHour, baseHour+3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("期望读到 1 行，得到 %d", len(got))
	}
	h := got[0]

	if h.TS != baseHour {
		t.Errorf("小时行的 ts 应当是整点 %d，得到 %d", baseHour, h.TS)
	}
	if h.CPUAvg != 20 {
		t.Errorf("cpu_avg 应当是 (10+30+20)/3=20，得到 %v", h.CPUAvg)
	}
	if h.CPUMax != 90 {
		t.Errorf("cpu_max 应当取最大 90，得到 %v", h.CPUMax)
	}
	if h.MemUsed != 200 {
		t.Errorf("mem_used 应当是 200，得到 %d", h.MemUsed)
	}
	if h.RxRateAvg != 20 || h.RxRateMax != 99 {
		t.Errorf("速率应当是 avg=20 max=99，得到 %d / %d", h.RxRateAvg, h.RxRateMax)
	}
	if h.Samples != 3 {
		t.Errorf("samples 应当是分钟行数 3，得到 %d", h.Samples)
	}

	// 关键断言：水位类必须来自 ts 最大的那一行（第三行），不是 cpu_max 最大的第二行
	if h.DiskUsed != 3000 {
		t.Errorf("disk_used 应当取最后一行 3000，得到 %d（取到 2000 说明依赖了裸列跟随 max 的行为）", h.DiskUsed)
	}
	if h.RxTotal != 7000 || h.TxTotal != 700 {
		t.Errorf("累计流量应当取最后一行 7000/700，得到 %d/%d", h.RxTotal, h.TxTotal)
	}
	if h.TCP != 3 || h.UDP != 3 || h.Procs != 103 {
		t.Errorf("连接数/进程数应当取最后一行 3/3/103，得到 %d/%d/%d", h.TCP, h.UDP, h.Procs)
	}
}

// TestRollupSplitsByHour 保证跨小时的分钟行被分到各自的整点。
func TestRollupSplitsByHour(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	rows := []store.MetricRow{
		{ServerID: 1, TS: baseHour, CPUAvg: 10, Samples: 60},
		{ServerID: 1, TS: baseHour + 1800, CPUAvg: 20, Samples: 60},
		{ServerID: 1, TS: baseHour + 3600, CPUAvg: 50, Samples: 60},
		{ServerID: 1, TS: baseHour + 7200, CPUAvg: 70, Samples: 60},
	}
	if err := db.InsertMinuteRows(ctx, rows); err != nil {
		t.Fatal(err)
	}

	if _, err := db.RollupHours(ctx, baseHour, baseHour+3*3600); err != nil {
		t.Fatal(err)
	}

	got, err := db.QueryMetrics(ctx, store.TableMetricsHour, 1, baseHour, baseHour+3*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("三个小时应当各一行，得到 %d 行", len(got))
	}
	want := []struct {
		ts  int64
		cpu float64
	}{
		{baseHour, 15}, // (10+20)/2
		{baseHour + 3600, 50},
		{baseHour + 7200, 70},
	}
	for i, w := range want {
		if got[i].TS != w.ts || got[i].CPUAvg != w.cpu {
			t.Errorf("第 %d 行应当是 ts=%d cpu=%v，得到 ts=%d cpu=%v", i, w.ts, w.cpu, got[i].TS, got[i].CPUAvg)
		}
	}
}

// TestRollupIsIdempotent 保证重复跑同一小时不会出重复行或把数值叠加。
func TestRollupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	if err := db.InsertMinuteRows(ctx, []store.MetricRow{
		{ServerID: 1, TS: baseHour, CPUAvg: 42, CPUMax: 50, DiskUsed: 7, Samples: 60},
	}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if _, err := db.RollupHours(ctx, baseHour, baseHour+3600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.QueryMetrics(ctx, store.TableMetricsHour, 1, baseHour, baseHour+3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("重复降采样不该产生多行，得到 %d 行", len(got))
	}
	if got[0].CPUAvg != 42 || got[0].DiskUsed != 7 {
		t.Errorf("数值不该被叠加，得到 cpu=%v disk=%d", got[0].CPUAvg, got[0].DiskUsed)
	}
}

// TestCatchUpFillsGap 覆盖验收第 6 条：面板重启后补齐停机期间的小时行。
func TestCatchUpFillsGap(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	// 停机期间仍然有分钟行（实际是停机前写下的），小时表停在第一个小时
	rows := []store.MetricRow{}
	for h := range int64(4) {
		rows = append(rows, store.MetricRow{ServerID: 1, TS: baseHour + h*3600, CPUAvg: float64(h + 1), Samples: 60})
	}
	if err := db.InsertMinuteRows(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RollupHours(ctx, baseHour, baseHour+3600); err != nil {
		t.Fatal(err)
	}

	// 现在是第 5 个小时的中段：应当把第 2、3、4 个小时补上
	r := newRollup(db, baseHour+4*3600+600)
	if err := r.CatchUp(ctx); err != nil {
		t.Fatalf("CatchUp: %v", err)
	}

	got, err := db.QueryMetrics(ctx, store.TableMetricsHour, 1, baseHour, baseHour+5*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("应当补齐成 4 行，得到 %d 行", len(got))
	}
	for i, row := range got {
		if row.TS != baseHour+int64(i)*3600 {
			t.Errorf("第 %d 行 ts 不对: %d", i, row.TS)
		}
	}
}

// TestCatchUpSkipsWhenNothingToDo 保证没有数据时不做无谓的写入。
func TestCatchUpSkipsWhenNothingToDo(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	r := newRollup(db, baseHour+3600)
	if err := r.CatchUp(ctx); err != nil {
		t.Fatalf("空库补跑不该报错: %v", err)
	}

	got, err := db.QueryMetrics(ctx, store.TableMetricsHour, 1, 0, baseHour+10*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("空库不该补出行，得到 %d 行", len(got))
	}
}

// TestCleanupRespectsRetention 覆盖验收第 3 条：过期的分钟行被删掉。
func TestCleanupRespectsRetention(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := baseHour + 30*86400 // 让 baseHour 之后的行都有足够的"年龄"
	rows := []store.MetricRow{
		{ServerID: 1, TS: now - 8*86400, CPUAvg: 1, Samples: 60}, // 8 天前，默认 7 天保留期外
		{ServerID: 1, TS: now - 6*86400, CPUAvg: 2, Samples: 60}, // 6 天前，保留
		{ServerID: 1, TS: now - 3600, CPUAvg: 3, Samples: 60},    // 一小时前，保留
	}
	if err := db.InsertMinuteRows(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RollupHours(ctx, 0, now); err != nil {
		t.Fatal(err)
	}

	r := newRollup(db, now)
	if err := r.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	left, err := db.QueryMetrics(ctx, store.TableMetricsMinute, 1, 0, now+3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("分钟表应当只剩 2 行，得到 %d 行", len(left))
	}
	if left[0].TS != now-6*86400 {
		t.Errorf("留下的应当是 6 天前那行，得到 ts=%d", left[0].TS)
	}

	// 小时表默认留 365 天，这几行都在期内
	hours, err := db.QueryMetrics(ctx, store.TableMetricsHour, 1, 0, now+3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 3 {
		t.Errorf("小时表默认留 365 天，应当一行不少（3 行），得到 %d", len(hours))
	}
}

// TestCleanupUsesSettingAndIgnoresNonsense 覆盖保留期可配置，以及非法值不生效。
func TestCleanupUsesSettingAndIgnoresNonsense(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := baseHour + 30*86400
	if err := db.InsertMinuteRows(ctx, []store.MetricRow{
		{ServerID: 1, TS: now - 3*86400, CPUAvg: 1, Samples: 60},
		{ServerID: 1, TS: now - 3600, CPUAvg: 2, Samples: 60},
	}); err != nil {
		t.Fatal(err)
	}

	// 保留 1 天：3 天前那行该被删
	if err := db.SetSetting(ctx, SettingMinuteDays, 1); err != nil {
		t.Fatal(err)
	}
	r := newRollup(db, now)
	if err := r.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	left, _ := db.QueryMetrics(ctx, store.TableMetricsMinute, 1, 0, now+3600)
	if len(left) != 1 {
		t.Fatalf("保留 1 天时应当只剩 1 行，得到 %d 行", len(left))
	}

	// 非法值（0）不能被当真——那会把历史一次性删光
	if err := db.SetSetting(ctx, SettingMinuteDays, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	left, _ = db.QueryMetrics(ctx, store.TableMetricsMinute, 1, 0, now+3600)
	if len(left) != 1 {
		t.Errorf("保留期为 0 应当回落到默认值而不是删光，剩 %d 行", len(left))
	}
}

// TestDeleteMetricsBeforeRejectsUnknownTable 防止表名被当参数拼进 SQL。
func TestDeleteMetricsBeforeRejectsUnknownTable(t *testing.T) {
	db := openDB(t)

	if _, err := db.DeleteMetricsBefore(context.Background(), "users", 0); err == nil {
		t.Error("未知表名应当被拒绝")
	}
	if _, err := db.QueryMetrics(context.Background(), "servers; DROP TABLE users", 1, 0, 1); err == nil {
		t.Error("未知表名应当被拒绝")
	}
}

// TestServerDeleteCascadesMetrics 保证删节点会带走它的历史。
func TestServerDeleteCascadesMetrics(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	if err := db.InsertMinuteRows(ctx, []store.MetricRow{
		{ServerID: 1, TS: baseHour, CPUAvg: 1, Samples: 60},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteServer(ctx, 1); err != nil {
		t.Fatal(err)
	}

	left, err := db.QueryMetrics(ctx, store.TableMetricsMinute, 1, 0, baseHour+3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("删节点应当级联删掉它的指标，还剩 %d 行", len(left))
	}
}
