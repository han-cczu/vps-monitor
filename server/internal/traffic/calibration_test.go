package traffic

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"vpsmon/server/internal/store"
)

func calibrationInput(t *testing.T, a *Accountant, id, inbound, outbound int64) CalibrationInput {
	t.Helper()
	s, err := a.Calibration(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return CalibrationInput{PeriodStart: s.Start, Revision: s.CalibrationRevision, In: inbound, Out: outbound, Mode: s.Mode, ResetDay: s.ResetDay}
}

func TestCalibrationDirectionsPersistenceRebootAndRollover(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	if _, err := db.Exec(`UPDATE servers SET traffic_mode='sum' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	a.OnMetrics(id, sample(1, 1000, 2000))
	a.OnMetrics(id, sample(2, 1040, 2060))
	input := calibrationInput(t, a, id, 100_000_000_000, 200_000_000_000)
	saved, err := a.Calibrate(ctx, id, input)
	if err != nil || saved.In != input.In || saved.Out != input.Out || saved.Used != 300_000_000_000 || saved.CalibrationRevision != 1 {
		t.Fatalf("replace, not add: %+v %v", saved, err)
	}
	restarted := New(db, nil)
	restarted.now = a.now
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := restarted.Calibration(ctx, id); s.Ready || s.CalibrationRevision != 1 || s.CalibratedAt != now.Unix() {
		t.Fatalf("restart: %+v", s)
	}
	restarted.OnMetrics(id, sample(3, 1050, 2080))
	if _, err := restarted.Calibrate(ctx, id, input); !errors.Is(err, ErrCalibrationChanged) {
		t.Fatalf("retry: %v", err)
	}
	restarted.OnMetrics(id, sample(4, 10, 15)) // node reboot: raw counters reset
	if s := restarted.SnapshotFor(id); s.In != input.In+20 || s.Out != input.Out+35 {
		t.Fatalf("restart/reboot baseline: %+v", s)
	}
	logs, err := db.ListAudit(ctx, 10, 0)
	if err != nil || len(logs) != 1 || logs[0].Action != "server.traffic.calibrate" || logs[0].Before == "" || logs[0].After == "" {
		t.Fatalf("audit: %+v %v", logs, err)
	}
	now = time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	if err := restarted.Rollover(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows, err := db.TrafficHistory(ctx, id, 12)
	if err != nil || len(rows) != 2 || rows[0].In != 0 || rows[0].Out != 0 || rows[0].CalibrationRevision != 0 || rows[0].CalibratedAt != 0 || rows[1].In != input.In+20 || rows[1].Out != input.Out+35 || rows[1].CalibrationRevision != 1 {
		t.Fatalf("rollover: %+v %v", rows, err)
	}
}

func TestCalibrationFailureRollsBackTotalsCountersAndAudit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 100, 200))
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	a.OnMetrics(id, sample(2, 140, 260)) // intentionally still unflushed
	input := calibrationInput(t, a, id, 300, 400)
	if _, err := db.Exec(`CREATE TRIGGER reject_calibration BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Calibrate(ctx, id, input); err == nil {
		t.Fatal("expected transaction failure")
	}
	if s := a.SnapshotFor(id); s.In != 40 || s.Out != 60 || !a.dirty {
		t.Fatalf("failed adjustment lost unflushed samples: %+v", s)
	}
	counters, periods, err := db.LoadTraffic(ctx)
	if err != nil || len(counters) != 1 || counters[0].LastRX != 100 || periods[0].In != 0 || periods[0].CalibrationRevision != 0 {
		t.Fatalf("partial commit: %+v %+v %v", counters, periods, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_calibration`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Calibrate(ctx, id, input); err != nil {
		t.Fatalf("failed adjustment cannot retry: %v", err)
	}
	a.OnMetrics(id, sample(3, 150, 280))
	if s := a.SnapshotFor(id); s.In != 310 || s.Out != 420 {
		t.Fatalf("retry baseline: %+v", s)
	}
	// A new revision can lower either direction, including explicit zero.
	if _, err := a.Calibrate(ctx, id, calibrationInput(t, a, id, 0, 15)); err != nil {
		t.Fatal(err)
	}
	if s := a.SnapshotFor(id); s.In != 0 || s.Out != 15 {
		t.Fatalf("downward calibration: %+v", s)
	}
}

func TestCalibrationRejectsStalePeriodsSamplesAndConfiguration(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	input := calibrationInput(t, a, id, 100, 200)
	if _, err := a.Calibrate(ctx, id, input); !errors.Is(err, ErrCalibrationStale) {
		t.Fatalf("no baseline: %v", err)
	}
	a.OnMetrics(id, sample(1, 1, 1))
	now = now.Add(121 * time.Second)
	if _, err := a.Calibrate(ctx, id, input); !errors.Is(err, ErrCalibrationStale) {
		t.Fatalf("offline: %v", err)
	}
	a.OnMetrics(id, sample(2, 2, 2))
	if _, err := db.Exec(`UPDATE servers SET traffic_mode='sum' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Calibrate(ctx, id, input); !errors.Is(err, ErrCalibrationChanged) {
		t.Fatalf("mode changed: %v", err)
	}
	input = calibrationInput(t, a, id, 100, 200)
	now = time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	if _, err := a.Calibrate(ctx, id, input); !errors.Is(err, ErrCalibrationChanged) {
		t.Fatalf("expired period: %v", err)
	}
	if s := a.SnapshotFor(id); s.In != 0 || s.Out != 0 {
		t.Fatalf("old form changed new period: %+v", s)
	}
	for _, amounts := range [][2]int64{{-1, 0}, {0, -1}, {MaxCalibrationBytes, 1}} {
		input.In, input.Out = amounts[0], amounts[1]
		if _, err := a.Calibrate(ctx, id, input); !errors.Is(err, ErrCalibrationInput) {
			t.Fatalf("invalid %v: %v", amounts, err)
		}
	}
}

func TestConcurrentCalibrationOnlyCommitsOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 100, 200))
	input := calibrationInput(t, a, id, 300, 400)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() { _, err := a.Calibrate(ctx, id, input); results <- err })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrCalibrationChanged) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("committed %d times", successes)
	}
	logs, _ := db.ListAudit(ctx, 20, 0)
	if len(logs) != 1 {
		t.Fatalf("audit count: %d", len(logs))
	}
	a.OnMetrics(id, sample(2, 110, 220))
	if s := a.SnapshotFor(id); s.In != 310 || s.Out != 420 {
		t.Fatalf("concurrent baseline: %+v", s)
	}
}

func TestCalibrationStoreGuardRejectsConcurrentConfigEdit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	a, db, id, _ := testAccountant(t, &now)
	a.OnMetrics(id, sample(1, 10, 20))
	if err := a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	input := calibrationInput(t, a, id, 30, 40)
	if _, err := db.Exec(`UPDATE servers SET traffic_reset_day=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	p := a.periods[id]
	p.In = 300
	err := db.SaveCalibratedTraffic(ctx, nil, []store.TrafficPeriod{p}, store.TrafficCalibrationCommit{ServerID: id, Mode: input.Mode, ResetDay: input.ResetDay})
	if !errors.Is(err, store.ErrTrafficConfigChanged) {
		t.Fatalf("guard: %v", err)
	}
	rows, _ := db.TrafficHistory(ctx, id, 1)
	if rows[0].In != 0 {
		t.Fatal("rejected config edit wrote totals")
	}
}
