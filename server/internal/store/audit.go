package store

import (
	"context"
)

// AuditEntry 是 audit_log 表的一行。TargetID / Before / After / IP 为空串时按 NULL 存。
type AuditEntry struct {
	ID         int64
	TS         int64 // Unix 秒
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Before     string // JSON
	After      string // JSON
	IP         string
}

// InsertAudit 追加一条审计记录，返回新行 ID。
func (db *DB) InsertAudit(ctx context.Context, e AuditEntry) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO audit_log (ts, actor, action, target_type, target_id, "before", "after", ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TS, e.Actor, e.Action, e.TargetType,
		nullIfEmpty(e.TargetID), nullIfEmpty(e.Before), nullIfEmpty(e.After), nullIfEmpty(e.IP),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAudit 按时间倒序分页返回审计记录。
func (db *DB) ListAudit(ctx context.Context, limit, offset int) ([]AuditEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, ts, actor, action, target_type,
		        COALESCE(target_id, ''), COALESCE("before", ''), COALESCE("after", ''), COALESCE(ip, '')
		 FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.Before, &e.After, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
