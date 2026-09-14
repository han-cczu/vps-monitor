package enforce

import (
	"testing"
	"time"
	_ "time/tzdata"
	"vpsmon/server/internal/store"
)

func TestEvaluate(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	expiry := "2026-03-08" // DST begins: a 23-hour local calendar day.
	end := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	for _, tc := range []struct {
		name        string
		enabled     bool
		previous    string
		limit, used int64
		expiry      *string
		now         time.Time
		want        string
	}{
		{"manual quota", false, "quota", 100, 0, nil, end, "quota"},
		{"manual none", false, "none", 100, 200, &expiry, end, "none"},
		{"unlimited", true, "quota", 0, 200, nil, end, "none"},
		{"equal quota", true, "none", 100, 100, nil, end, "quota"},
		{"below quota", true, "quota", 100, 99, nil, end, "none"},
		{"expiry priority", true, "quota", 100, 200, &expiry, end, "expired"},
		{"end day still valid", true, "none", 100, 99, &expiry, end.Add(-time.Nanosecond), "none"},
		{"boundary UTC", true, "none", 100, 99, &expiry, end.UTC(), "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.Subscriber{Enabled: tc.enabled, AutoDisabled: tc.previous, TrafficLimit: tc.limit, TrafficUsed: tc.used, ExpireAt: tc.expiry}
			d := Evaluate(s, tc.now, loc)
			if d.AutoDisabled != tc.want || d.Changed != (tc.want != tc.previous) {
				t.Fatalf("got %+v want %s", d, tc.want)
			}
		})
	}
}

func TestResetCalendarAndNextDate(t *testing.T) {
	loc := time.FixedZone("panel", 8*3600)
	for _, tc := range []struct {
		now          string
		day          int
		latest, next string
	}{
		{"2026-03-01", 31, "2026-02-28", "2026-03-31"},
		{"2028-02-29", 31, "2028-02-29", "2028-03-31"},
		{"2026-01-02", 15, "2025-12-15", "2026-01-15"},
		{"2026-09-15", 15, "2026-09-15", "2026-10-15"},
	} {
		now, _ := time.ParseInLocation(time.DateOnly, tc.now, loc)
		reset, ok := LatestReset(tc.day, now, loc)
		if !ok || reset.Format(time.DateOnly) != tc.latest {
			t.Fatalf("%+v reset=%v", tc, reset)
		}
		s := store.Subscriber{Enabled: true, AutoDisabled: "none", ResetDay: tc.day}
		Decorate(&s, now, loc)
		if s.Status != "active" || s.NextResetDate == nil || *s.NextResetDate != tc.next {
			t.Fatalf("%+v got %+v", tc, s)
		}
	}
	if _, ok := LatestReset(0, time.Now(), loc); ok {
		t.Fatal("one-time quota reset")
	}
}
