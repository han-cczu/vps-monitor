package traffic

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"vpsmon/server/internal/store"
)

func date(t *testing.T, value string, loc *time.Location) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(time.DateOnly, value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTrafficResetDates(t *testing.T) {
	for _, tc := range []struct {
		start, mode string
		day         int
		want        string
	}{
		{"2026-08-15", "days", 15, "2026-09-14"},
		{"2026-08-15", "monthly", 15, "2026-09-15"},
		{"2026-01-31", "days", 31, "2026-03-02"},
		{"2026-01-31", "monthly", 31, "2026-02-28"},
		{"2028-01-31", "monthly", 31, "2028-02-29"},
		{"2026-02-28", "monthly", 31, "2026-03-31"},
		{"2026-12-15", "days", 15, "2027-01-14"},
	} {
		if got := NextResetDate(date(t, tc.start, time.UTC), tc.mode, tc.day).Format(time.DateOnly); got != tc.want {
			t.Errorf("%+v: got %s", tc, got)
		}
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := date(t, "2026-03-01", loc)
	next := NextResetDate(start, "days", 1)
	if next.Format("2006-01-02 15:04") != "2026-03-31 00:00" || next.Sub(start) != 719*time.Hour {
		t.Fatalf("DST: %v", next)
	}
}

func scheduleConfig() store.ServerInput {
	return store.ServerInput{Name: "test", Currency: "CNY", BillingCycle: "year", TrafficMode: "max", TrafficResetMode: "days", TrafficResetDay: 31}
}

func TestChangeResetRuleAdvancesPastTodayAndPreservesUsage(t *testing.T) {
	for _, tc := range []struct {
		day  int
		next string
	}{
		{14, "2026-10-14"},
		{15, "2026-10-15"},
		{16, "2026-09-16"},
	} {
		t.Run(tc.next, func(t *testing.T) {
			ctx := context.Background()
			now := date(t, "2026-09-15", time.UTC).Add(12 * time.Hour)
			a, db, id, _ := testAccountant(t, &now)
			a.OnMetrics(id, sample(1, 100, 200))
			a.OnMetrics(id, sample(2, 140, 270))
			before, _ := a.PeriodFor(id)
			config := scheduleConfig()
			config.TrafficResetMode, config.TrafficResetDay = "monthly", tc.day
			expiry := "2027-07-15"
			config.ExpireAt = &expiry
			if _, err := a.UpdateServer(ctx, id, config, nil); err != nil {
				t.Fatalf("changing only the reset day failed: %v", err)
			}
			restarted := New(db, nil)
			restarted.now = a.now
			if err := restarted.Load(ctx); err != nil {
				t.Fatal(err)
			}
			view := restarted.SnapshotFor(id)
			if view.PeriodStart != before.Start || view.PeriodEndExpected != date(t, tc.next, time.UTC).Unix() || view.In != 40 || view.Out != 70 {
				t.Fatalf("rule edit changed usage or selected the wrong reset: %+v", view)
			}
			stored, _ := db.GetServer(ctx, id)
			if stored.BillingCycle != "year" || stored.ExpireAt == nil || *stored.ExpireAt != expiry {
				t.Fatal("traffic schedule changed annual billing")
			}
			now = date(t, tc.next, time.UTC)
			if err := restarted.Rollover(ctx, now); err != nil {
				t.Fatal(err)
			}
			if view := restarted.SnapshotFor(id); view.Used != 0 || view.PeriodStart != now.Unix() {
				t.Fatalf("usage did not reset on the new date: %+v", view)
			}
			rows, err := db.TrafficHistory(ctx, id, 12)
			if err != nil || len(rows) != 2 || rows[1].In != 40 || rows[1].Out != 70 {
				t.Fatalf("closed usage was lost: %+v, %v", rows, err)
			}
		})
	}
}

func TestManualTrafficDatesPreserveUsageAndSurviveRestart(t *testing.T) {
	ctx := context.Background()
	now := date(t, "2026-09-15", time.UTC).Add(12 * time.Hour)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 100, 200))
	a.OnMetrics(id, sample(2, 140, 270)) // pending deltas must survive the date correction
	oldCalibration := calibrationInput(t, a, id, 1, 2)
	p, _ := a.PeriodFor(id)
	input := &ScheduleInput{Start: date(t, "2026-09-14", time.UTC).Unix(), NextReset: date(t, "2026-10-20", time.UTC).Unix(), ExpectedStart: p.Start, Revision: p.CalibrationRevision}
	config := scheduleConfig()
	expiry := "2027-08-15"
	config.ExpireAt = &expiry
	if _, err := a.UpdateServer(ctx, id, config, input); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Calibrate(ctx, id, oldCalibration); !errors.Is(err, ErrCalibrationChanged) {
		t.Fatalf("stale calibration accepted: %v", err)
	}
	if _, err := a.UpdateServer(ctx, id, config, input); !errors.Is(err, ErrScheduleChanged) {
		t.Fatalf("stale date edit accepted: %v", err)
	}
	if _, err := a.Calibrate(ctx, id, calibrationInput(t, a, id, 40, 70)); err != nil {
		t.Fatalf("30-day schedule cannot calibrate usage: %v", err)
	}
	restarted := New(db, nil)
	restarted.now = a.now
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.OnMetrics(id, sample(3, 150, 290))
	view := restarted.SnapshotFor(id)
	if view.In != 50 || view.Out != 90 || view.PeriodStart != input.Start || view.PeriodEndExpected != input.NextReset {
		t.Fatalf("restart: %+v", view)
	}
	rows, _ := db.TrafficHistory(ctx, id, 12)
	if len(rows) != 1 || rows[0].Start != input.Start || rows[0].Out != 70 {
		t.Fatalf("old period duplicated or usage lost: %+v", rows)
	}
	now = date(t, "2026-10-20", time.UTC)
	if err := restarted.Rollover(ctx, now); err != nil {
		t.Fatal(err)
	}
	view = restarted.SnapshotFor(id)
	if view.Used != 0 || view.PeriodStart != input.NextReset || view.PeriodEndExpected != date(t, "2026-11-19", time.UTC).Unix() {
		t.Fatalf("custom reset then 30-day cadence: %+v", view)
	}
	restarted.OnMetrics(id, sample(4, 155, 305))
	if restarted.SnapshotFor(id).Out != 15 {
		t.Fatal("rollover lost raw counter baseline")
	}
	stored, _ := db.GetServer(ctx, id)
	if stored.ExpireAt == nil || *stored.ExpireAt != expiry {
		t.Fatal("traffic rollover changed package expiry")
	}
}

