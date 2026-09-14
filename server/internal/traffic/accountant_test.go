package traffic

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

func testAccountant(t *testing.T, now *time.Time) (*Accountant, *store.DB, int64, <-chan hub.Event) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := db.CreateServer(context.Background(), store.ServerInput{Name: "test", Currency: "CNY", BillingCycle: "month", TrafficLimit: 100, TrafficMode: "max", TrafficResetDay: 31}, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	bus := &hub.Bus{}
	events := bus.Subscribe(10)
	a := New(db, bus)
	a.now = func() time.Time { return *now }
	if err := a.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, db, s.ID, events
}
func sample(ts, rx, tx int64) *proto.Metrics {
	return &proto.Metrics{TS: ts, Net: proto.NetStat{RxTotal: rx, TxTotal: tx}}
}

func TestUsedModes(t *testing.T) {
	p := store.TrafficPeriod{In: 40, Out: 60}
	for mode, want := range map[string]int64{"in": 40, "out": 60, "sum": 100, "max": 60} {
		if got := Used(p, mode); got != want {
			t.Errorf("%s=%d want %d", mode, got, want)
		}
	}
}

func TestCounterResetDuplicateAndRestartRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	a, db, id, events := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 1000, 2000))
	if a.SnapshotFor(id).Used != 0 {
		t.Fatal("first sample counted")
	}
	a.OnMetrics(id, sample(2, 1040, 2060))
	a.OnMetrics(id, sample(2, 0, 0))
	a.OnMetrics(id, sample(1, 0, 0))
	if a.SnapshotFor(id).Used != 60 {
		t.Fatal("duplicate/stale sample counted")
	}
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := New(db, a.bus)
	restarted.now = a.now
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.OnMetrics(id, sample(3, 1060, 2080))
	if got := restarted.SnapshotFor(id); got.In != 60 || got.Out != 80 {
		t.Fatalf("restart lost baseline: %+v", got)
	}
	restarted.OnMetrics(id, sample(4, 10, 15))
	if got := restarted.SnapshotFor(id); got.In != 70 || got.Out != 95 {
		t.Fatalf("machine reset: %+v", got)
	}
	got := []int{}
	for len(events) > 0 {
		got = append(got, (<-events).Threshold)
	}
	if len(got) != 2 || got[0] != 80 || got[1] != 90 {
		t.Fatalf("threshold crossings: %v", got)
	}
	if err := restarted.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.TrafficHistory(ctx, id, 12)
	if err != nil || len(rows) != 1 || rows[0].Out != 95 {
		t.Fatalf("persisted rows=%+v err=%v", rows, err)
	}
}

func TestMonthEndRolloverCatchupAndIdempotence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 100, 100))
	a.OnMetrics(id, sample(2, 125, 160))
	now = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := a.Rollover(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := a.Rollover(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows, err := db.TrafficHistory(ctx, id, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("period count=%d", len(rows))
	}
	if rows[0].Start != time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC).Unix() || rows[1].Start != time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("month-end clamp: %+v", rows)
	}
	if rows[0].End != nil || rows[1].End == nil || rows[2].End == nil || rows[2].Out != 60 {
		t.Fatalf("period history: %+v", rows)
	}
	// A persisted counter survives rollover; the next delta belongs to the current period.
	a.OnMetrics(id, sample(3, 130, 170))
	if a.SnapshotFor(id).Used != 10 {
		t.Fatal("rollover lost baseline")
	}
}

func TestFlushFailureRetainsDirtyStateAndDeletedNodeDoesNotPoisonFlush(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 1, 1))
	a.OnMetrics(id, sample(2, 20, 30))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.Flush(cancelled); err == nil {
		t.Fatal("expected canceled transaction")
	}
	if !a.dirty {
		t.Fatal("failed flush discarded pending totals")
	}
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteServer(ctx, id); err != nil {
		t.Fatal(err)
	}
	a.OnMetrics(id, sample(3, 40, 50))
	if err := a.Flush(ctx); err != nil {
		t.Fatalf("deleted node blocked flush: %v", err)
	}
}
