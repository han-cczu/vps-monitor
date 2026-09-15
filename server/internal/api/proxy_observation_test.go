package api

import (
	"context"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"strings"
	"testing"
	"time"
	"vpsmon/proto"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/proxy"
	"vpsmon/server/internal/proxyobserve"
)

func TestExternalNodeReadOnlyHTTPAndRefresh(t *testing.T) {
	var realtime *hub.Hub
	e := newTestEnv(t, func(d *Deps) {
		realtime = hub.New(d.DB, d.Tokens)
		d.Hub = realtime
		d.ProxyObserve = proxyobserve.New(d.DB)
		r := proxy.NewReconciler(d.DB, proxy.ReconcilerOptions{Agents: realtime.Agents, AllowManage: realtime.Agents.CanManageProxy, Debounce: time.Hour})
		t.Cleanup(r.Close)
		d.Reconciler = r
		d.Proxy = proxy.New(d.DB, r)
		d.Proxy.AllowManage = func(id int64) bool { return realtime.Agents.CanManageProxy(id, true) }
		realtime.Agents.OnCore(r.Handle, r.OnAgentHello)
	})
	token := e.adminToken(t)
	id, agentToken, _ := e.createServer(t, token, newServerBody())
	base := fmt.Sprintf("/api/servers/%d", id)
	for _, method := range []string{"GET", "POST"} {
		route := base + "/proxy-observations"
		if method == "POST" {
			route += "/refresh"
		}
		res, _ := e.do(t, method, route, "", nil)
		if res.StatusCode != 401 {
			t.Fatal("missing observation auth")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + agentToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var c proto.Config
	if err := wsjson.Read(ctx, conn, &c); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, proto.Hello{Type: proto.TypeHello, ProtoVersion: proto.Version, Capabilities: []string{proto.ProxyObserveCapability}, ProxyManagement: "external"}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Read(ctx, conn, &c); err != nil {
		t.Fatal(err)
	}
	if c.ProxyObserveSession == "" {
		t.Fatal("missing session")
	}
	for _, route := range []struct {
		method, path string
		body         any
	}{{"POST", "/core/install", nil}, {"POST", "/core/restart", nil}, {"POST", "/core/apply", nil}, {"POST", "/core/rollback/1", nil}, {"GET", "/core/logs", nil}, {"POST", "/inbounds", map[string]any{"protocol": "vless", "listen_port": 10000}}, {"POST", "/cert/regenerate", nil}, {"PUT", "/advanced", map[string]any{"extra_json": map[string]any{}}}} {
		res, out := e.do(t, route.method, base+route.path, token, route.body)
		if res.StatusCode != 409 {
			t.Fatalf("external write %s allowed or wrong status: %d %v", route.path, res.StatusCode, out)
		}
	}
	res, out := e.do(t, "GET", base+"/proxy-observations", token, nil)
	if res.StatusCode != 200 || out["management"] != "external" {
		t.Fatal("wrong external view", out)
	}
	res, _ = e.do(t, "POST", base+"/proxy-observations/refresh", token, map[string]any{"file": "/etc/shadow"})
	if res.StatusCode != 400 {
		t.Fatal("arbitrary refresh argument accepted")
	}
	res, _ = e.do(t, "POST", base+"/proxy-observations/refresh", token, nil)
	if res.StatusCode != 202 {
		t.Fatal("safe refresh blocked")
	}
	var envelope proto.Envelope
	if err := wsjson.Read(ctx, conn, &envelope); err != nil || envelope.Type != proto.TypeProxyRefresh {
		t.Fatal("wrong command delivered", envelope, err)
	}
	var count int
	if err := e.db.QueryRow("SELECT count(*) FROM config_revisions").Scan(&count); err != nil || count != 0 {
		t.Fatal("external request generated config revision")
	}
}
