package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

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
