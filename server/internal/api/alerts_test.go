package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"vpsmon/server/internal/alert"
	"vpsmon/server/internal/store"
)

func TestAlertAPIChannelsRulesEventsAndExplicitLocalTest(t *testing.T) {
	var received atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Signature") == "" {
			t.Error("test lacked HMAC")
		}
		received.Add(1)
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	e := newTestEnv(t, func(d *Deps) {
		d.Alerts = alert.New(d.DB, nil, nil, &alert.Sender{Client: receiver.Client(), TelegramBase: receiver.URL})
	})
	token := e.adminToken(t)
	for _, path := range []string{"/api/alert-rules", "/api/notify-channels", "/api/alert-events"} {
		resp, _ := e.do(t, "GET", path, "", nil)
		if resp.StatusCode != 401 {
			t.Fatalf("%s auth=%d", path, resp.StatusCode)
		}
	}
	payload := map[string]any{"name": "local test", "kind": "webhook", "enabled": true, "config": map[string]string{"url": receiver.URL, "secret": "sensitive-test-secret"}}
	resp, body := e.do(t, "POST", "/api/notify-channels", token, payload)
	if resp.StatusCode != 201 {
		t.Fatalf("create=%d %v", resp.StatusCode, body)
	}
	channel := body["channel"].(map[string]any)
	id := int64(channel["id"].(float64))
	path := "/api/notify-channels/" + strconv.FormatInt(id, 10)
	encoded, _ := json.Marshal(body)
	if strings.Contains(string(encoded), "sensitive-test-secret") {
		t.Fatal("create response leaked secret")
	}
	_, body = e.do(t, "GET", "/api/notify-channels", token, nil)
	encoded, _ = json.Marshal(body)
	if strings.Contains(string(encoded), "sensitive-test-secret") {
		t.Fatal("list response leaked secret")
	}
	if received.Load() != 0 {
		t.Fatal("channel CRUD sent an unsolicited message")
	}
	resp, body = e.do(t, "POST", path+"/test", token, nil)
	if resp.StatusCode != 200 || received.Load() != 1 {
		t.Fatalf("test=%d body=%v count=%d", resp.StatusCode, body, received.Load())
	}
	payload["config"] = map[string]string{"url": receiver.URL, "secret": ""}
	resp, _ = e.do(t, "PUT", path, token, payload)
	if resp.StatusCode != 200 {
		t.Fatal("update failed")
	}
	stored, _ := e.db.NotifyChannel(context.Background(), id)
	if !strings.Contains(string(stored.Config), "sensitive-test-secret") {
		t.Fatal("blank edit discarded saved secret")
	}
	payload["clear_secret"] = true
	resp, _ = e.do(t, "PUT", path, token, payload)
	if resp.StatusCode != 200 {
		t.Fatal("clear failed")
	}
	stored, _ = e.db.NotifyChannel(context.Background(), id)
	if strings.Contains(string(stored.Config), "sensitive-test-secret") {
		t.Fatal("explicit clear ignored")
	}
	audit, err := e.db.ListAudit(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range audit {
		if strings.Contains(a.Before+a.After, "sensitive-test-secret") || strings.Contains(a.Before+a.After, receiver.URL) {
			t.Fatal("audit leaked channel config")
		}
	}
	_, body = e.do(t, "GET", "/api/alert-rules", token, nil)
	rules := body["rules"].([]any)
	if len(rules) != 11 {
		t.Fatalf("rules=%d", len(rules))
	}
	resp, _ = e.do(t, "PUT", "/api/alert-rules", token, rules[:1])
	if resp.StatusCode != 400 {
		t.Fatal("partial rules accepted")
	}
	rules[0].(map[string]any)["enabled"] = false
	resp, _ = e.do(t, "PUT", "/api/alert-rules", token, rules)
	if resp.StatusCode != 200 {
		t.Fatal("full rules rejected")
	}
	ev := store.AlertEvent{RuleKind: "server.offline", TargetType: "server", TargetID: 1, Level: "warning", Title: "offline", FiredAt: 100, DedupeKey: "offline:1"}
	if _, err := e.db.FireAlert(context.Background(), &ev, "", 0); err != nil {
		t.Fatal(err)
	}
	_, body = e.do(t, "GET", "/api/alert-events?open=1&kind=server.offline&target=server:1", token, nil)
	if body["open_count"] != float64(1) || body["total"] != float64(1) {
		t.Fatalf("filter=%v", body)
	}
	resp, _ = e.do(t, "POST", "/api/alert-events/"+strconv.FormatInt(ev.ID, 10)+"/resolve", token, nil)
	if resp.StatusCode != 200 {
		t.Fatal("resolve failed")
	}
	_, body = e.do(t, "GET", "/api/alert-events?open=1", token, nil)
	if body["open_count"] != float64(0) {
		t.Fatal("open count stale")
	}
	for _, query := range []string{"page=0", "open=yes", "kind=unknown", "target=invalid", "target=server:-1"} {
		resp, _ := e.do(t, "GET", "/api/alert-events?"+query, token, nil)
		if resp.StatusCode != 400 {
			t.Fatalf("invalid filter accepted: %s", query)
		}
	}
	resp, _ = e.do(t, "DELETE", path, token, nil)
	if resp.StatusCode != 204 {
		t.Fatal("delete failed")
	}
}
