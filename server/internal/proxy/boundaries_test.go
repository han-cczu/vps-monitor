package proxy

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

func boundaryFixture(t *testing.T, now *time.Time) (*Service, *store.DB, *Stats, *Enforcer, int64, int64, *[]hub.Event) {
	t.Helper()
	s, db, notices, nodes := setup(t)
	s.now = func() time.Time { return *now }
	i := makeInbound(t, s, nodes[0], "shadowsocks", 28443)
	u := makeSubscriber(t, s)
	if _, err := s.Assign(context.Background(), u.ID, []int64{i.ID}); err != nil {
		t.Fatal(err)
	}
	events := []hub.Event{}
	e := NewEnforcer(db, notices, func(ev hub.Event) { events = append(events, ev) })
	e.now = s.now
	stats := NewStats(db)
	stats.now = s.now
	stats.BeforeIngest = e.EnsurePeriods
	return s, db, stats, e, nodes[0], u.ID, &events
}

func usageMessage(id, down int64) proto.CoreStats {
	return proto.CoreStats{Type: proto.TypeCoreStats, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%d", id), Down: down}}}
}

func TestFirstPostMidnightSampleSurvivesLaterRolloverAndManualReset(t *testing.T) {
	ctx := context.Background()
	loc := time.FixedZone("panel", 8*3600)
	now := time.Date(2026, 8, 31, 23, 59, 50, 0, loc)
	s, db, stats, e, node, user, _ := boundaryFixture(t, &now)
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, loc).Unix()
	if _, err := db.Exec(`UPDATE subscribers SET reset_day=1,period_start=?,period_date='2026-08-01',traffic_used=20 WHERE id=?`, old, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_traffic(subscriber_id,server_id,period_start,down_bytes) VALUES(?,?,?,20)`, user, node, old); err != nil {
		t.Fatal(err)
	}
	if err := e.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// This old-period sample remains queued when the new period begins.
	if err := stats.Ingest(ctx, node, usageMessage(user, 5)); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 9, 1, 0, 0, 10, 0, loc)
	if err := stats.Ingest(ctx, node, usageMessage(user, 25)); err != nil {
		t.Fatal(err)
	}
	beforeFlush, err := db.Proxy().Subscriber(ctx, user)
	if err != nil || beforeFlush.PeriodDate != "2026-09-01" || beforeFlush.PeriodStart != time.Date(2026, 9, 1, 0, 0, 0, 0, loc).Unix() {
		t.Fatalf("first sample did not advance period: %+v %v", beforeFlush, err)
	}
	// Reproduce the actual scheduler order: flush first, then its AfterStats hook.
	now = now.Add(30 * time.Second)
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.Proxy().Subscriber(ctx, user)
	if err != nil || got.TrafficUsed != 25 {
		t.Fatalf("new-period usage erased or old queue counted: %+v %v", got, err)
	}
	var current, historical int64
	if err := db.QueryRow(`SELECT down_bytes FROM subscriber_traffic WHERE subscriber_id=? AND period_start=?`, user, got.PeriodStart).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT down_bytes FROM subscriber_traffic WHERE subscriber_id=? AND period_start=?`, user, old).Scan(&historical); err != nil {
		t.Fatal(err)
	}
	if current != 25 || historical != 20 {
		t.Fatalf("current=%d history=%d", current, historical)
	}
	if err := stats.Ingest(ctx, node, usageMessage(user, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscriberAction(ctx, user, "reset-usage"); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Proxy().Subscriber(ctx, user)
	if got.TrafficUsed != 0 || got.PeriodDate != "2026-09-01" {
		t.Fatalf("manual reset restored queued samples: %+v", got)
	}
	if err := stats.Ingest(ctx, node, usageMessage(user, 3)); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Proxy().Subscriber(ctx, user)
	if got.TrafficUsed != 3 {
		t.Fatalf("fresh post-reset sample lost: %+v", got)
	}
}

func TestMidMonthTimezoneChangeDoesNotResetAnchoredPeriod(t *testing.T) {
	ctx := context.Background()
	loc := time.FixedZone("panel", 8*3600)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, loc)
	_, db, _, e, _, user, events := boundaryFixture(t, &now)
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, loc).Unix()
	// Simulate an existing database from before the date-anchor column.
	if _, err := db.Exec(`UPDATE subscribers SET reset_day=1,period_start=?,period_date='',traffic_used=123 WHERE id=?`, old, user); err != nil {
		t.Fatal(err)
	}
	if err := e.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Proxy().Subscriber(ctx, user)
	if got.PeriodDate != "2026-09-01" {
		t.Fatalf("legacy timestamp interpreted in wrong timezone: %+v", got)
	}
	if err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.In(time.UTC)
	if err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// A restart under the new timezone must not re-interpret the old timestamp.
	restarted := NewEnforcer(db, nil, nil)
	restarted.now = e.now
	if err := restarted.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Proxy().Subscriber(ctx, user)
	if got.TrafficUsed != 123 || got.PeriodStart != old || got.PeriodDate != "2026-09-01" || len(*events) != 0 {
		t.Fatalf("timezone change reset existing period: %+v events=%v", got, *events)
	}
	now = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for range 2 {
		if err := restarted.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = db.Proxy().Subscriber(ctx, user)
	var resets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='subscriber.period_reset'`).Scan(&resets); err != nil {
		t.Fatal(err)
	}
	if got.TrafficUsed != 0 || got.PeriodDate != "2026-10-01" || got.PeriodStart != now.Unix() || resets != 1 {
		t.Fatalf("next real boundary wrong: %+v resets=%d", got, resets)
	}
}

func TestBoundaryFailureDoesNotCachePublishOrAcceptSample(t *testing.T) {
	ctx := context.Background()
	loc := time.FixedZone("panel", 8*3600)
	now := time.Date(2026, 9, 1, 0, 0, 10, 0, loc)
	_, db, stats, e, node, user, events := boundaryFixture(t, &now)
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, loc).Unix()
	if _, err := db.Exec(`UPDATE subscribers SET reset_day=1,period_start=?,period_date='2026-08-01',traffic_limit=100,traffic_used=120,auto_disabled='quota',warn80_sent=1 WHERE id=?`, old, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_boundary BEFORE UPDATE OF period_date ON subscribers WHEN new.period_date <> old.period_date BEGIN SELECT RAISE(ABORT,'fail boundary'); END`); err != nil {
		t.Fatal(err)
	}
	notices := e.notifier.(*notices)
	notices.take()
	if err := stats.Ingest(ctx, node, usageMessage(user, 25)); err == nil {
		t.Fatal("expected boundary failure")
	}
	got, _ := db.Proxy().Subscriber(ctx, user)
	if got.TrafficUsed != 120 || got.AutoDisabled != "quota" || got.PeriodDate != "2026-08-01" || len(*events) != 0 || len(stats.pending) != 0 || e.periodDay != "" {
		t.Fatalf("failed transaction leaked state: %+v events=%v date=%s pending=%v", got, *events, e.periodDay, stats.pending)
	}
	if ids := notices.take(); len(ids) != 0 {
		t.Fatalf("failed boundary notified nodes: %v", ids)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_boundary`); err != nil {
		t.Fatal(err)
	}
	if err := stats.Ingest(ctx, node, usageMessage(user, 25)); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Proxy().Subscriber(ctx, user)
	if got.TrafficUsed != 25 || got.AutoDisabled != "none" || len(*events) != 1 || (*events)[0].Kind != "subscriber.restored" {
		t.Fatalf("retry failed: %+v events=%v", got, *events)
	}
	if ids := notices.take(); len(ids) != 1 || ids[0] != node {
		t.Fatalf("committed boundary did not notify once: %v", ids)
	}
	// Once this date is prepared, no per-sample all-user writer transaction runs.
	if _, err := db.Exec(`CREATE TRIGGER block_daily_write BEFORE UPDATE ON settings WHEN old.key='enforce.last_rollover_date' BEGIN SELECT RAISE(ABORT,'unexpected daily write'); END`); err != nil {
		t.Fatal(err)
	}
	if err := stats.Ingest(ctx, node, usageMessage(user, 5)); err != nil {
		t.Fatalf("same-date sample repeated calendar writer: %v", err)
	}
}

func TestConcurrentFirstSamplesShareOneBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 10, 0, time.UTC)
	_, db, stats, _, node, user, _ := boundaryFixture(t, &now)
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Unix()
	if _, err := db.Exec(`UPDATE subscribers SET reset_day=1,period_start=?,period_date='2026-08-01',traffic_used=80 WHERE id=?`, old, user); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errors <- stats.Ingest(ctx, node, usageMessage(user, 1)) })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Proxy().Subscriber(ctx, user)
	var resets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='subscriber.period_reset'`).Scan(&resets); err != nil {
		t.Fatal(err)
	}
	if got.TrafficUsed != 8 || resets != 1 {
		t.Fatalf("concurrent boundary lost usage or repeated reset: %+v resets=%d", got, resets)
	}
}
