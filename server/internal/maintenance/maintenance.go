// Package maintenance runs bounded SQLite housekeeping outside request handlers.
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"vpsmon/server/internal/store"
)

type Worker struct {
	DB                *store.DB
	lastDay, lastWeek time.Time
}

func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if err := w.Tick(ctx, time.Now()); err != nil {
			slog.Warn("database maintenance", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (w *Worker) Tick(ctx context.Context, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if now.Sub(w.lastDay) >= 24*time.Hour {
		days := 365
		var configured int
		if found, err := w.DB.GetSetting(ctx, "retention.audit_days", &configured); err != nil {
			return err
		} else if found && configured >= 1 && configured <= 3650 {
			days = configured
		}
		if _, err := w.DB.ExecContext(ctx, "DELETE FROM audit_log WHERE ts<?", now.Unix()-int64(days)*86400); err != nil {
			return err
		}
		if _, err := w.DB.ExecContext(ctx, "DELETE FROM totp_used WHERE used_at<?", now.Unix()-86400); err != nil {
			return err
		}
		var busy, log, checkpointed int
		if err := w.DB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
			return err
		}
		if busy != 0 {
			return fmt.Errorf("WAL checkpoint busy; retry next hour (frames=%d, checkpointed=%d)", log, checkpointed)
		}
		w.lastDay = now
	}
	if now.Sub(w.lastWeek) >= 7*24*time.Hour {
		if _, err := w.DB.ExecContext(ctx, "PRAGMA optimize"); err != nil {
			return err
		}
		w.lastWeek = now
	}
	return nil
}
