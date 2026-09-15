package store

import (
	"context"
	"encoding/json"
	"vpsmon/proto"
)

type ProxyObservationRow struct {
	proto.ObservedInstance
	ReceivedAt int64 `json:"received_at"`
}

func (db *DB) SaveProxyObservations(ctx context.Context, id int64, items []proto.ObservedInstance, complete bool, at int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := (ProxyQueries{DB: tx}).ServerExists(ctx, id); err != nil {
		return err
	}
	if complete {
		if _, err = tx.ExecContext(ctx, `UPDATE proxy_observations SET absent=1 WHERE server_id=?`, id); err != nil {
			return err
		}
	}
	for _, item := range items {
		if item.Stale && item.ConfigReadAt == 0 {
			var previous string
			if err := tx.QueryRowContext(ctx, `SELECT snapshot_json FROM proxy_observations WHERE server_id=? AND instance_id=?`, id, item.ID).Scan(&previous); err == nil {
				var old proto.ObservedInstance
				if json.Unmarshal([]byte(previous), &old) == nil {
					item.Inbounds = old.Inbounds
					item.ConfigReadAt = old.ConfigReadAt
					item.LastSuccess = old.LastSuccess
				}
			}
		}
		b, e := json.Marshal(item)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO proxy_observations(server_id,instance_id,snapshot_json,received_at,absent) VALUES(?,?,?,?,0) ON CONFLICT(server_id,instance_id) DO UPDATE SET snapshot_json=excluded.snapshot_json,received_at=excluded.received_at,absent=0`, id, item.ID, string(b), at); err != nil {
			return err
		}
	}
	// Keep recent last-known data, with a bounded tombstone history per node.
	if _, err = tx.ExecContext(ctx, `DELETE FROM proxy_observations WHERE server_id=? AND absent=1 AND (received_at<? OR instance_id NOT IN (SELECT instance_id FROM proxy_observations WHERE server_id=? ORDER BY absent,received_at DESC,instance_id LIMIT 128))`, id, at-30*86400, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) ProxyObservations(ctx context.Context, id int64) ([]ProxyObservationRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT snapshot_json,received_at,absent FROM proxy_observations WHERE server_id=? ORDER BY absent,instance_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyObservationRow{}
	for rows.Next() {
		var raw string
		var item ProxyObservationRow
		var absent bool
		if err := rows.Scan(&raw, &item.ReceivedAt, &absent); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.ObservedInstance); err != nil {
			return nil, err
		}
		item.Absent = absent
		out = append(out, item)
	}
	return out, rows.Err()
}
