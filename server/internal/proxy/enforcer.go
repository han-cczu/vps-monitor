package proxy

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/proxy/enforce"
	"vpsmon/server/internal/store"
)

// policyEvents runs inside the same transaction as the subscriber write. Events
// are returned to the caller and only published after a successful commit.
func policyEvents(ctx context.Context, q store.ProxyQueries, before, s *store.Subscriber, now time.Time) ([]hub.Event, error) {
	d := enforce.Evaluate(*s, now, now.Location())
	s.AutoDisabled = d.AutoDisabled
	previous := "none"
	if before != nil {
		previous = before.AutoDisabled
	}
	events := []hub.Event{}
	emit := func(kind hub.EventKind, threshold int) {
		events = append(events, hub.Event{Kind: kind, TargetType: "subscriber", TargetID: s.ID, Threshold: threshold, At: now})
	}
	if previous != s.AutoDisabled {
		action := "subscriber.auto_disable"
		if s.AutoDisabled == "none" {
			action = "subscriber.auto_restore"
		}
		oldJSON, _ := json.Marshal(map[string]string{"auto_disabled": previous})
		newJSON, _ := json.Marshal(map[string]any{"auto_disabled": s.AutoDisabled, "traffic_used": s.TrafficUsed, "traffic_limit": s.TrafficLimit})
		if err := q.Audit(ctx, store.AuditEntry{TS: now.Unix(), Actor: audit.SystemActor, Action: action, TargetType: "subscriber", TargetID: strconv.FormatInt(s.ID, 10), Before: string(oldJSON), After: string(newJSON)}); err != nil {
			return nil, err
		}
		if previous != "none" {
			emit("subscriber.restored", 0)
		}
		if s.AutoDisabled == "quota" {
			emit("subscriber.quota", 100)
		}
		if s.AutoDisabled == "expired" {
			emit("subscriber.expired", 0)
		}
	}
	// Multiplication is bounded by the validated JS-safe integer traffic limit.
	if s.Enabled && s.TrafficLimit > 0 && s.TrafficUsed >= (s.TrafficLimit*4+4)/5 && !s.Warn80Sent {
		s.Warn80Sent = true
		if s.AutoDisabled != "quota" {
			emit("subscriber.quota", 80)
		}
	}
	return events, nil
}

type Enforcer struct {
	db       *store.DB
	notifier Notifier
	publish  func(hub.Event)
	now      func() time.Time
	mu       sync.Mutex
}

func NewEnforcer(db *store.DB, n Notifier, publish func(hub.Event)) *Enforcer {
	if n == nil {
		n = NoopNotifier{}
	}
	return &Enforcer{db: db, notifier: n, publish: publish, now: clock.Now}
}

// RunOnce rolls periods forward and evaluates all users under a single writer
// transaction. Per-user period_start makes startup catch-up and retries idempotent.
// Invoke after Stats.Flush so the policy sees the latest committed usage.
func (e *Enforcer) RunOnce(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	changedNodes := map[int64]bool{}
	events := []hub.Event{}
	changed := false
	err := e.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		users, err := q.Subscribers(ctx)
		if err != nil {
			return err
		}
		for _, s := range users {
			before := *s
			rolled := false
			if reset, ok := enforce.LatestReset(s.ResetDay, now, now.Location()); ok && s.PeriodStart < reset.Unix() {
				s.PeriodStart = reset.Unix()
				s.TrafficUsed = 0
				s.Warn80Sent = false
				rolled = true
				// Keep historical period rows, but reject samples queued for the old period.
				if _, err = q.DB.ExecContext(ctx, "UPDATE subscribers SET usage_epoch=usage_epoch+1 WHERE id=?", s.ID); err != nil {
					return err
				}
				oldJSON, _ := json.Marshal(map[string]int64{"period_start": before.PeriodStart, "traffic_used": before.TrafficUsed})
				newJSON, _ := json.Marshal(map[string]int64{"period_start": s.PeriodStart, "traffic_used": 0})
				if err = q.Audit(ctx, store.AuditEntry{TS: now.Unix(), Actor: audit.SystemActor, Action: "subscriber.period_reset", TargetType: "subscriber", TargetID: strconv.FormatInt(s.ID, 10), Before: string(oldJSON), After: string(newJSON)}); err != nil {
					return err
				}
			}
			pending, err := policyEvents(ctx, q, &before, s, now)
			if err != nil {
				return err
			}
			if rolled || s.AutoDisabled != before.AutoDisabled || s.Warn80Sent != before.Warn80Sent {
				if _, err = q.DB.ExecContext(ctx, `UPDATE subscribers SET period_start=?,traffic_used=?,auto_disabled=?,warn80_sent=?,updated_at=? WHERE id=?`, s.PeriodStart, s.TrafficUsed, s.AutoDisabled, s.Warn80Sent, now.Unix(), s.ID); err != nil {
					return err
				}
				changed = true
			}
			if s.AutoDisabled != before.AutoDisabled {
				for _, a := range s.AssignedInbounds {
					changedNodes[a.ServerID] = true
				}
			}
			events = append(events, pending...)
		}
		date, _ := json.Marshal(now.Format(time.DateOnly))
		_, err = q.DB.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('enforce.last_rollover_date',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(date))
		return err
	})
	if err != nil {
		return err
	}
	if changed {
		e.db.InvalidateSubscriptions()
	}
	for id := range changedNodes {
		e.notifier.NodeChanged(id, "subscriber.policy")
	}
	if e.publish != nil {
		for _, event := range events {
			e.publish(event)
		}
	}
	return nil
}
