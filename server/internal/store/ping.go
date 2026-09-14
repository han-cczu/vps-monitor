package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ----------------------------------------------------------------------

// PingTask 是 ping_tasks 表的一行：面板里配的一个探测目标。
type PingTask struct {
	ID          int64
	Name        string
	Target      string // icmp: IP 或域名；tcp: host:port
	Kind        string // icmp | tcp
	IntervalSec int
	// ServerIDs 为 nil 表示作用于全部节点；空切片表示不作用于任何节点。
	ServerIDs []int64
	Enabled   bool
	SortOrder int64
	CreatedAt int64
	UpdatedAt int64
}

// AppliesTo 判断这个任务是否作用于某台节点。
func (t *PingTask) AppliesTo(serverID int64) bool {
	if !t.Enabled {
		return false
	}
	if t.ServerIDs == nil {
		return true
	}
	for _, id := range t.ServerIDs {
		if id == serverID {
			return true
		}
	}
	return false
}

// PingTaskInput 是创建 / 更新时的可写字段。
type PingTaskInput struct {
	Name        string
	Target      string
	Kind        string
	IntervalSec int
	ServerIDs   []int64
	Enabled     bool
	SortOrder   int64
}

const pingTaskColumns = `id, name, target, kind, interval_sec, server_ids, enabled, sort_order, created_at, updated_at`

func scanPingTask(row scanner) (*PingTask, error) {
	var (
		t         PingTask
		serverIDs sql.NullString
		enabled   int64
	)
	err := row.Scan(&t.ID, &t.Name, &t.Target, &t.Kind, &t.IntervalSec,
		&serverIDs, &enabled, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	t.Enabled = enabled != 0
	if serverIDs.Valid {
		// 解不出来当成「全部节点」会更危险（可能把任务推给不该收的节点），
		// 所以解析失败按「不作用于任何节点」处理。
		var ids []int64
		if err := json.Unmarshal([]byte(serverIDs.String), &ids); err != nil {
			ids = []int64{}
		}
		t.ServerIDs = ids
	}
	return &t, nil
}

// ListPingTasks 返回全部任务，按 sort_order、id 升序。
func (db *DB) ListPingTasks(ctx context.Context) ([]PingTask, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT "+pingTaskColumns+" FROM ping_tasks ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PingTask{}
	for rows.Next() {
		t, err := scanPingTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetPingTask 按 ID 取任务。
func (db *DB) GetPingTask(ctx context.Context, id int64) (*PingTask, error) {
	return scanPingTask(db.QueryRowContext(ctx,
		"SELECT "+pingTaskColumns+" FROM ping_tasks WHERE id = ?", id))
}

// CreatePingTask 新建任务。
func (db *DB) CreatePingTask(ctx context.Context, in PingTaskInput) (*PingTask, error) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx,
		`INSERT INTO ping_tasks (name, target, kind, interval_sec, server_ids, enabled, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.Target, in.Kind, in.IntervalSec, serverIDsArg(in.ServerIDs),
		boolToInt(in.Enabled), in.SortOrder, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return db.GetPingTask(ctx, id)
}

// UpdatePingTask 覆盖可写字段。
func (db *DB) UpdatePingTask(ctx context.Context, id int64, in PingTaskInput) (*PingTask, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE ping_tasks SET name = ?, target = ?, kind = ?, interval_sec = ?,
			server_ids = ?, enabled = ?, sort_order = ?, updated_at = ?
		 WHERE id = ?`,
		in.Name, in.Target, in.Kind, in.IntervalSec, serverIDsArg(in.ServerIDs),
		boolToInt(in.Enabled), in.SortOrder, time.Now().Unix(), id)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, ErrNotFound
	}
	return db.GetPingTask(ctx, id)
}

