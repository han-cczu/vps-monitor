package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"vpsmon/server/internal/proxy"
	"vpsmon/server/internal/store"
)

func TestAdvancedAPIAndRelayVisibility(t *testing.T) {
	var reject bool
	e := newTestEnv(t, func(d *Deps) {
		d.AdvancedCheck = func(context.Context, string, []byte) error {
			if reject {
				return errors.New("decode config: unknown field broken")
			}
			return nil
		}
	})
	ctx := context.Background()
	_ = e.db.SetSetting(ctx, "core.current_version", "1.14.0")
	token := e.adminToken(t)
	a, err := e.db.CreateServer(ctx, store.ServerInput{Name: "source"}, "source-token")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.db.CreateServer(ctx, store.ServerInput{Name: "target", PublicHost: "target.example.com"}, "target-token")
	if err != nil {
		t.Fatal(err)
	}
	port := 8388
	service := proxy.New(e.db, proxy.NoopNotifier{})
	inbound, err := service.SaveInbound(ctx, b.ID, 0, proxy.InboundInput{Protocol: "shadowsocks", ListenPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/servers/%d/advanced", a.ID)
	for _, route := range []struct{ method, path string }{{"POST", base + "/check"}, {"POST", base + "/relay"}, {"DELETE", base + "/relay"}} {
		resp, _ := e.do(t, route.method, route.path, "", nil)
		if resp.StatusCode != 401 {
			t.Fatal("advanced endpoint unauthenticated")
		}
	}
	reject = true
	resp, data := e.do(t, "POST", base+"/check", token, map[string]any{"extra_json": map[string]any{"broken": true}})
	if resp.StatusCode != 400 || data["message"] != "decode config: unknown field broken" {
		t.Fatal("check diagnostic not returned")
	}
	reject = false
	resp, _ = e.do(t, "POST", base+"/relay", token, map[string]any{"target_server_id": b.ID, "target_inbound_id": inbound.ID})
	if resp.StatusCode != 200 {
		t.Fatal("relay API failed")
	}
	_, data = e.do(t, "GET", "/api/subscribers", token, nil)
	if len(data["subscribers"].([]any)) != 0 {
		t.Fatal("relay appears in ordinary subscriber list")
	}
	_, data = e.do(t, "GET", "/api/subscribers?include_relay=1", token, nil)
	if len(data["subscribers"].([]any)) != 1 {
		t.Fatal("relay unavailable to node detail")
	}
	resp, _ = e.do(t, "DELETE", base+"/relay", token, nil)
	if resp.StatusCode != 200 {
		t.Fatal("relay removal API failed")
	}
}
