package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/hub"
)

func TestEnforcerWarningsAtomicityAndNodeDeduplication(t *testing.T) {
	s, db, n, nodes := setup(t)
	ctx := context.Background()
	in := makeInbound(t, s, nodes[0], "shadowsocks", 8433)
	ids := []int64{}
	for range 3 {
		u := makeSubscriber(t, s)
		if _, err := s.Assign(ctx, u.ID, []int64{in.ID}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	if _, err := db.Exec("UPDATE subscribers SET traffic_limit=100,traffic_used=80"); err != nil {
		t.Fatal(err)
	}
	n.take()
	events := []hub.Event{}
	e := NewEnforcer(db, n, func(ev hub.Event) { events = append(events, ev) })
	e.now = s.now
	for range 2 {
		if err := e.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 3 || len(n.take()) != 0 {
		t.Fatalf("warnings repeated or rendered: %+v", events)
	}
	for _, ev := range events {
		if ev.Kind != "subscriber.quota" || ev.Threshold != 80 {
			t.Fatal(ev)
		}
	}
	events = nil
	if _, err := db.Exec("UPDATE subscribers SET traffic_used=100"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_policy BEFORE UPDATE OF auto_disabled ON subscribers WHEN new.id=%d BEGIN SELECT RAISE(ABORT,'write failure'); END`, ids[1])); err != nil {
		t.Fatal(err)
	}
	if err := e.RunOnce(ctx); err == nil {
		t.Fatal("expected write failure")
	}
	first, err := db.Proxy().Subscriber(ctx, ids[0])
	if err != nil || first.AutoDisabled != "none" || len(events) != 0 || len(n.take()) != 0 {
		t.Fatalf("transaction leaked: %+v %v events=%+v", first, err, events)
	}
	if _, err := db.Exec("DROP TRIGGER fail_policy"); err != nil {
		t.Fatal(err)
	}
	if err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := n.take(); len(got) != 1 || got[0] != nodes[0] {
		t.Fatalf("node notifications not deduplicated: %v", got)
	}
	if len(events) != 3 {
		t.Fatal(events)
	}
	for _, id := range ids {
		u, err := db.Proxy().Subscriber(ctx, id)
		if err != nil || u.AutoDisabled != "quota" || u.SubToken == "" || u.UUID == "" {
			t.Fatalf("bad state/credentials after job: %+v %v", u, err)
		}
	}
}

func TestEnforcerMissedMonthEndResetKeepsHistoryAndManualState(t *testing.T) {
	r, s, db, _, node, _, u := reconcileSetup(t)
	ctx := context.Background()
	loc := s.now().Location()
	old := time.Date(2026, 8, 31, 0, 0, 0, 0, loc).Unix()
	if _, err := db.Exec("UPDATE subscribers SET reset_day=31,period_start=?,traffic_limit=100,traffic_used=120,auto_disabled='quota',warn80_sent=1 WHERE id=?", old, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO subscriber_traffic(subscriber_id,server_id,period_start,up_bytes,down_bytes) VALUES(?,?,?,?,?)", u.ID, node, old, 20, 100); err != nil {
		t.Fatal(err)
	}
	if err := r.Stats.Ingest(ctx, node, proto.CoreStats{Type: proto.TypeCoreStats, Users: []proto.Counter{{Name: fmt.Sprintf("sub-%d", u.ID), Down: 25}}}); err != nil {
		t.Fatal(err)
	}
	manual := makeSubscriber(t, s)
	if _, err := db.Exec("UPDATE subscribers SET enabled=0,auto_disabled='quota',reset_day=31,period_start=?,traffic_used=120,warn80_sent=1 WHERE id=?", old, manual.ID); err != nil {
		t.Fatal(err)
	}
	once := makeSubscriber(t, s)
	if _, err := db.Exec("UPDATE subscribers SET reset_day=0,period_start=?,traffic_used=120 WHERE id=?", old, once.ID); err != nil {
		t.Fatal(err)
	}
	e := NewEnforcer(db, nil, nil)
	e.now = func() time.Time { return time.Date(2026, 11, 2, 10, 0, 0, 0, loc) }
	for range 2 {
		if err := e.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.Proxy().Subscriber(ctx, u.ID)
	if err != nil || got.PeriodStart != time.Date(2026, 10, 31, 0, 0, 0, 0, loc).Unix() || got.TrafficUsed != 0 || got.Warn80Sent || got.AutoDisabled != "none" {
		t.Fatalf("catchup failed: %+v %v", got, err)
	}
	m, _ := db.Proxy().Subscriber(ctx, manual.ID)
	if m.Enabled || m.AutoDisabled != "quota" || m.TrafficUsed != 0 {
		t.Fatalf("manual intent lost: %+v", m)
	}
	o, _ := db.Proxy().Subscriber(ctx, once.ID)
	if o.TrafficUsed != 120 || o.PeriodStart != old {
		t.Fatalf("one-time quota reset: %+v", o)
	}
	var history, resets int
	if err := db.QueryRow("SELECT COUNT(*) FROM subscriber_traffic WHERE subscriber_id=? AND period_start=?", u.ID, old).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='subscriber.period_reset'").Scan(&resets); err != nil {
		t.Fatal(err)
	}
	if history != 1 || resets != 2 {
		t.Fatalf("history=%d resets=%d", history, resets)
	}
	if _, err := db.Exec("UPDATE subscribers SET traffic_used=80 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	events := []hub.Event{}
	e.publish = func(ev hub.Event) { events = append(events, ev) }
	if err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Threshold != 80 {
		t.Fatalf("new period warning missing: %+v", events)
	}
}

func TestSubscriberPolicyImmediateRestoreExpiryAndReset(t *testing.T) {
	s, db, _, _ := setup(t)
	ctx := context.Background()
	u := makeSubscriber(t, s)
	events := []hub.Event{}
	s.Events = func(ev hub.Event) { events = append(events, ev) }
	if _, err := db.Exec("UPDATE subscribers SET traffic_limit=100,traffic_used=100,auto_disabled='quota' WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.SaveSubscriber(ctx, u.ID, SubscriberInput{TrafficLimit: ptr(int64(200)), ResetDay: ptr(31)})
	if err != nil || got.Status != "active" || got.NextResetDate == nil || *got.NextResetDate != "2026-09-30" {
		t.Fatalf("no immediate restore: %+v %v", got, err)
	}
	if len(events) != 1 || events[0].Kind != "subscriber.restored" {
		t.Fatal(events)
	}
	got, err = s.SaveSubscriber(ctx, u.ID, SubscriberInput{ExpireAt: json.RawMessage(`"2026-09-14"`)})
	if err != nil || got.Status != "expired" {
		t.Fatalf("expiry not immediate: %+v %v", got, err)
	}
	got, err = s.SubscriberAction(ctx, u.ID, "reset-usage")
	if err != nil || got.Status != "expired" || got.TrafficUsed != 0 {
		t.Fatalf("reset revived expired user: %+v %v", got, err)
	}
	got, err = s.SaveSubscriber(ctx, u.ID, SubscriberInput{Enabled: ptr(false), ExpireAt: json.RawMessage(`null`)})
	if err != nil || got.Status != "disabled" || got.AutoDisabled != "expired" {
		t.Fatalf("manual state lost: %+v %v", got, err)
	}
	got, err = s.SaveSubscriber(ctx, u.ID, SubscriberInput{Enabled: ptr(true)})
	if err != nil || got.Status != "active" {
		t.Fatalf("re-enable not evaluated: %+v %v", got, err)
	}
}
