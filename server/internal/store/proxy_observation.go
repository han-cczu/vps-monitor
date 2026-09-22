package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"vpsmon/proto"
)

type ProxyObservationRow struct {
	proto.ObservedInstance
	ReceivedAt int64 `json:"received_at"`
	// AbsentAt 是面板确认这条实例从完整扫描中消失的时刻。0 表示未知：
	// 升级前标记的 absent 记录没有这个信息，页面按"未记录"展示并用最后观测时间兜底。
	AbsentAt int64 `json:"absent_at"`
}

// ProxyScanState 是节点级的扫描状态。
//
// 只说数据库里的既有事实：最近一次提交的快照是否来自完整扫描、它什么时候采的、
// 什么时候收到的、那一次共发现几个实例。离线、能力协商等运行态由调用方补充。
type ProxyScanState struct {
	Complete    bool  `json:"complete"`
	CollectedAt int64 `json:"collected_at"`
	ReceivedAt  int64 `json:"received_at"`
	Instances   int   `json:"instances"`
	// Stale 由服务层按在线状态和接收时间填充，不是数据库里的列。
	Stale bool `json:"stale"`
}

// SaveProxyObservations 落一次完整组装的快照。
//
// 完整扫描先把旧记录标记 absent（保留最后快照与"何时确认消失"），再写入本轮发现的实例；
// 不完整扫描既不标记也不删除。整个提交在一个事务里，读到的永远是整轮结果。
func (db *DB) SaveProxyObservations(ctx context.Context, id int64, items []proto.ObservedInstance, complete bool, at, collectedAt int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := (ProxyQueries{DB: tx}).ServerExists(ctx, id); err != nil {
		return err
	}
	if complete {
		// 只给本轮刚消失的记录写 absent_at，重复标记不能把"何时消失"往后推。
		if _, err = tx.ExecContext(ctx, `UPDATE proxy_observations SET absent=1,absent_at=? WHERE server_id=? AND absent=0`, at, id); err != nil {
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
		if _, err = tx.ExecContext(ctx, `INSERT INTO proxy_observations(server_id,instance_id,snapshot_json,received_at,absent,absent_at) VALUES(?,?,?,?,0,0) ON CONFLICT(server_id,instance_id) DO UPDATE SET snapshot_json=excluded.snapshot_json,received_at=excluded.received_at,absent=0,absent_at=0`, id, item.ID, string(b), at); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO proxy_observation_scans(server_id,complete,collected_at,received_at,instances) VALUES(?,?,?,?,?) ON CONFLICT(server_id) DO UPDATE SET complete=excluded.complete,collected_at=excluded.collected_at,received_at=excluded.received_at,instances=excluded.instances`, id, complete, collectedAt, at, len(items)); err != nil {
		return err
	}
	// Keep recent last-known data, with a bounded tombstone history per node.
	// 保留期按"确认消失"的时刻算；升级前留下的未知时刻（absent_at=0）用最后观测时间兜底，
	// 否则那些记录永远不会过期。
	if _, err = tx.ExecContext(ctx, `DELETE FROM proxy_observations WHERE server_id=? AND absent=1 AND ((absent_at>0 AND absent_at<?) OR (absent_at=0 AND received_at<?) OR instance_id NOT IN (SELECT instance_id FROM proxy_observations WHERE server_id=? ORDER BY absent,received_at DESC,instance_id LIMIT 128))`, id, at-30*86400, at-30*86400, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ProxyObservations 返回该节点保存的全部记录（当前实例在前、历史记录在后）和节点级扫描状态。
//
// 扫描状态只在真的提交过一轮快照后才存在；重置会把它一起删掉，返回 nil，
// 调用方据此区分「等待首个快照」与「完整扫描后确实没有实例」。
func (db *DB) ProxyObservations(ctx context.Context, id int64) ([]ProxyObservationRow, *ProxyScanState, error) {
	rows, err := db.QueryContext(ctx, `SELECT snapshot_json,received_at,absent,absent_at FROM proxy_observations WHERE server_id=? ORDER BY absent,instance_id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []ProxyObservationRow{}
	for rows.Next() {
		var raw string
		var item ProxyObservationRow
		var absent bool
		if err := rows.Scan(&raw, &item.ReceivedAt, &absent, &item.AbsentAt); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.ObservedInstance); err != nil {
			return nil, nil, err
		}
		item.Absent = absent
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var scan ProxyScanState
	err = db.QueryRowContext(ctx, `SELECT complete,collected_at,received_at,instances FROM proxy_observation_scans WHERE server_id=?`, id).Scan(&scan.Complete, &scan.CollectedAt, &scan.ReceivedAt, &scan.Instances)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return out, &scan, nil
}

// DeleteProxyObservation 删除该节点的一条观测记录，返回是否真的删掉了。
// 只按 (server_id,instance_id) 定位，别的节点和别的实例都不受影响。
func (db *DB) DeleteProxyObservation(ctx context.Context, id int64, instance string) (bool, error) {
	if err := (ProxyQueries{DB: db}).ServerExists(ctx, id); err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM proxy_observations WHERE server_id=? AND instance_id=?`, id, instance)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearProxyObservations 清空该节点保存的全部观测快照，扫描状态一并回到「等待首个快照」。
//
// 只动观测数据：探针、真实代理、配置、托管记录和账务数据都不受影响。
// 清理与在途上报之间的屏障由观测服务持有（见 proxyobserve.Service）。
func (db *DB) ClearProxyObservations(ctx context.Context, id int64) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := (ProxyQueries{DB: tx}).ServerExists(ctx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_observations WHERE server_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM proxy_observation_scans WHERE server_id=?`, id)
		return err
	})
}
