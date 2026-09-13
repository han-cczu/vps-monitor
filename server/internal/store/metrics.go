package store

import (
	"context"
	"database/sql"
	"fmt"
)

// MetricRow 是 metrics_minute / metrics_hour 的一行。两张表结构一样，共用这一个类型。
type MetricRow struct {
	ServerID int64
	TS       int64 // 区间起点：分钟表是整分，小时表是整点

	CPUAvg   float64
	CPUMax   float64
	MemUsed  int64
	SwapUsed int64
	DiskUsed int64
	Load1    float64
	Load5    float64
	Load15   float64

	RxRateAvg int64
	RxRateMax int64
	TxRateAvg int64
	TxRateMax int64
	RxTotal   int64
	TxTotal   int64

	TCP   int
	UDP   int
	Procs int

	Samples int
}

// 两张表的表名。对外只暴露这两个常量，避免把表名当参数到处传。
const (
	TableMetricsMinute = "metrics_minute"
	TableMetricsHour   = "metrics_hour"
)

const metricColumns = `server_id, ts, cpu_avg, cpu_max, mem_used, swap_used, disk_used,
	load1, load5, load15, rx_rate_avg, rx_rate_max, tx_rate_avg, tx_rate_max,
	rx_total, tx_total, tcp, udp, procs, samples`

const metricPlaceholders = `(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// InsertMinuteRows 批量写分钟行，一个事务。
//
// 用 INSERT OR REPLACE 而不是 INSERT：进程重启或补写时可能重复写同一分钟，
// 覆盖比报错合理——后写的那份样本数更全。
func (db *DB) InsertMinuteRows(ctx context.Context, rows []MetricRow) error {
	if len(rows) == 0 {
		return nil
	}

	return db.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			"INSERT OR REPLACE INTO "+TableMetricsMinute+" ("+metricColumns+") VALUES "+metricPlaceholders)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx,
				r.ServerID, r.TS, r.CPUAvg, r.CPUMax, r.MemUsed, r.SwapUsed, r.DiskUsed,
				r.Load1, r.Load5, r.Load15, r.RxRateAvg, r.RxRateMax, r.TxRateAvg, r.TxRateMax,
				r.RxTotal, r.TxTotal, r.TCP, r.UDP, r.Procs, r.Samples,
			); err != nil {
				return fmt.Errorf("写入分钟行 server=%d ts=%d: %w", r.ServerID, r.TS, err)
			}
		}
		return nil
	})
}

// RollupHours 把 [from, to) 区间内的分钟行降采样成小时行，返回写入的行数。
//
// 平均类取分钟平均的平均（每分钟样本数接近，不做加权；缺样本的分钟本来就该算得轻一点，
// 但那点偏差远小于「节点掉线导致整分钟缺失」带来的影响，不值得为此复杂化）；
// 峰值类取分钟峰值的最大值；水位类（磁盘、累计流量、连接数、进程数）取区间内最后一行。
//
// 「取最后一行」特意用 JOIN 实现，没有依赖 SQLite「裸列跟随 min/max」的特性：
// 那条规则只在查询里**恰好只有一个** min/max 聚合时才确定，而这里同时有好几个 MAX()，
// 裸列会从哪一行取是未定义的。
func (db *DB) RollupHours(ctx context.Context, from, to int64) (int64, error) {
	const query = `
INSERT OR REPLACE INTO metrics_hour (` + metricColumns + `)
SELECT
	a.server_id, a.hour_ts,
	a.cpu_avg, a.cpu_max, a.mem_used, a.swap_used, last.disk_used,
	a.load1, a.load5, a.load15,
	a.rx_rate_avg, a.rx_rate_max, a.tx_rate_avg, a.tx_rate_max,
	last.rx_total, last.tx_total,
	last.tcp, last.udp, last.procs,
	a.samples
FROM (
	SELECT
		server_id,
		(ts / 3600) * 3600            AS hour_ts,
		AVG(cpu_avg)                  AS cpu_avg,
		MAX(cpu_max)                  AS cpu_max,
		CAST(AVG(mem_used) AS INTEGER)  AS mem_used,
		CAST(AVG(swap_used) AS INTEGER) AS swap_used,
		AVG(load1)                    AS load1,
		AVG(load5)                    AS load5,
		AVG(load15)                   AS load15,
		CAST(AVG(rx_rate_avg) AS INTEGER) AS rx_rate_avg,
		MAX(rx_rate_max)              AS rx_rate_max,
		CAST(AVG(tx_rate_avg) AS INTEGER) AS tx_rate_avg,
		MAX(tx_rate_max)              AS tx_rate_max,
		MAX(ts)                       AS last_ts,
		COUNT(*)                      AS samples
	FROM metrics_minute
	WHERE ts >= ? AND ts < ?
	GROUP BY server_id, hour_ts
) AS a
JOIN metrics_minute AS last
	ON last.server_id = a.server_id AND last.ts = a.last_ts`

	res, err := db.ExecContext(ctx, query, from, to)
	if err != nil {
		return 0, fmt.Errorf("降采样 [%d, %d): %w", from, to, err)
	}
	return res.RowsAffected()
}

// LatestHourTS 返回小时表里最大的 ts，表为空时返回 0。启动补跑用它定起点。
func (db *DB) LatestHourTS(ctx context.Context) (int64, error) {
	var ts *int64
	if err := db.QueryRowContext(ctx, "SELECT MAX(ts) FROM "+TableMetricsHour).Scan(&ts); err != nil {
		return 0, err
	}
	if ts == nil {
		return 0, nil
	}
	return *ts, nil
}

// EarliestMinuteTS 返回分钟表里最小的 ts，表为空时返回 0。
func (db *DB) EarliestMinuteTS(ctx context.Context) (int64, error) {
	var ts *int64
	if err := db.QueryRowContext(ctx, "SELECT MIN(ts) FROM "+TableMetricsMinute).Scan(&ts); err != nil {
		return 0, err
	}
	if ts == nil {
		return 0, nil
	}
	return *ts, nil
}

// DeleteMetricsBefore 删掉 table 里 ts < cutoff 的行，返回删除行数。
// table 只接受本包的两个表名常量。
func (db *DB) DeleteMetricsBefore(ctx context.Context, table string, cutoff int64) (int64, error) {
	if table != TableMetricsMinute && table != TableMetricsHour {
		return 0, fmt.Errorf("未知的指标表 %q", table)
	}

	res, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE ts < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("清理 %s: %w", table, err)
	}
	return res.RowsAffected()
}

// QueryMetrics 取一台节点在 [from, to) 内的行，按 ts 升序。
func (db *DB) QueryMetrics(ctx context.Context, table string, serverID, from, to int64) ([]MetricRow, error) {
	if table != TableMetricsMinute && table != TableMetricsHour {
		return nil, fmt.Errorf("未知的指标表 %q", table)
	}

	rows, err := db.QueryContext(ctx,
		"SELECT "+metricColumns+" FROM "+table+" WHERE server_id = ? AND ts >= ? AND ts < ? ORDER BY ts",
		serverID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MetricRow{}
	for rows.Next() {
		var r MetricRow
		if err := rows.Scan(
			&r.ServerID, &r.TS, &r.CPUAvg, &r.CPUMax, &r.MemUsed, &r.SwapUsed, &r.DiskUsed,
			&r.Load1, &r.Load5, &r.Load15, &r.RxRateAvg, &r.RxRateMax, &r.TxRateAvg, &r.TxRateMax,
			&r.RxTotal, &r.TxTotal, &r.TCP, &r.UDP, &r.Procs, &r.Samples,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
