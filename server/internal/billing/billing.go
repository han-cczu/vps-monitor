// Package billing advances node expiry dates and emits durable daily reminders.
package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
	"vpsmon/server/internal/traffic"
)

type Service struct {
	db         *store.DB
	bus        *hub.Bus
	invalidate func()
}

func New(db *store.DB, bus *hub.Bus, invalidate func()) *Service {
	return &Service{db: db, bus: bus, invalidate: invalidate}
}

// Renew retains the original day while catching up multiple cycles (Jan 31 ->
// Feb 28 -> Mar 31), and clamps leap day anniversaries to the month end.
func Renew(expire, today time.Time, cycle string) time.Time {
	months := map[string]int{"month": 1, "quarter": 3, "year": 12}[cycle]
	if months == 0 || !expire.Before(today) {
		return expire
	}
	original := expire
	for n := months; ; n += months {
		next := traffic.Boundary(original.Year(), original.Month()+time.Month(n), original.Day(), original.Location())
		if next.After(today) {
			return next
		}
	}
}

func (s *Service) Check(ctx context.Context, now time.Time) error {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	servers, err := s.db.ListServers(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, node := range servers {
		if node.ExpireAt == nil {
			continue
		}
		expire, err := time.ParseInLocation("2006-01-02", *node.ExpireAt, now.Location())
		if err != nil {
			continue
		}
		if node.AutoRenew && expire.Before(today) {
			next := Renew(expire, today, node.BillingCycle)
			if next.After(expire) {
				after := next.Format("2006-01-02")
				before := *node.ExpireAt
				err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
					result, err := tx.ExecContext(ctx, `UPDATE servers SET expire_at=?,updated_at=? WHERE id=? AND expire_at=? AND auto_renew=1 AND billing_cycle=?`, after, now.Unix(), node.ID, before, node.BillingCycle)
					if err != nil {
						return err
					}
					n, err := result.RowsAffected()
					if err != nil || n == 0 {
						return err
					}
					beforeJSON, _ := json.Marshal(map[string]string{"expire_at": before})
					afterJSON, _ := json.Marshal(map[string]string{"expire_at": after})
					_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(ts,actor,action,target_type,target_id,"before","after") VALUES(?,'system','server.auto_renew','server',?,?,?)`, now.Unix(), strconv.FormatInt(node.ID, 10), string(beforeJSON), string(afterJSON))
					return err
				})
				if err != nil {
					return err
				}
				expire = next
				changed = true
			}
		}
		// Date arithmetic in UTC gives calendar days even across DST transitions.
		days := int(time.Date(expire.Year(), expire.Month(), expire.Day(), 0, 0, 0, 0, time.UTC).Sub(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
		if days != 7 && days != 3 && days != 1 {
			continue
		}
		result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO billing_reminders(server_id,date,days) VALUES(?,?,?)`, node.ID, today.Format("2006-01-02"), days)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 && s.bus != nil {
			s.bus.Publish(hub.Event{Kind: hub.EventServerExpiringSoon, ServerID: node.ID, TargetType: "server", TargetID: node.ID, Threshold: days, At: now})
		}
	}
	if changed && s.invalidate != nil {
		s.invalidate()
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM billing_reminders WHERE date < ?`, today.AddDate(0, 0, -40).Format("2006-01-02"))
	return err
}

func (s *Service) Run(ctx context.Context) {
	// Startup catches up after downtime. Database keys keep reminders idempotent.
	if err := s.Check(ctx, clock.Now()); err != nil {
		slog.Error("billing check", "err", err)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	date := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := clock.Now()
			if now.Hour() == 0 && now.Minute() < 5 {
				continue
			}
			if date == now.Format("2006-01-02") {
				continue
			}
			if err := s.Check(ctx, now); err != nil {
				slog.Error("billing check", "err", err)
			} else {
				date = now.Format("2006-01-02")
			}
		}
	}
}
