package billing

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

func date(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
func TestRenewCalendarAndCatchup(t *testing.T) {
	for _, tc := range []struct{ expire, today, cycle, want string }{
		{"2024-01-31", "2024-03-01", "month", "2024-03-31"},
		{"2024-02-29", "2026-09-14", "year", "2027-02-28"},
		{"2025-10-31", "2026-09-14", "quarter", "2026-10-31"},
		{"2025-01-01", "2026-09-14", "once", "2025-01-01"},
		{"2026-09-14", "2026-09-14", "month", "2026-09-14"},
	} {
		if got := Renew(date(tc.expire), date(tc.today), tc.cycle).Format("2006-01-02"); got != tc.want {
			t.Errorf("%+v got %s", tc, got)
		}
	}
}

func TestRenewAuditAndDailyReminderAreIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	expired := "2025-09-13"
	soon := "2026-09-21"
	in := store.ServerInput{Name: "renew", Currency: "CNY", BillingCycle: "year", ExpireAt: &expired, AutoRenew: true, TrafficMode: "max", TrafficResetDay: 1}
	renewing, err := db.CreateServer(ctx, in, "renew")
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "reminder"
	in.ExpireAt = &soon
	in.AutoRenew = false
	if _, err := db.CreateServer(ctx, in, "reminder"); err != nil {
		t.Fatal(err)
	}
	in.Name = "once"
	in.ExpireAt = &expired
	in.AutoRenew = true
	in.BillingCycle = "once"
	once, err := db.CreateServer(ctx, in, "once")
	if err != nil {
		t.Fatal(err)
	}
	bus := &hub.Bus{}
	events := bus.Subscribe(10)
	invalidations := 0
	s := New(db, bus, func() { invalidations++ })
	for range 2 {
		if err := s.Check(ctx, date("2026-09-14")); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := db.GetServer(ctx, renewing.ID)
	if *got.ExpireAt != "2027-09-13" || invalidations != 1 {
		t.Fatalf("renew=%s invalidations=%d", *got.ExpireAt, invalidations)
	}
	got, _ = db.GetServer(ctx, once.ID)
	if *got.ExpireAt != expired {
		t.Fatal("once renewed")
	}
	audit, err := db.ListAudit(ctx, 10, 0)
	if err != nil || len(audit) != 1 || audit[0].Actor != "system" || audit[0].Action != "server.auto_renew" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
	if len(events) != 1 {
		t.Fatalf("duplicate reminders=%d", len(events))
	}
	ev := <-events
	if ev.Threshold != 7 || ev.Kind != hub.EventServerExpiringSoon {
		t.Fatalf("event=%+v", ev)
	}
	// New service instance uses persisted marks, not only in-memory dates.
	if err := New(db, bus, nil).Check(ctx, date("2026-09-14")); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("restart duplicated reminder")
	}
}
