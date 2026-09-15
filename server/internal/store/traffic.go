package store

import (
	"context"
	"database/sql"
	"errors"
)

type TrafficCounter struct{ ServerID, LastRX, LastTX, LastTS int64 }
type TrafficPeriod struct {
	ServerID            int64  `json:"-"`
	Start               int64  `json:"period_start"`
	End                 *int64 `json:"period_end"`
	In                  int64  `json:"in"`
	Out                 int64  `json:"out"`
	CalibrationRevision int64  `json:"calibration_revision"`
	CalibratedAt        int64  `json:"calibrated_at"`
}

var ErrTrafficConfigChanged = errors.New("traffic configuration changed")

// TrafficCalibrationCommit guards the billing configuration and records the
// adjustment in the same transaction as the totals and raw counter baselines.
type TrafficCalibrationCommit struct {
	ServerID int64
	Mode     string
	ResetDay int
	Audit    AuditEntry
}

func (db *DB) LoadTraffic(ctx context.Context) ([]TrafficCounter, []TrafficPeriod, error) {
	rows, err := db.QueryContext(ctx, `SELECT server_id,last_rx,last_tx,last_ts FROM traffic_counters`)
	if err != nil {
		return nil, nil, err
	}
	var counters []TrafficCounter
	for rows.Next() {
		var c TrafficCounter
		if err = rows.Scan(&c.ServerID, &c.LastRX, &c.LastTX, &c.LastTS); err != nil {
			rows.Close()
			return nil, nil, err
		}
		counters = append(counters, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT server_id,period_start,period_end,in_bytes,out_bytes,calibration_revision,calibrated_at FROM traffic_periods WHERE period_end IS NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var periods []TrafficPeriod
	for rows.Next() {
		var p TrafficPeriod
		if err = rows.Scan(&p.ServerID, &p.Start, &p.End, &p.In, &p.Out, &p.CalibrationRevision, &p.CalibratedAt); err != nil {
			return nil, nil, err
		}
		periods = append(periods, p)
	}
	return counters, periods, rows.Err()
}

// Counter baselines and period totals must be committed together: replay after a crash
// then adds the delta since this transaction exactly once.
func (db *DB) SaveTraffic(ctx context.Context, counters []TrafficCounter, periods []TrafficPeriod, rolloverDate string) error {
	return db.saveTraffic(ctx, counters, periods, rolloverDate, nil)
}

func (db *DB) SaveCalibratedTraffic(ctx context.Context, counters []TrafficCounter, periods []TrafficPeriod, commit TrafficCalibrationCommit) error {
	return db.saveTraffic(ctx, counters, periods, "", &commit)
}

func (db *DB) saveTraffic(ctx context.Context, counters []TrafficCounter, periods []TrafficPeriod, rolloverDate string, calibration *TrafficCalibrationCommit) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		if calibration != nil {
			// Take the write lock before any reads. A concurrent edit or deletion
			// cannot slip between this guard and the traffic/audit writes.
			res, err := tx.ExecContext(ctx, `UPDATE servers SET id=id WHERE id=? AND traffic_mode=? AND traffic_reset_day=?`, calibration.ServerID, calibration.Mode, calibration.ResetDay)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrTrafficConfigChanged
			}
		}
		for _, p := range periods {
			_, err := tx.ExecContext(ctx, `INSERT INTO traffic_periods(server_id,period_start,period_end,in_bytes,out_bytes,calibration_revision,calibrated_at)
     SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM servers WHERE id=?)
     ON CONFLICT(server_id,period_start) DO UPDATE SET period_end=excluded.period_end,in_bytes=excluded.in_bytes,out_bytes=excluded.out_bytes,calibration_revision=excluded.calibration_revision,calibrated_at=excluded.calibrated_at`, p.ServerID, p.Start, p.End, p.In, p.Out, p.CalibrationRevision, p.CalibratedAt, p.ServerID)
			if err != nil {
				return err
			}
		}
		for _, c := range counters {
			_, err := tx.ExecContext(ctx, `INSERT INTO traffic_counters(server_id,last_rx,last_tx,last_ts)
    SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM servers WHERE id=?)
    ON CONFLICT(server_id) DO UPDATE SET last_rx=excluded.last_rx,last_tx=excluded.last_tx,last_ts=excluded.last_ts`, c.ServerID, c.LastRX, c.LastTX, c.LastTS, c.ServerID)
			if err != nil {
				return err
			}
		}
		if rolloverDate != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('traffic.last_rollover_date',json_quote(?)) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, rolloverDate)
			return err
		}
		if calibration != nil {
			_, err := insertAudit(ctx, tx, calibration.Audit)
			return err
		}
		return nil
	})
}

func (db *DB) TrafficHistory(ctx context.Context, id int64, months int) ([]TrafficPeriod, error) {
	rows, err := db.QueryContext(ctx, `SELECT server_id,period_start,period_end,in_bytes,out_bytes,calibration_revision,calibrated_at FROM traffic_periods WHERE server_id=? ORDER BY period_start DESC LIMIT ?`, id, months)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficPeriod{}
	for rows.Next() {
		var p TrafficPeriod
		if err := rows.Scan(&p.ServerID, &p.Start, &p.End, &p.In, &p.Out, &p.CalibrationRevision, &p.CalibratedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
