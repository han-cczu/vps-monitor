package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// SetSettings atomically applies a partial update without replacing unrelated settings.
func (db *DB) SetSettings(ctx context.Context, values map[string]json.RawMessage) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		for key, value := range values {
			if !json.Valid(value) {
				return &json.SyntaxError{}
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value)); err != nil {
				return err
			}
		}
		return nil
	})
}