func TestTrafficScheduleFailureAndHistoryOverlap(t *testing.T) {
	ctx := context.Background()
	now := date(t, "2026-09-15", time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 10, 20))
	a.OnMetrics(id, sample(2, 30, 50))
	p, _ := a.PeriodFor(id)
	input := &ScheduleInput{Start: date(t, "2026-09-14", time.UTC).Unix(), NextReset: date(t, "2026-10-14", time.UTC).Unix(), ExpectedStart: p.Start, Revision: p.CalibrationRevision}
	if _, err := db.Exec(`CREATE TRIGGER fail_schedule BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'schedule failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateServer(ctx, id, scheduleConfig(), input); err == nil {
		t.Fatal("expected failure")
	}
	stored, _ := db.GetServer(ctx, id)
	if stored.TrafficResetMode != "monthly" || a.SnapshotFor(id).PeriodStart != p.Start || a.SnapshotFor(id).Out != 30 {
		t.Fatal("failed transaction changed schedule or totals")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_schedule`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateServer(ctx, id, scheduleConfig(), input); err != nil {
		t.Fatal(err)
	}
	now = date(t, "2026-10-14", time.UTC)
	if err := a.Rollover(ctx, now); err != nil {
		t.Fatal(err)
	}
	p, _ = a.PeriodFor(id)
	input.ExpectedStart, input.Revision = p.Start, p.CalibrationRevision
	input.Start, input.NextReset = date(t, "2026-10-13", time.UTC).Unix(), date(t, "2026-11-14", time.UTC).Unix()
	if _, err := a.UpdateServer(ctx, id, scheduleConfig(), input); !errors.Is(err, store.ErrTrafficPeriodOverlap) {
		t.Fatalf("overlapping history accepted: %v", err)
	}
	rows, _ := db.TrafficHistory(ctx, id, 12)
	if len(rows) != 2 || rows[1].Out != 30 || *rows[1].End != p.Start || rows[0].Start != p.Start {
		t.Fatalf("history changed: %+v", rows)
	}
}

func TestConcurrentTrafficScheduleOnlyOneEditWins(t *testing.T) {
	now := date(t, "2026-09-15", time.UTC)
	a, _, id, _ := testAccountant(t, &now)
	p, _ := a.PeriodFor(id)
	input := &ScheduleInput{Start: now.Unix(), NextReset: now.AddDate(0, 0, 30).Unix(), ExpectedStart: p.Start, Revision: p.CalibrationRevision}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := a.UpdateServer(context.Background(), id, scheduleConfig(), input); results <- err })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrScheduleChanged) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d edits won", winners)
	}
}
