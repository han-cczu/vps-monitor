package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type AlertRule struct {
	Kind      string          `json:"kind"`
	Params    json.RawMessage `json:"params"`
	Enabled   bool            `json:"enabled"`
	UpdatedAt int64           `json:"updated_at"`
}
type NotifyChannel struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Config    json.RawMessage `json:"config"`
	Enabled   bool            `json:"enabled"`
	CreatedAt int64           `json:"created_at"`
}
type AlertEvent struct {
	ID         int64  `json:"id"`
	RuleKind   string `json:"rule_kind"`
	TargetType string `json:"target_type"`
	TargetID   int64  `json:"target_id"`
	Level      string `json:"level"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	FiredAt    int64  `json:"fired_at"`
	ResolvedAt *int64 `json:"resolved_at"`
	NotifiedAt *int64 `json:"notified_at"`
	DedupeKey  string `json:"dedupe_key"`
}
type AlertDelivery struct {
	EventID, ChannelID int64
	Recovery           bool
	Attempts           int
}

const alertEventColumns = `id,rule_kind,target_type,COALESCE(target_id,0),level,title,message,fired_at,resolved_at,notified_at,dedupe_key`

func scanAlert(row scanner) (*AlertEvent, error) {
	var e AlertEvent
	err := row.Scan(&e.ID, &e.RuleKind, &e.TargetType, &e.TargetID, &e.Level, &e.Title, &e.Message, &e.FiredAt, &e.ResolvedAt, &e.NotifiedAt, &e.DedupeKey)
	return &e, notFound(err)
}

func (db *DB) AlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := db.QueryContext(ctx, `SELECT kind,params,enabled,updated_at FROM alert_rules ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		var r AlertRule
		var raw string
		if err := rows.Scan(&r.Kind, &raw, &r.Enabled, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Params = json.RawMessage(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (db *DB) SaveAlertRules(ctx context.Context, rules []AlertRule, now int64) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, r := range rules {
			if _, err := tx.ExecContext(ctx, `UPDATE alert_rules SET params=?,enabled=?,updated_at=? WHERE kind=?`, string(r.Params), r.Enabled, now, r.Kind); err != nil {
				return err
			}
		}
		return nil
	})
}
func scanChannel(row scanner) (*NotifyChannel, error) {
	var c NotifyChannel
	var raw string
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &raw, &c.Enabled, &c.CreatedAt)
	c.Config = json.RawMessage(raw)
	return &c, notFound(err)
}
func (db *DB) NotifyChannels(ctx context.Context) ([]NotifyChannel, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,kind,config,enabled,created_at FROM notify_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotifyChannel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}
func (db *DB) NotifyChannel(ctx context.Context, id int64) (*NotifyChannel, error) {
	return scanChannel(db.QueryRowContext(ctx, `SELECT id,name,kind,config,enabled,created_at FROM notify_channels WHERE id=?`, id))
}
func (db *DB) SaveNotifyChannel(ctx context.Context, c *NotifyChannel) error {
	if c.ID == 0 {
		res, err := db.ExecContext(ctx, `INSERT INTO notify_channels(name,kind,config,enabled,created_at) VALUES(?,?,?,?,?)`, c.Name, c.Kind, string(c.Config), c.Enabled, c.CreatedAt)
		if err != nil {
			return err
		}
		c.ID, err = res.LastInsertId()
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE notify_channels SET name=?,kind=?,config=?,enabled=? WHERE id=?`, c.Name, c.Kind, string(c.Config), c.Enabled, c.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (db *DB) DeleteNotifyChannel(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM notify_channels WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// FireAlert atomically deduplicates, applies cooldown and creates channel work.
// occurrence is a period/date identity for one-shot threshold reminders.
func (db *DB) FireAlert(ctx context.Context, e *AlertEvent, occurrence string, cooldown int64) (bool, error) {
	created := false
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_events WHERE dedupe_key=? AND resolved_at IS NULL`, e.DedupeKey).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		if occurrence != "" {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_once(dedupe_key,occurrence,fired_at) VALUES(?,?,?)`, e.DedupeKey, occurrence, e.FiredAt)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return nil
			}
		}
		var previous sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(fired_at) FROM alert_events WHERE dedupe_key=?`, e.DedupeKey).Scan(&previous); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO alert_events(rule_kind,target_type,target_id,level,title,message,fired_at,resolved_at,dedupe_key) VALUES(?,?,?,?,?,?,?,?,?)`, e.RuleKind, e.TargetType, e.TargetID, e.Level, e.Title, e.Message, e.FiredAt, e.ResolvedAt, e.DedupeKey)
		if err != nil {
			return err
		}
		e.ID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		created = true
		if previous.Valid && e.FiredAt-previous.Int64 < cooldown {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_deliveries(event_id,channel_id,next_at) SELECT ?,id,? FROM notify_channels WHERE enabled=1`, e.ID, e.FiredAt)
		return err
	})
	return created, err
}

