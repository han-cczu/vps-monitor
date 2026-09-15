package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/traffic"
)

func TestTrafficScheduleAPI(t *testing.T) {
	var a *traffic.Accountant
	e := newTestEnv(t, func(d *Deps) {
		a = traffic.New(d.DB, nil)
		if err := a.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		d.Traffic = a
	})
	token := e.adminToken(t)
	now := clock.Now()
	today := now.Format(time.DateOnly)
	start := now.AddDate(0, 0, -2).Format(time.DateOnly)
	next := now.AddDate(0, 0, 28).Format(time.DateOnly)
	id, _, created := e.createServer(t, token, map[string]any{"name": "default schedule"})
	initial := created["server"].(map[string]any)
	if initial["traffic_reset_mode"] != "days" || initial["traffic_period_start"] != today || initial["traffic_next_reset"] != now.AddDate(0, 0, 30).Format(time.DateOnly) {
		t.Fatalf("default: %v", initial)
	}
	path := "/api/servers/" + strconv.FormatInt(id, 10)
	a.OnMetrics(id, &proto.Metrics{TS: 1, Net: proto.NetStat{RxTotal: 100, TxTotal: 200}})
	a.OnMetrics(id, &proto.Metrics{TS: 2, Net: proto.NetStat{RxTotal: 140, TxTotal: 260}})
	body := map[string]any{"name": "manual schedule", "traffic_reset_mode": "days", "traffic_period_start": start, "traffic_next_reset": next, "traffic_expected_start": initial["traffic_expected_start"], "traffic_period_revision": initial["traffic_period_revision"], "expire_at": "2028-12-31"}
	resp, out := e.do(t, "PUT", path, token, body)
	saved, _ := out["server"].(map[string]any)
	if resp.StatusCode != 200 || saved["traffic_period_start"] != start || saved["traffic_next_reset"] != next || saved["traffic_used"] != float64(60) || saved["expire_at"] != "2028-12-31" {
		t.Fatalf("manual save: %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do(t, "PUT", path, token, body)
	if resp.StatusCode != 409 {
		t.Fatalf("stale edit: %d", resp.StatusCode)
	}
	for _, invalid := range []string{"2026-02-30", "", today} {
		body["traffic_period_revision"] = saved["traffic_period_revision"]
		body["traffic_expected_start"] = saved["traffic_expected_start"]
		body["traffic_next_reset"] = invalid
		resp, _ = e.do(t, "PUT", path, token, body)
		if resp.StatusCode != 400 {
			t.Fatalf("invalid next %q: %d", invalid, resp.StatusCode)
		}
	}
	// A client unaware of schedules can edit a name without changing current dates.
	resp, out = e.do(t, "PUT", path, token, map[string]any{"name": "renamed"})
	saved = out["server"].(map[string]any)
	if resp.StatusCode != 200 || saved["traffic_reset_mode"] != "days" || saved["traffic_period_start"] != start || saved["traffic_next_reset"] != next {
		t.Fatalf("unrelated edit reset dates: %v", out)
	}
	customStart := now.AddDate(0, 0, -1).Format(time.DateOnly)
	_, _, custom := e.createServer(t, token, map[string]any{"name": "custom initial", "traffic_reset_mode": "days", "traffic_period_start": customStart})
	customRow := custom["server"].(map[string]any)
	if customRow["traffic_period_start"] != customStart || customRow["traffic_next_reset"] != now.AddDate(0, 0, 29).Format(time.DateOnly) {
		t.Fatalf("initial dates: %v", customRow)
	}
}

func TestLegacyClientChangesMonthlyResetDay(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.Traffic = traffic.New(d.DB, nil)
		if err := d.Traffic.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	token := e.adminToken(t)
	now := clock.Now()
	start := now.AddDate(0, 0, -2).Format(time.DateOnly)
	id, _, created := e.createServer(t, token, map[string]any{
		"name": "annual node", "billing_cycle": "year", "expire_at": "2027-07-15",
		"traffic_reset_day": now.Day()%31 + 1, "traffic_period_start": start,
		"traffic_next_reset": now.AddDate(0, 0, 10).Format(time.DateOnly),
	})
	initial := created["server"].(map[string]any)
	// The old form submits only a monthly day, with no schedule dates or revision.
	resp, out := e.do(t, "PUT", "/api/servers/"+strconv.FormatInt(id, 10), token, map[string]any{
		"name": "annual node", "billing_cycle": "year", "expire_at": "2027-07-15", "traffic_reset_day": now.Day(),
	})
	saved, _ := out["server"].(map[string]any)
	wantNext := traffic.NextBoundary(now, now.Day()).Format(time.DateOnly)
	if resp.StatusCode != 200 || saved["traffic_next_reset"] != wantNext || saved["traffic_period_start"] != start || saved["expire_at"] != "2027-07-15" || saved["traffic_reset_mode"] != "monthly" {
		t.Fatalf("legacy reset-day edit: %d %v (initial %v)", resp.StatusCode, out, initial)
	}
}
