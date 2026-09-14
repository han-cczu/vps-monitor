// Package enforce contains the calendar and policy rules for subscriber limits.
package enforce

import (
	"time"

	"vpsmon/server/internal/store"
)

type Decision struct {
	AutoDisabled string
	Changed      bool
	Reason       string
}

// Evaluate preserves the administrator's disabled state. Expiration is the end
// of the selected local calendar day, including days with a DST transition.
func Evaluate(s store.Subscriber, now time.Time, loc *time.Location) Decision {
	next := s.AutoDisabled
	if s.Enabled {
		next = "none"
		if s.ExpireAt != nil {
			if day, err := time.ParseInLocation(time.DateOnly, *s.ExpireAt, loc); err == nil && !now.Before(day.AddDate(0, 0, 1)) {
				next = "expired"
			}
		}
		if next == "none" && s.TrafficLimit > 0 && s.TrafficUsed >= s.TrafficLimit {
			next = "quota"
		}
	}
	return Decision{AutoDisabled: next, Changed: next != s.AutoDisabled, Reason: next}
}

func resetInMonth(year int, month time.Month, day int, loc *time.Location) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	return time.Date(year, month, min(day, last), 0, 0, 0, 0, loc)
}

// LatestReset returns the most recent scheduled reset, with month-end clamping.
// It catches up arbitrarily long downtime without iterating over missed days.
func LatestReset(day int, now time.Time, loc *time.Location) (time.Time, bool) {
	if day < 1 || day > 31 {
		return time.Time{}, false
	}
	now = now.In(loc)
	reset := resetInMonth(now.Year(), now.Month(), day, loc)
	if reset.After(now) {
		reset = resetInMonth(now.Year(), now.Month()-1, day, loc)
	}
	return reset, true
}

func Decorate(s *store.Subscriber, now time.Time, loc *time.Location) {
	if s == nil {
		return
	}
	s.Status = "active"
	if !s.Enabled {
		s.Status = "disabled"
	} else if s.AutoDisabled != "none" {
		s.Status = s.AutoDisabled
	}
	s.NextResetDate = nil
	if s.ResetDay > 0 && s.ResetDay <= 31 {
		now = now.In(loc)
		next := resetInMonth(now.Year(), now.Month(), s.ResetDay, loc)
		if !next.After(now) {
			next = resetInMonth(now.Year(), now.Month()+1, s.ResetDay, loc)
		}
		date := next.Format(time.DateOnly)
		s.NextResetDate = &date
	}
}