// DeletePingTask 删除任务，它的结果靠外键级联删除。
func (db *DB) DeletePingTask(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, "DELETE FROM ping_tasks WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// serverIDsArg 把作用范围转成库里的值：nil → NULL（全部节点）。
func serverIDsArg(ids []int64) any {
	if ids == nil {
		return nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// ----------------------------------------------------------------------

// PingResult 是一次探测的结果。LatencyMS 为 nil 表示丢包。
type PingResult struct {
	ServerID  int64
	TaskID    int64
	TS        int64
	LatencyMS *float64
}

// InsertPingResults 批量写结果，一个事务。
//
// 同一秒同一任务的重复上报覆盖；已删除节点/任务的在途结果跳过，不影响同批其他结果。
// 用 INSERT OR REPLACE：
// 主键已经保证了不会堆重复行。
func (db *DB) InsertPingResults(ctx context.Context, results []PingResult) error {
	if len(results) == 0 {
		return nil
	}

	return db.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT OR REPLACE INTO ping_results (server_id, task_id, ts, latency_ms)
        SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM servers WHERE id = ?)
        AND EXISTS (SELECT 1 FROM ping_tasks WHERE id = ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, r := range results {
			var latency any
			if r.LatencyMS != nil {
				latency = *r.LatencyMS
			}
			if _, err := stmt.ExecContext(ctx, r.ServerID, r.TaskID, r.TS, latency, r.ServerID, r.TaskID); err != nil {
				return fmt.Errorf("写入 ping 结果 server=%d task=%d: %w", r.ServerID, r.TaskID, err)
			}
		}
		return nil
	})
}

// RecentPingResults 取一台节点每个任务最近 n 次结果，按时间升序。
//
// 面板重启后内存环是空的，用它回填。
func (db *DB) RecentPingResults(ctx context.Context, serverID int64, taskID int64, n int) ([]PingResult, error) {
	// 先按时间倒序取 n 条，再在外层翻回升序——直接升序 + LIMIT 会取到最老的 n 条。
	rows, err := db.QueryContext(ctx, `
SELECT server_id, task_id, ts, latency_ms FROM (
	SELECT server_id, task_id, ts, latency_ms
	FROM ping_results
	WHERE server_id = ? AND task_id = ?
	ORDER BY ts DESC
	LIMIT ?
) ORDER BY ts`, serverID, taskID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PingResult{}
	for rows.Next() {
		var (
			r       PingResult
			latency sql.NullFloat64
		)
		if err := rows.Scan(&r.ServerID, &r.TaskID, &r.TS, &latency); err != nil {
			return nil, err
		}
		if latency.Valid {
			v := latency.Float64
			r.LatencyMS = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PingBucket 是延迟曲线上的一个分桶点。
type PingBucket struct {
	TS      int64
	Avg     *float64 // 桶内成功探测的平均延迟；全丢包时为 nil
	Max     *float64
	LossPct float64 // 0–100
	Samples int
}

// PingHistory 按 bucket 秒分桶统计一段时间的结果。
//
// AVG / MAX 会自动跳过 NULL（丢包），所以它们是「成功探测的平均 / 最大延迟」；
// 丢包率单独用 CASE 数出来。全桶都丢包时 avg / max 是 NULL，前端按断点处理。
func (db *DB) PingHistory(ctx context.Context, serverID, taskID, from, to int64, bucket int64) ([]PingBucket, error) {
	if bucket < 1 {
		return nil, fmt.Errorf("分桶大小必须大于 0，得到 %d", bucket)
	}

	rows, err := db.QueryContext(ctx, `
SELECT (ts / ?) * ? AS bts,
       AVG(latency_ms),
       MAX(latency_ms),
       SUM(CASE WHEN latency_ms IS NULL THEN 1 ELSE 0 END) * 100.0 / COUNT(*),
       COUNT(*)
FROM ping_results
WHERE server_id = ? AND task_id = ? AND ts >= ? AND ts < ?
GROUP BY bts
ORDER BY bts`, bucket, bucket, serverID, taskID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PingBucket{}
	for rows.Next() {
		var (
			b        PingBucket
			avg, max sql.NullFloat64
		)
		if err := rows.Scan(&b.TS, &avg, &max, &b.LossPct, &b.Samples); err != nil {
			return nil, err
		}
		if avg.Valid {
			v := avg.Float64
			b.Avg = &v
		}
		if max.Valid {
			v := max.Float64
			b.Max = &v
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeletePingResultsBefore 删掉 ts < cutoff 的结果，返回删除行数。
func (db *DB) DeletePingResultsBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := db.ExecContext(ctx, "DELETE FROM ping_results WHERE ts < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("清理 ping 结果: %w", err)
	}
	return res.RowsAffected()
}
