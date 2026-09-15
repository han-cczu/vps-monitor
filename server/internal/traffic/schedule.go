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
	ErrScheduleChanged = errors.New("流量周期或校准记录已变化，请重新打开编辑窗口")
	ErrScheduleDates   = errors.New("本期开始日期不能晚于今天，下次重置日期必须晚于今天及本期开始日期")
)

// A manually supplied next reset applies to this period only. Later periods
// follow the selected rule, in the panel timezone (including DST boundaries).
func NextResetDate(start time.Time, mode string, day int) time.Time {
	if mode == "days" {
		return start.AddDate(0, 0, 30)
	}
	return NextBoundary(start, day)
}

// Automatic suggestions must remain in the future when an existing period's
// rule changes. Advancing the date does not reset counters or move its start.
func UpcomingResetDate(start, now time.Time, mode string, day int) time.Time {
	next := NextResetDate(start, mode, day)
	for !next.After(now) {
		next = NextResetDate(next, mode, day)
	}
	return next
}

func NewPeriod(now time.Time, mode string, day int) store.TrafficPeriod {
	start := PeriodStart(now, day)
	if mode == "days" {
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
	return store.TrafficPeriod{Start: start.Unix(), NextReset: NextResetDate(start, mode, day).Unix()}
}

func periodBoundary(p store.TrafficPeriod, s store.Server, loc *time.Location) time.Time {
	if p.NextReset > p.Start {
		return time.Unix(p.NextReset, 0).In(loc)
	}
	return NextResetDate(time.Unix(p.Start, 0).In(loc), s.TrafficResetMode, s.TrafficResetDay)
}

type ScheduleInput struct {
	Start         int64
	NextReset     int64
	ExpectedStart int64
	Revision      int64
}

func ValidatePeriod(start, nextReset int64, now time.Time) error {
	if start <= 0 || start > now.Unix() || nextReset <= now.Unix() || nextReset <= start {
		return ErrScheduleDates
	}
	return nil
}

func (a *Accountant) PeriodFor(id int64) (store.TrafficPeriod, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.servers[id]
	if !ok {
		return store.TrafficPeriod{}, false
	}
	a.roll(id, a.now())
	p := a.periods[id]
	p.NextReset = periodBoundary(p, s, a.now().Location()).Unix()
	return p, true
}

// Serialize edits with incoming samples, rollover and calibration. Persist the
// latest counters first, then atomically change the configuration and period key.
// Correcting dates carries all existing totals forward; it does not invent usage
// for unobserved dates or rewrite closed history.
func (a *Accountant) UpdateServer(ctx context.Context, id int64, in store.ServerInput, input *ScheduleInput) (*store.Server, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	beforeServer, err := a.calibrationServer(ctx, id)
	if err != nil {
		return nil, err
	}
	before := a.periods[id]
	ruleChanged := beforeServer.TrafficResetMode != in.TrafficResetMode || beforeServer.TrafficResetDay != in.TrafficResetDay
	after := before
	if input != nil {
		if before.Start != input.ExpectedStart || before.CalibrationRevision != input.Revision {
			return nil, ErrScheduleChanged
		}
		after.Start, after.NextReset = input.Start, input.NextReset
	} else if ruleChanged {
		now := a.now()
		after.NextReset = UpcomingResetDate(time.Unix(before.Start, 0).In(now.Location()), now, in.TrafficResetMode, in.TrafficResetDay).Unix()
	}
	changed := ruleChanged || after.Start != before.Start || after.NextReset != before.NextReset
	if changed {
		if err := ValidatePeriod(after.Start, after.NextReset, a.now()); err != nil {
			return nil, err
		}
		after.CalibrationRevision++ // also invalidates open usage-calibration forms
	}
	if err := a.flush(ctx, ""); err != nil {
		return nil, err
	}
	var changes []store.TrafficPeriodChange
	if changed {
		entry := audit.Entry(ctx, "server.traffic.schedule", "server", strconv.FormatInt(id, 10), before, after)
		changes = append(changes, store.TrafficPeriodChange{Before: before, After: after, Audit: entry})
	}
	updated, err := a.db.UpdateServer(ctx, id, in, changes...)
	if errors.Is(err, store.ErrTrafficConfigChanged) {
		return nil, ErrScheduleChanged
	}
	if err != nil {
		return nil, err
	}
	a.servers[id] = *updated
	a.periods[id] = after
	return updated, nil
}
