package store

import (
	"context"
	"strings"
)

type AuditFilter struct {
	Actor, Action, TargetType string
	From, To                  int64
	Limit, Offset             int
}

func (db *DB) QueryAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, int64, error) {
	clauses, args := []string{"1=1"}, []any{}
	for _, s := range []struct{ column, value string }{{"actor", f.Actor}, {"action", f.Action}, {"target_type", f.TargetType}} {
		if s.value != "" {
			clauses = append(clauses, s.column+"=?")
			args = append(args, s.value)
		}
	}
	if f.From > 0 {
		clauses = append(clauses, "ts>=?")
		args = append(args, f.From)
	}
	if f.To > 0 {
		clauses = append(clauses, "ts<=?")
		args = append(args, f.To)
	}
	where := strings.Join(clauses, " AND ")
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM audit_log WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := db.QueryContext(ctx, `SELECT id,ts,actor,action,target_type,COALESCE(target_id,''),COALESCE("before",''),COALESCE("after",''),COALESCE(ip,'') FROM audit_log WHERE `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.Before, &e.After, &e.IP); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
