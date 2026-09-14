package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/proxy/render"
)

func TestAdvancedPreflightRejectsStaleAndDoesNotWrite(t *testing.T) {
	service, db, notices, ids := setup(t)
	ctx := context.Background()
	if err := db.SetSetting(ctx, corefiles.CurrentKey, "1.14.0"); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"dns":{"servers":[{"type":"local","tag":"local"}]}}`)
	reject := func(context.Context, string, []byte) error { return errors.New("unknown field: broken") }
	if _, err := service.SaveAdvancedChecked(ctx, ids[0], raw, reject); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatal("core check error not returned")
	}
	saved, _ := service.Advanced(ctx, ids[0])
	if string(saved.ExtraJSON) != "{}" {
		t.Fatal("failed preflight persisted")
	}
	passed := func(context.Context, string, []byte) error { return nil }
	if err := service.CheckAdvanced(ctx, ids[0], raw, passed); err != nil {
		t.Fatal(err)
	}
	saved, _ = service.Advanced(ctx, ids[0])
	if string(saved.ExtraJSON) != "{}" {
		t.Fatal("check endpoint mutated")
	}
	changed := func(context.Context, string, []byte) error {
		_, err := service.SaveAdvanced(ctx, ids[0], json.RawMessage(`{"route":{"final":"direct"}}`))
		return err
	}
	if _, err := service.SaveAdvancedChecked(ctx, ids[0], raw, changed); !errors.Is(err, ErrAdvancedChanged) {
		t.Fatalf("stale preflight accepted: %v", err)
	}
	notices.take()
	if _, err := service.SaveAdvancedChecked(ctx, ids[0], raw, passed); err != nil {
		t.Fatal(err)
	}
	if got := notices.take(); len(got) != 1 || got[0] != ids[0] {
		t.Fatal("saved advanced not notified")
	}
	var count int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM config_revisions").Scan(&count)
	if count != 0 {
		t.Fatal("preflight created a revision")
	}
}

func TestRelayRealCorePreflight(t *testing.T) {
	binary := os.Getenv("VM_TEST_SINGBOX")
	if binary == "" {
		t.Skip("set VM_TEST_SINGBOX for a real local core preflight")
	}
	service, db, _, ids := setup(t)
	ctx := context.Background()
	_ = db.SetSetting(ctx, corefiles.CurrentKey, "1.14.0")
	_, err := db.ExecContext(ctx, "UPDATE servers SET public_host='127.0.0.1' WHERE id=?", ids[1])
	if err != nil {
		t.Fatal(err)
	}
	inbound := makeInbound(t, service, ids[1], "shadowsocks", 8388)
	checker := func(ctx context.Context, _ string, config []byte) error {
		return render.CheckDetailed(ctx, binary, config)
	}
	if _, _, err = service.Relay(ctx, ids[0], ids[1], inbound.ID, checker); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RemoveRelay(ctx, ids[0], checker); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveAdvancedChecked(ctx, ids[0], json.RawMessage(`{"definitely_unknown_field":true}`), checker); err == nil || !strings.Contains(err.Error(), "definitely_unknown_field") {
		t.Fatalf("real core diagnostic unavailable: %v", err)
	}
}
func TestRelayAtomicIdempotentAndRemoval(t *testing.T) {
	service, db, notices, ids := setup(t)
	ctx := context.Background()
	if err := db.SetSetting(ctx, corefiles.CurrentKey, "1.14.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE servers SET public_host='target.example.com' WHERE id=?", ids[1]); err != nil {
		t.Fatal(err)
	}
	inbound := makeInbound(t, service, ids[1], "shadowsocks", 8388)
	reject := func(context.Context, string, []byte) error { return errors.New("check failed") }
	if _, _, err := service.Relay(ctx, ids[0], ids[1], inbound.ID, reject); err == nil {
		t.Fatal("failed relay check accepted")
	}
	all, _ := service.Subscribers(ctx)
	if len(all) != 0 {
		t.Fatal("failed relay left orphan subscriber")
	}
	passed := func(context.Context, string, []byte) error { return nil }
	notices.take()
	advanced, id, err := service.Relay(ctx, ids[0], ids[1], inbound.ID, passed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(advanced.ExtraJSON), "target.example.com") {
		t.Fatal("relay target missing")
	}
	if len(notices.take()) != 2 {
		t.Fatal("both nodes must be notified")
	}
	subscriber, err := service.Subscriber(ctx, id)
	if err != nil || subscriber.Kind != "relay" || len(subscriber.AssignedInbounds) != 1 || subscriber.TrafficLimit != 0 || subscriber.ExpireAt != nil {
		t.Fatal("relay subscriber contract")
	}
	if _, err = service.SubscriberAction(ctx, id, "regenerate-credentials"); err == nil {
		t.Fatal("relay credentials rotated independently of its outbound")
	}
	if _, err = service.SaveSubscriber(ctx, id, SubscriberInput{Name: ptr("renamed")}); err == nil {
		t.Fatal("relay identity changed outside helper")
	}
	_, same, err := service.Relay(ctx, ids[0], ids[1], inbound.ID, passed)
	if err != nil || same != id {
		t.Fatal("relay not idempotent")
	}
	var config map[string]any
	_ = json.Unmarshal(advanced.ExtraJSON, &config)
	if len(config["outbounds"].([]any)) != 1 {
		t.Fatal("duplicate relay outbound")
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO subscriber_traffic(subscriber_id,server_id,period_start,up_bytes,down_bytes) VALUES(?,?,?,?,?)", id, ids[1], subscriber.PeriodStart, 42, 64); err != nil {
		t.Fatal(err)
	}
	removed, err := service.RemoveRelay(ctx, ids[0], passed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(removed.ExtraJSON), `"final"`) {
		t.Fatal("relay final retained")
	}
	subscriber, _ = service.Subscriber(ctx, id)
	if len(subscriber.AssignedInbounds) != 0 {
		t.Fatal("removed relay still authorized on target")
	}
	traffic, err := db.NodeSubscriberTraffic(ctx, ids[1])
	if err != nil || len(traffic) != 1 || traffic[0].Kind != "relay" || traffic[0].Down != 64 {
		t.Fatal("relay historical node traffic lost after removal")
	}
	if _, _, err = service.Relay(ctx, ids[0], ids[0], inbound.ID, passed); err == nil {
		t.Fatal("self relay accepted")
	}
}
