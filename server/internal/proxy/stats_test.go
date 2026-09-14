package proxy

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

func TestStatsAccountingResetAndFailureRecovery(t *testing.T) {
	r, s, db, _, id, i, sub := reconcileSetup(t)
	ctx := context.Background()
	stats := r.Stats
	stats.now = func() time.Time { return time.Date(2026, 9, 15, 0, 1, 0, 0, time.FixedZone("panel", 8*3600)) }
	message := func(up, down int64) proto.CoreStats {
		return proto.CoreStats{Type: proto.TypeCoreStats, TS: 1, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%d", sub.ID), Up: up, Down: down}}, Inbounds: []proto.Counter{{Name: i.Tag, Up: up, Down: down}}}
	}
	if err := stats.Ingest(ctx, id, message(1234, 50<<20)); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(want int64) {
		t.Helper()
		got, err := db.Proxy().Subscriber(ctx, sub.ID)
		if err != nil || got.TrafficUsed != want {
			t.Fatalf("used=%v expected=%d err=%v", got, want, err)
		}
	}
	check(1234 + 50<<20)
	var date string
	if err := db.QueryRow("SELECT date FROM subscriber_traffic_daily WHERE subscriber_id=?", sub.ID).Scan(&date); err != nil || date != "2026-09-15" {
		t.Fatal("used untrusted agent timestamp or UTC day", date, err)
	}
	if err := stats.Ingest(ctx, id, message(100, 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscriberAction(ctx, sub.ID, "reset-usage"); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	check(0)
	if err := stats.Ingest(ctx, id, message(10, 20)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_daily BEFORE INSERT ON subscriber_traffic_daily BEGIN SELECT RAISE(ABORT,'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err == nil {
		t.Fatal("expected atomic flush failure")
	}
	check(0)
	if err := stats.Ingest(ctx, id, message(2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_daily"); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	check(35)
	// Removing an assignment still accepts its final report from the applied config.
	rev, err := r.Reconcile(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE node_core SET applied_revision=?,config_sha256=? WHERE server_id=?", rev.Revision, rev.SHA256, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assign(ctx, sub.ID, []int64{}); err != nil {
		t.Fatal(err)
	}
	if err = stats.Ingest(ctx, id, message(3, 4)); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteServer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	check(42)
}
func TestStatsRejectsOtherNodesBadCountersAndCountsConcurrentBatches(t *testing.T) {
	r, _, db, _, id, i, sub := reconcileSetup(t)
	ctx := context.Background()
	stats := r.Stats
	base := proto.CoreStats{Type: proto.TypeCoreStats, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%d", sub.ID), Up: 1, Down: 2}}, Inbounds: []proto.Counter{{Name: i.Tag, Up: 1, Down: 2}}}
	if err := stats.Ingest(ctx, id+1, base); err != nil {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Proxy().Subscriber(ctx, sub.ID)
	if got.TrafficUsed != 0 {
		t.Fatal("foreign node charged user")
	}
	for _, c := range []proto.Counter{{Name: "sub-1", Up: -1}, {Name: "sub-1", Down: 1 << 51}} {
		bad := base
		bad.Users = []proto.Counter{c}
		if err := stats.Ingest(ctx, id, bad); err == nil {
			t.Fatal("bad count accepted")
		}
	}
	var wg sync.WaitGroup
	failures := make(chan error, 60)
	for range 50 {
		wg.Go(func() {
			if err := stats.Ingest(ctx, id, base); err != nil {
				failures <- err
			}
		})
	}
	for range 5 {
		wg.Go(func() {
			if err := stats.Flush(ctx); err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if err := stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Proxy().Subscriber(ctx, sub.ID)
	if got.TrafficUsed != 150 {
		t.Fatal("lost concurrent batch", got.TrafficUsed)
	}
	counters := stats.Inbounds(id)
	if len(counters) != 1 || counters[0].Up != 50 || counters[0].Down != 100 {
		t.Fatal(counters)
	}
}
func TestStatsDeletedSubscriberAndPeriodChange(t *testing.T) {
	r, _, db, _, id, _, sub := reconcileSetup(t)
	ctx := context.Background()
	msg := proto.CoreStats{Type: proto.TypeCoreStats, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%d", sub.ID), Up: 10}}}
	if err := r.Stats.Ingest(ctx, id, msg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE subscribers SET period_start=period_start+86400 WHERE id=?", sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Proxy().Subscriber(ctx, sub.ID)
	if got.TrafficUsed != 0 {
		t.Fatal("old-period sample entered new period")
	}
	if err := r.Stats.Ingest(ctx, id, msg); err != nil {
		t.Fatal(err)
	}
	if err := db.WithProxyTx(ctx, func(q store.ProxyQueries) error { return q.DeleteSubscriber(ctx, sub.ID) }); err != nil {
		t.Fatal(err)
	}
	if err := r.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM subscriber_traffic").Scan(&n); err != nil || n != 0 {
		t.Fatal("deleted user resurrected")
	}
}

func TestStatsCloseDrainsAcceptedSamples(t *testing.T) {
	r, _, db, _, id, _, sub := reconcileSetup(t)
	ctx := context.Background()
	msg := proto.CoreStats{Type: proto.TypeCoreStats, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%d", sub.ID), Up: 123}}}
	if err := r.Stats.Ingest(ctx, id, msg); err != nil {
		t.Fatal(err)
	}
	if err := r.Stats.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Stats.Ingest(ctx, id, msg); err == nil {
		t.Fatal("shutdown accepted unflushable batch")
	}
	got, err := db.Proxy().Subscriber(ctx, sub.ID)
	if err != nil || got.TrafficUsed != 123 {
		t.Fatal("final flush lost accepted sample", err)
	}
}
