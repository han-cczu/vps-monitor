package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// SetSettings atomically applies a partial update without replacing unrelated settings.
func (db *DB) SetSettings(ctx context.Context, values map[string]json.RawMessage) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error { return setSettingsTx(ctx, tx, values) })
}
func setSettingsTx(ctx context.Context, tx *sql.Tx, values map[string]json.RawMessage) error {
	for key, value := range values {
		if !json.Valid(value) {
			return errors.New("invalid setting JSON")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value)); err != nil {
			return err
		}
	}
	return nil
}

// SetSettingsAudited captures the actual previous database values in the same transaction.
// Missing keys are null (the default was active). Any audit failure rolls back all setting changes.
func (db *DB) SetSettingsAudited(ctx context.Context, values map[string]json.RawMessage, e AuditEntry) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		before, after := map[string]json.RawMessage{}, map[string]json.RawMessage{}
		for key, value := range values {
			var raw string
			err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if errors.Is(err, sql.ErrNoRows) {
				raw = "null"
			}
			before[key] = json.RawMessage(raw)
			after[key] = value
			if key == "sub.clash_template" {
				before[key] = json.RawMessage(`"[template]"`)
				after[key] = json.RawMessage(`"[template updated]"`)
			}
		}
		if err := setSettingsTx(ctx, tx, values); err != nil {
			return err
		}
		b, err := json.Marshal(before)
		if err != nil {
			return err
		}
		a, err := json.Marshal(after)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(ts,actor,action,target_type,target_id,"before","after",ip) VALUES(?,?,?,?,?,?,?,?)`, e.TS, e.Actor, e.Action, e.TargetType, e.TargetID, string(b), string(a), nullIfEmpty(e.IP))
		return err
	})
}