func (db *DB) AlertEvent(ctx context.Context, id int64) (*AlertEvent, error) {
	return scanAlert(db.QueryRowContext(ctx, `SELECT `+alertEventColumns+` FROM alert_events WHERE id=?`, id))
}
func (db *DB) ResolveAlert(ctx context.Context, id, now int64, recovery bool) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE alert_events SET resolved_at=? WHERE id=? AND resolved_at IS NULL`, now, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		if recovery {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_deliveries(event_id,channel_id,recovery,next_at) SELECT event_id,channel_id,1,? FROM alert_deliveries WHERE event_id=? AND recovery=0 AND sent_at IS NOT NULL`, now, id); err != nil {
				return err
			}
		}
		// Do not deliver an obsolete failure after it already recovered.
		_, err = tx.ExecContext(ctx, `DELETE FROM alert_deliveries WHERE event_id=? AND recovery=0 AND sent_at IS NULL`, id)
		return err
	})
}

type AlertFilter struct {
	Page         int
	Open         bool
	Kind, Target string
}

func (db *DB) ListAlertEvents(ctx context.Context, f AlertFilter) ([]AlertEvent, int, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Open {
		where = append(where, "resolved_at IS NULL")
	}
	if f.Kind != "" {
		where = append(where, "rule_kind=?")
		args = append(args, f.Kind)
	}
	if f.Target != "" {
		parts := strings.SplitN(f.Target, ":", 2)
		if len(parts) != 2 {
			return nil, 0, 0, fmt.Errorf("invalid target")
		}
		where = append(where, "target_type=? AND target_id=?")
		args = append(args, parts[0], parts[1])
	}
	clause := strings.Join(where, " AND ")
	var total, open int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_events WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_events WHERE resolved_at IS NULL`).Scan(&open); err != nil {
		return nil, 0, 0, err
	}
	args = append(args, 50, max(0, f.Page-1)*50)
	rows, err := db.QueryContext(ctx, `SELECT `+alertEventColumns+` FROM alert_events WHERE `+clause+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	out := []AlertEvent{}
	for rows.Next() {
		e, err := scanAlert(rows)
		if err != nil {
			return nil, 0, 0, err
		}
		out = append(out, *e)
	}
	return out, total, open, rows.Err()
}
func (db *DB) OpenAlerts(ctx context.Context) ([]AlertEvent, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+alertEventColumns+` FROM alert_events WHERE resolved_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertEvent{}
	for rows.Next() {
		e, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}
func (db *DB) DueAlertDeliveries(ctx context.Context, now int64) ([]AlertDelivery, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.event_id,d.channel_id,d.recovery,d.attempts FROM alert_deliveries d JOIN notify_channels c ON c.id=d.channel_id WHERE d.sent_at IS NULL AND d.attempts<3 AND d.next_at<=? AND c.enabled=1 ORDER BY d.event_id,d.recovery LIMIT 20`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertDelivery{}
	for rows.Next() {
		var d AlertDelivery
		if err := rows.Scan(&d.EventID, &d.ChannelID, &d.Recovery, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (db *DB) FinishAlertDelivery(ctx context.Context, d AlertDelivery, now int64, sendErr error) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		var sent any
		message := ""
		if sendErr == nil {
			sent = now
		} else {
			message = sendErr.Error()
		}
		result, err := tx.ExecContext(ctx, `UPDATE alert_deliveries SET attempts=attempts+1,sent_at=?,next_at=?,last_error=? WHERE event_id=? AND channel_id=? AND recovery=? AND attempts=? AND sent_at IS NULL`, sent, now+30, message, d.EventID, d.ChannelID, d.Recovery, d.Attempts)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			return err
		}
		if sendErr == nil && !d.Recovery {
			_, err = tx.ExecContext(ctx, `UPDATE alert_events SET notified_at=? WHERE id=? AND NOT EXISTS(SELECT 1 FROM alert_deliveries WHERE event_id=? AND recovery=0 AND sent_at IS NULL)`, now, d.EventID, d.EventID)
		}
		return err
	})
}
