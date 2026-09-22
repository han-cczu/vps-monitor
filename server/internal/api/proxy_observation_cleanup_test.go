package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/proxyobserve"
)

// 观测记录的删除与重置：只动面板保存的观测数据，且必须按节点和实例定位。
func TestProxyObservationDeleteAndResetHTTP(t *testing.T) {
	var realtime *hub.Hub
	e := newTestEnv(t, func(d *Deps) {
		realtime = hub.New(d.DB, d.Tokens)
		d.Hub = realtime
		d.ProxyObserve = proxyobserve.New(d.DB)
		d.ProxyObserve.Session = realtime.Agents.ProxySession
		d.ProxyObserve.Supersede = realtime.Agents.RotateProxySession
	})
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())
	other, _, _ := e.createServer(t, token, map[string]any{"name": "hk-02"})
	base := fmt.Sprintf("/api/servers/%d/proxy-observations", id)
	otherBase := fmt.Sprintf("/api/servers/%d/proxy-observations", other)

	// 面板自己造两条记录：a 仍是当前实例，b 已确认消失。
	for _, row := range []struct {
		instance string
		absent   bool
	}{{"a", false}, {"b", true}} {
		if _, err := e.db.ExecContext(context.Background(),
			`INSERT INTO proxy_observations(server_id,instance_id,snapshot_json,received_at,absent,absent_at) VALUES(?,?,?,?,?,?)`,
			id, row.instance, `{"id":"`+row.instance+`","core":"xray","ownership":"external","source":"generic","stats_status":"not_configured","inbounds":[]}`,
			time.Now().Unix(), row.absent, 0); err != nil {
			t.Fatal(err)
		}
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodDelete, base + "/a"},
		{http.MethodPost, base + "/reset"},
	} {
		res, _ := e.do(t, route.method, route.path, "", nil)
		if res.StatusCode != 401 {
			t.Fatalf("%s %s without admin token: %d", route.method, route.path, res.StatusCode)
		}
	}
	res, out := e.do(t, http.MethodDelete, base+"/missing", token, nil)
	if res.StatusCode != 404 {
		t.Fatalf("deleting an unknown record: %d %v", res.StatusCode, out)
	}
	res, out = e.do(t, http.MethodDelete, base+"/bad id", token, nil)
	if res.StatusCode != 400 || out["message"] != "代理实例标识不合法" {
		t.Fatalf("illegal instance id accepted: %d %v", res.StatusCode, out)
	}
	res, out = e.do(t, http.MethodDelete, otherBase+"/a", token, nil)
	if res.StatusCode != 404 {
		t.Fatalf("delete crossed node boundary: %d %v", res.StatusCode, out)
	}

	res, out = e.do(t, http.MethodDelete, base+"/b", token, nil)
	if res.StatusCode != 200 || out["deleted"] != true {
		t.Fatalf("delete failed: %d %v", res.StatusCode, out)
	}
	res, out = e.do(t, http.MethodGet, base, token, nil)
	if res.StatusCode != 200 {
		t.Fatalf("list failed: %d %v", res.StatusCode, out)
	}
	rows, _ := out["instances"].([]any)
	if len(rows) != 1 {
		t.Fatalf("delete left the wrong records: %v", rows)
	}
	if row, _ := rows[0].(map[string]any); row["id"] != "a" {
		t.Fatalf("delete removed the wrong record: %v", row)
	}

	// 离线节点也允许清理；重置后列表为空、扫描状态回到「等待首个快照」。
	res, out = e.do(t, http.MethodPost, base+"/reset", token, nil)
	if res.StatusCode != 200 || out["cleared"] != true || out["requested"] != false {
		t.Fatalf("offline reset reported wrong result: %d %v", res.StatusCode, out)
	}
	res, out = e.do(t, http.MethodGet, base, token, nil)
	rows, _ = out["instances"].([]any)
	if len(rows) != 0 || out["scan"] != nil {
		t.Fatalf("reset left observations behind: %d %v", res.StatusCode, out)
	}
	res, _ = e.do(t, http.MethodPost, base+"/reset", token, map[string]any{"file": "/etc/shadow"})
	if res.StatusCode != 400 {
		t.Fatal("reset accepted an arbitrary argument")
	}
	// 审计记录必须留下，且只针对本节点。
	var actions []string
	rowsAudit, err := e.db.QueryContext(context.Background(),
		`SELECT action FROM audit_log WHERE action LIKE 'proxy_observation.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsAudit.Close()
	for rowsAudit.Next() {
		var action string
		if err := rowsAudit.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if len(actions) != 2 || actions[0] != "proxy_observation.delete" || actions[1] != "proxy_observation.reset" {
		t.Fatalf("unexpected audit trail: %v", actions)
	}
}

// 观测记录的增删不得触碰真实代理、托管配置、订阅账务这些业务表。
func TestProxyObservationCleanupLeavesBusinessDataUntouched(t *testing.T) {
	var realtime *hub.Hub
	e := newTestEnv(t, func(d *Deps) {
		realtime = hub.New(d.DB, d.Tokens)
		d.Hub = realtime
		d.ProxyObserve = proxyobserve.New(d.DB)
		d.ProxyObserve.Session = realtime.Agents.ProxySession
		d.ProxyObserve.Supersede = realtime.Agents.RotateProxySession
	})
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())
	base := fmt.Sprintf("/api/servers/%d", id)
	res, out := e.do(t, http.MethodPost, base+"/inbounds", token, map[string]any{"protocol": "vless", "listen_port": 12345})
	if res.StatusCode != 201 {
		t.Fatalf("inbound setup: %d %v", res.StatusCode, out)
	}
	if _, err := e.db.ExecContext(context.Background(),
		`INSERT INTO proxy_observations(server_id,instance_id,snapshot_json,received_at,absent,absent_at) VALUES(?,?,?,?,0,0)`,
		id, "gone", `{"id":"gone","core":"xray","ownership":"external","source":"generic","stats_status":"not_configured","inbounds":[]}`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	res, out = e.do(t, http.MethodPost, base+"/proxy-observations/reset", token, nil)
	if res.StatusCode != 200 {
		t.Fatalf("reset failed: %d %v", res.StatusCode, out)
	}
	var servers, inbounds, observations int
	for table, count := range map[string]*int{"servers": &servers, "inbounds": &inbounds, "proxy_observations": &observations} {
		if err := e.db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(count); err != nil {
			t.Fatal(err)
		}
	}
	if servers != 1 || inbounds != 1 || observations != 0 {
		t.Fatalf("cleanup changed business data: servers=%d inbounds=%d observations=%d", servers, inbounds, observations)
	}
	res, out = e.do(t, http.MethodGet, base+"/core", token, nil)
	if res.StatusCode != 200 {
		t.Fatalf("core view broken after cleanup: %d %v", res.StatusCode, out)
	}
}
