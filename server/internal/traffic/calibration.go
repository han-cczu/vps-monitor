package traffic

import (
	"context"
	"errors"
	"strconv"
	"time"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/store"
)

var (
	ErrCalibrationChanged = errors.New("账期、计费方式或校准记录已变化，请关闭后重新打开校准窗口")
	ErrCalibrationStale   = errors.New("尚未收到近期探针采样，请等待节点恢复上报后再校准")
	ErrCalibrationInput   = errors.New("入站、出站必须是非负整数字节，且合计不能超过 9007199254740991 字节")
)

// Keep the JSON numbers exact in browser clients, including a bidirectional sum.
const MaxCalibrationBytes int64 = 1<<53 - 1

type CalibrationInput struct {
	PeriodStart int64  `json:"period_start"`
	Revision    int64  `json:"calibration_revision"`
	In          int64  `json:"in"`
	Out         int64  `json:"out"`
	Mode        string `json:"mode"`
	ResetDay    int    `json:"reset_day"`
}

type CalibrationSnapshot struct {
	store.TrafficPeriod
	Mode              string `json:"mode"`
	ResetDay          int    `json:"reset_day"`
	Limit             int64  `json:"limit"`
	Used              int64  `json:"used"`
	PeriodEndExpected int64  `json:"period_end_expected"`
	SampleReceivedAt  int64  `json:"sample_received_at"`
	Ready             bool   `json:"ready"`
}

// Refresh under the accounting lock, so a form never targets an expired period.
func (a *Accountant) calibrationServer(ctx context.Context, id int64) (store.Server, error) {
	s, err := a.db.GetServer(ctx, id)
	if err != nil {
		return store.Server{}, err
	}
	if _, ok := a.periods[id]; !ok {
		return store.Server{}, store.ErrNotFound
	}
	a.servers[id] = *s
	a.roll(id, a.now())
	return *s, nil
}

func (a *Accountant) calibrationSnapshot(id int64, s store.Server) CalibrationSnapshot {
	p := a.periods[id]
	received := a.received[id]
	var at int64
	if !received.IsZero() {
		at = received.Unix()
	}
	age := a.now().Sub(received)
	return CalibrationSnapshot{
		TrafficPeriod: p, Mode: s.TrafficMode, ResetDay: s.TrafficResetDay,
		Limit: s.TrafficLimit, Used: Used(p, s.TrafficMode),
		PeriodEndExpected: NextBoundary(time.Unix(p.Start, 0).In(a.now().Location()), s.TrafficResetDay).Unix(),
		SampleReceivedAt:  at, Ready: !received.IsZero() && age >= 0 && age <= 2*time.Minute,
	}
}

func (a *Accountant) Calibration(ctx context.Context, id int64) (CalibrationSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, err := a.calibrationServer(ctx, id)
	if err != nil {
		return CalibrationSnapshot{}, err
	}
	return a.calibrationSnapshot(id, s), nil
}

// Calibrate replaces this period's totals at the latest accepted sample. Raw
// interface counters stay untouched: only subsequent deltas are added. The
// revision rejects retries and concurrent forms even after a panel restart.
func (a *Accountant) Calibrate(ctx context.Context, id int64, input CalibrationInput) (CalibrationSnapshot, error) {
	if input.In < 0 || input.Out < 0 || input.In > MaxCalibrationBytes || input.Out > MaxCalibrationBytes-input.In {
		return CalibrationSnapshot{}, ErrCalibrationInput
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, err := a.calibrationServer(ctx, id)
	if err != nil {
		return CalibrationSnapshot{}, err
	}
	before := a.calibrationSnapshot(id, s)
	if input.PeriodStart != before.Start || input.Revision != before.CalibrationRevision || input.Mode != s.TrafficMode || input.ResetDay != s.TrafficResetDay {
		return CalibrationSnapshot{}, ErrCalibrationChanged
	}
	if !before.Ready {
		return CalibrationSnapshot{}, ErrCalibrationStale
	}
	p := before.TrafficPeriod
	p.In, p.Out = input.In, input.Out
	p.CalibrationRevision++
	p.CalibratedAt = a.now().Unix()
	a.periods[id] = p
	a.dirty = true
	after := a.calibrationSnapshot(id, s)
	entry := audit.Entry(ctx, "server.traffic.calibrate", "server", strconv.FormatInt(id, 10), before, after)
	err = a.flush(ctx, "", store.TrafficCalibrationCommit{ServerID: id, Mode: s.TrafficMode, ResetDay: s.TrafficResetDay, Audit: entry})
	if err != nil {
		// Keep all pending samples and rollover work; undo only this adjustment.
		a.periods[id] = before.TrafficPeriod
		if errors.Is(err, store.ErrTrafficConfigChanged) {
			err = ErrCalibrationChanged
		}
		return CalibrationSnapshot{}, err
	}
	a.publishThresholds(id, s, before.Used, after.Used)
	return after, nil
}
