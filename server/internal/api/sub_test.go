package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
	"vpsmon/server/internal/proxy"
	"vpsmon/server/internal/store"
)

func TestSubscriptionLifecycleCacheHeadersLogs(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	service := proxy.New(e.db, proxy.NoopNotifier{})
	input := store.ServerInput{Name: "yes", PublicHost: "first.example.com"}
	node, err := e.db.CreateServer(ctx, input, "hash")
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	inbound, err := service.SaveInbound(ctx, node.ID, 0, proxy.InboundInput{Protocol: "vless", ListenPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	name := "测试 名称"
	subscriber, err := service.SaveSubscriber(ctx, 0, proxy.SubscriberInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	subscriber, err = service.Assign(ctx, subscriber.ID, []int64{inbound.ID})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)
	get := func(token, format string, want int) (http.Header, string) {
		t.Helper()
		resp, err := e.srv.Client().Get(e.srv.URL + "/sub/" + token + "?format=" + format)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("status %d want %d", resp.StatusCode, want)
		}
		return resp.Header, string(b)
	}
	headers, body := get(subscriber.SubToken, "clash", 200)
	if !strings.Contains(body, "first.example.com") || headers.Get("Cache-Control") != "no-store" || headers.Get("subscription-userinfo") != "upload=0; download=0" || headers.Get("profile-update-interval") != "24" {
		t.Fatal("subscription response missing contract")
	}
	input.PublicHost = "second.example.com"
	if _, err = e.db.UpdateServer(ctx, node.ID, input); err != nil {
		t.Fatal(err)
	}
	_, body = get(subscriber.SubToken, "clash", 200)
	if !strings.Contains(body, "second.example.com") {
		t.Fatal("public_host cache stale")
	}
	subscriber, err = service.SubscriberAction(ctx, subscriber.ID, "regenerate-credentials")
	if err != nil {
		t.Fatal(err)
	}
	_, body = get(subscriber.SubToken, "clash", 200)
	if !strings.Contains(body, subscriber.UUID) {
		t.Fatal("credentials cache stale")
	}
	if _, err = e.db.ExecContext(ctx, "UPDATE subscribers SET traffic_used=123 WHERE id=?", subscriber.ID); err != nil {
		t.Fatal(err)
	}
	headers, _ = get(subscriber.SubToken, "clash", 200)
	if headers.Get("subscription-userinfo") != "upload=0; download=123" {
		t.Fatal("usage header stale")
	}
	disabled := false
	if _, err = service.SaveSubscriber(ctx, subscriber.ID, proxy.SubscriberInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	_, body = get(subscriber.SubToken, "clash", 200)
	if !strings.HasPrefix(body, "# 已停用：手动停用\nproxies: []") {
		t.Fatal("disabled cached clash subscription contains nodes")
	}
	_, body = get(subscriber.SubToken, "clash-provider", 200)
	if !strings.HasPrefix(body, "# 已停用：手动停用\nproxies: []") {
		t.Fatal("disabled subscription contains nodes")
	}
	oldToken := subscriber.SubToken
	subscriber, err = service.SubscriberAction(ctx, subscriber.ID, "reset-token")
	if err != nil {
		t.Fatal(err)
	}
	_, body = get(oldToken, "clash", 404)
	if body != "" {
		t.Fatal("unknown token body must be empty")
	}
	if err = e.db.SetSetting(ctx, "sub.clash_template", "broken"); err != nil {
		t.Fatal(err)
	}
	get(subscriber.SubToken, "clash", 500)
	if strings.Contains(logs.String(), oldToken) || strings.Contains(logs.String(), subscriber.SubToken) || strings.Contains(logs.String(), subscriber.UUID) {
		t.Fatal("subscription secret leaked to logs")
	}
	if !strings.Contains(logs.String(), "subscriber_id") {
		t.Fatal("missing subscriber metadata")
	}
}

func TestSubscriptionEpochCommittedSources(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	service := proxy.New(e.db, proxy.NoopNotifier{})
	node, err := e.db.CreateServer(ctx, store.ServerInput{Name: "node", PublicHost: "example.com"}, "epoch-token")
	if err != nil {
		t.Fatal(err)
	}
	port := 8443
	inbound, err := service.SaveInbound(ctx, node.ID, 0, proxy.InboundInput{Protocol: "hysteria2", ListenPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	name := "epoch"
	subscriber, err := service.SaveSubscriber(ctx, 0, proxy.SubscriberInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"assignment", func() error { _, err := service.Assign(ctx, subscriber.ID, []int64{inbound.ID}); return err }},
		{"certificate", func() error { _, err := service.RegenerateCert(ctx, node.ID, "new.example.com"); return err }},
		{"inbound keys", func() error { _, err := service.RegenerateKeys(ctx, inbound.ID); return err }},
		{"subscriber credentials", func() error {
			_, err := service.SubscriberAction(ctx, subscriber.ID, "regenerate-credentials")
			return err
		}},
		{"token", func() error { _, err := service.SubscriberAction(ctx, subscriber.ID, "reset-token"); return err }},
		{"template", func() error {
			return e.db.SetSetting(ctx, "sub.clash_template", "{{PROXIES}}\nnames: [{{PROXY_NAMES}}]")
		}},
		{"inbound delete", func() error { return service.DeleteInbound(ctx, inbound.ID) }},
		{"server delete", func() error { return e.db.DeleteServer(ctx, node.ID) }},
		{"subscriber delete", func() error { return service.DeleteSubscriber(ctx, subscriber.ID) }},
	}
	for _, operation := range operations {
		before := e.db.SubscriptionEpoch()
		if err = operation.run(); err != nil {
			t.Fatalf("%s: %v", operation.name, err)
		}
		if e.db.SubscriptionEpoch() <= before {
			t.Fatalf("%s did not invalidate subscriptions", operation.name)
		}
	}
}
func TestSubscriptionLimiterBoundary(t *testing.T) {
	c := newSubscriptionCache()
	hash := sha256.Sum256([]byte("secret"))
	now := time.Now()
	for i := 0; i < 30; i++ {
		if !c.allow(hash, now) {
			t.Fatal("premature rate limit")
		}
	}
	if c.allow(hash, now) {
		t.Fatal("31st accepted")
	}
	if !c.allow(hash, now.Add(time.Minute)) {
		t.Fatal("window not reset")
	}
}
func TestSubscriberTrafficHistoryAndDeletedNode(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	service := proxy.New(e.db, proxy.NoopNotifier{})
	name := "traffic"
	subscriber, err := service.SaveSubscriber(ctx, 0, proxy.SubscriberInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	for _, sql := range []string{"INSERT INTO subscriber_traffic(subscriber_id,server_id,period_start,up_bytes,down_bytes) VALUES(?,999,?,10,20)", "INSERT INTO subscriber_traffic_daily(subscriber_id,date,up_bytes,down_bytes) VALUES(?,'2026-09-14',30,40)"} {
		args := []any{subscriber.ID}
		if strings.Contains(sql, "period_start") {
			args = append(args, subscriber.PeriodStart)
		}
		if _, err = e.db.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	traffic, err := e.db.SubscriberTraffic(ctx, subscriber.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(traffic.ByServer) != 1 || traffic.ByServer[0].Name != "已删除节点" || len(traffic.Daily) != 30 || traffic.Daily[29].Down != 40 || traffic.Daily[0].Date != "2026-08-16" {
		t.Fatalf("unexpected traffic %#v", traffic)
	}
}
