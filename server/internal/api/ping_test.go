package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"vpsmon/proto"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/ping"
)

func TestPingRoutesCRUDLiveDeliveryAndRecovery(t *testing.T) {
	var service *ping.Service
	var realtime *hub.Hub
	e := newTestEnv(t, func(d *Deps) {
		realtime = hub.New(d.DB, d.Tokens)
		service = ping.New(d.DB, realtime.Agents)
		if err := service.ReloadTasks(context.Background()); err != nil {
			t.Fatal(err)
		}
		realtime.Agents.OnPing(service.OnPing)
		realtime.Agents.SetConfigBuilder(func(id int64) any { return service.BuildConfig(id, hub.DefaultReportInterval) })
		realtime.Registry.SetPingSource(service.SnapshotFor)
		d.Hub = realtime
		d.Ping = service
	})
	token := e.adminToken(t)
	id, agentToken, _ := e.createServer(t, token, newServerBody())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + agentToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	readConfig := func() proto.Config {
		t.Helper()
		var cfg proto.Config
		if err := wsjson.Read(ctx, conn, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if cfg := readConfig(); len(cfg.PingTasks) != 3 || cfg.ReportInterval != 1 {
		t.Fatalf("initial=%+v", cfg)
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/ping-tasks"}, {"POST", "/api/ping-tasks"}, {"PUT", "/api/ping-tasks/1"}, {"DELETE", "/api/ping-tasks/1"}, {"GET", fmt.Sprintf("/api/servers/%d/ping/recent", id)}, {"GET", fmt.Sprintf("/api/servers/%d/ping/history?task=1", id)}} {
		resp, _ := e.do(t, route.method, route.path, "", nil)
		if resp.StatusCode != 401 {
			t.Fatalf("unprotected %s", route.path)
		}
	}
	body := map[string]any{"name": "TCP", "target": "localhost:443", "kind": "tcp", "interval_sec": 10, "server_ids": []int64{id}, "enabled": true, "sort_order": 4}
	resp, out := e.do(t, "POST", "/api/ping-tasks", token, body)
	if resp.StatusCode != 201 || out["pushed"] != float64(1) {
		t.Fatalf("create %d %v", resp.StatusCode, out)
	}
	taskID := int64(out["task"].(map[string]any)["id"].(float64))
	if cfg := readConfig(); len(cfg.PingTasks) != 4 {
		t.Fatalf("create config=%+v", cfg)
	}
	ms := 15.0
	if err := wsjson.Write(ctx, conn, proto.PingResult{Type: proto.TypePing, TaskID: taskID, LatencyMS: &ms}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		views := service.SnapshotFor(id)
		if len(views) == 4 && views[3].Latency != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ping result never reached snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	if err := service.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	resp, out = e.do(t, "GET", fmt.Sprintf("/api/servers/%d/ping/recent", id), token, nil)
	if resp.StatusCode != 200 || len(out["tasks"].([]any)) != 4 {
		t.Fatalf("recent=%v", out)
	}
	restored := ping.New(e.db, nil)
	restored.ReloadTasks(ctx)
	if err := restored.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	if view := restored.SnapshotFor(id)[3]; view.Latency == nil || *view.Latency != 15 {
		t.Fatalf("restart=%+v", view)
	}
	for name, step := range map[string]float64{"1h": 60, "24h": 60, "7d": 600, "30d": 3600} {
		resp, out = e.do(t, "GET", fmt.Sprintf("/api/servers/%d/ping/history?task=%d&range=%s", id, taskID, name), token, nil)
		if resp.StatusCode != 200 || out["step"] != step {
			t.Fatalf("history=%v", out)
		}
	}
	body["enabled"] = false
	resp, out = e.do(t, "PUT", fmt.Sprintf("/api/ping-tasks/%d", taskID), token, body)
	if resp.StatusCode != 200 {
		t.Fatalf("disable=%v", out)
	}
	if cfg := readConfig(); len(cfg.PingTasks) != 3 {
		t.Fatalf("disabled config=%+v", cfg)
	}
	resp, _ = e.do(t, "DELETE", fmt.Sprintf("/api/ping-tasks/%d", taskID), token, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("delete=%d", resp.StatusCode)
	}
	readConfig()
	var n int
	e.db.QueryRow("SELECT COUNT(*) FROM ping_results WHERE task_id=?", taskID).Scan(&n)
	if n != 0 {
		t.Fatal("results survived deletion")
	}
}
func TestPingTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		kind, target string
		valid        bool
	}{
		{"icmp", "127.0.0.1", true}, {"icmp", "::1", true}, {"icmp", "example.com", true}, {"icmp", "host:80", false}, {"icmp", "https://example.com", false},
		{"tcp", "[::1]:443", true}, {"tcp", "host:443", true}, {"tcp", "host", false}, {"tcp", "host:0", false}, {"tcp", "host:65536", false}, {"udp", "host", false},
	} {
		if got := validatePingTarget(tc.kind, tc.target) == nil; got != tc.valid {
			t.Errorf("%s %s valid=%v", tc.kind, tc.target, got)
		}
	}
}

func TestPingRequestAndQueryValidation(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())
	for _, bad := range []map[string]any{{"interval_sec": 9}, {"interval_sec": 3601}, {"server_ids": []int64{-1}}, {"server_ids": []int64{1, 1}}, {"sort_order": 1000001}} {
		body := map[string]any{"name": "valid", "target": "localhost", "kind": "icmp", "interval_sec": 60}
		for k, v := range bad {
			body[k] = v
		}
		resp, _ := e.do(t, "POST", "/api/ping-tasks", token, body)
		if resp.StatusCode != 400 {
			t.Fatalf("bad=%v status=%d", bad, resp.StatusCode)
		}
	}
	resp, _ := e.do(t, "GET", fmt.Sprintf("/api/servers/%d/ping/history?task=1&range=bad", id), token, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("invalid range status=%d", resp.StatusCode)
	}
}
