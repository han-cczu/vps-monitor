package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/traffic"
)

func TestTrafficHistoryFlushAndListProjection(t *testing.T) {
	var accountant *traffic.Accountant
	e := newTestEnv(t, func(d *Deps) {
		accountant = traffic.New(d.DB, nil)
		if err := accountant.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		d.Traffic = accountant
	})
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())
	accountant.OnMetrics(id, &proto.Metrics{TS: 1, Net: proto.NetStat{RxTotal: 100, TxTotal: 200}})
	accountant.OnMetrics(id, &proto.Metrics{TS: 2, Net: proto.NetStat{RxTotal: 130, TxTotal: 250}})
	_, body := e.do(t, "GET", "/api/servers", token, nil)
	row := body["servers"].([]any)[0].(map[string]any)
	if row["traffic_used"] != float64(50) {
		t.Fatalf("list traffic=%v", row)
	}
	path := "/api/servers/" + strconv.FormatInt(id, 10) + "/traffic"
	req, _ := http.NewRequest("GET", e.srv.URL+path+"?months=12", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || len(rows) != 1 || rows[0]["used"] != float64(50) || rows[0]["in"] != float64(30) || rows[0]["period_end"] != nil {
		t.Fatalf("history status=%d rows=%v", resp.StatusCode, rows)
	}
	for _, query := range []string{"0", "121", "abc", "1.5"} {
		resp, _ := e.do(t, "GET", path+"?months="+query, token, nil)
		if resp.StatusCode != 400 {
			t.Fatalf("months=%s status=%d", query, resp.StatusCode)
		}
	}
	resp, _ = e.do(t, "GET", path, "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("unauthorized status=%d", resp.StatusCode)
	}
}

func TestTrafficCalibrationAPI(t *testing.T) {
	var accountant *traffic.Accountant
	e := newTestEnv(t, func(d *Deps) {
		accountant = traffic.New(d.DB, nil)
		if err := accountant.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		d.Traffic = accountant
	})
	token := e.adminToken(t)
	body := newServerBody()
	body["traffic_mode"] = "sum"
	id, _, _ := e.createServer(t, token, body)
	path := "/api/servers/" + strconv.FormatInt(id, 10) + "/traffic/calibration"
	for _, method := range []string{"GET", "POST"} {
		resp, _ := e.do(t, method, path, "", nil)
		if resp.StatusCode != 401 {
			t.Fatalf("%s unauthenticated: %d", method, resp.StatusCode)
		}
	}
	resp, preview := e.do(t, "GET", path, token, nil)
	if resp.StatusCode != 200 || preview["ready"] != false {
		t.Fatalf("preview: %v", preview)
	}
	payload := map[string]any{"period_start": preview["period_start"], "calibration_revision": preview["calibration_revision"], "mode": preview["mode"], "reset_day": preview["reset_day"], "in": 100_000_000_000, "out": 200_000_000_000}
	resp, _ = e.do(t, "POST", path, token, payload)
	if resp.StatusCode != 409 {
		t.Fatalf("no baseline: %d", resp.StatusCode)
	}
	accountant.OnMetrics(id, &proto.Metrics{TS: time.Now().Unix(), Net: proto.NetStat{RxTotal: 100, TxTotal: 200}})
	for _, field := range []string{"in", "out", "period_start", "calibration_revision"} {
		original := payload[field]
		delete(payload, field)
		resp, _ = e.do(t, "POST", path, token, payload)
		if resp.StatusCode != 400 {
			t.Fatalf("missing %s: %d", field, resp.StatusCode)
		}
		payload[field] = nil
		resp, _ = e.do(t, "POST", path, token, payload)
		if resp.StatusCode != 400 {
			t.Fatalf("null %s: %d", field, resp.StatusCode)
		}
		payload[field] = original
	}
	for _, invalid := range []any{-1, 1.5, "100", float64(1 << 53)} {
		payload["in"] = invalid
		resp, _ = e.do(t, "POST", path, token, payload)
		if resp.StatusCode != 400 {
			t.Fatalf("invalid in=%v: %d", invalid, resp.StatusCode)
		}
	}
	payload["in"] = 100_000_000_000
	resp, saved := e.do(t, "POST", path, token, payload)
	if resp.StatusCode != 200 || saved["in"] != float64(100_000_000_000) || saved["out"] != float64(200_000_000_000) || saved["used"] != float64(300_000_000_000) || saved["calibration_revision"] != float64(1) {
		t.Fatalf("save: %d %+v", resp.StatusCode, saved)
	}
	resp, _ = e.do(t, "POST", path, token, payload)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate save: %d", resp.StatusCode)
	}
	_, list := e.do(t, "GET", "/api/servers", token, nil)
	row := list["servers"].([]any)[0].(map[string]any)
	if row["traffic_used"] != float64(300_000_000_000) {
		t.Fatalf("list not calibrated: %+v", row)
	}
	logs, err := e.db.ListAudit(context.Background(), 10, 0)
	if err != nil || logs[0].Actor != "admin" || logs[0].Action != "server.traffic.calibrate" {
		t.Fatalf("authenticated audit: %+v %v", logs, err)
	}
	resp, _ = e.do(t, "GET", "/api/servers/999999/traffic/calibration", token, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown server: %d", resp.StatusCode)
	}
}
