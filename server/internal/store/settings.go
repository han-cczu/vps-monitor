package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// GetSetting 把 key 对应的 JSON 值反序列化到 v。key 不存在返回 (false, nil)，v 不动。
func (db *DB) GetSetting(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return true, fmt.Errorf("setting %q: %w", key, err)
	}
	return true, nil
}

// SetSetting 把 v JSON 序列化后写入（不存在插入，存在覆盖）。
func (db *DB) SetSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	_, err = db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, string(raw),
	)
	if err == nil {
		db.InvalidateSubscriptions()
	}
	return err
}
