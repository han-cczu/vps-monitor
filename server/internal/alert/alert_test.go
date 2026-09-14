package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

func alertDB(t *testing.T) (*store.DB, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	node, err := db.CreateServer(context.Background(), store.ServerInput{Name: "test <node>", TrafficMode: "max", TrafficResetDay: 1, BillingCycle: "month", Currency: "CNY"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	return db, node.ID
}
func webhook(t *testing.T, db *store.DB, url string) store.NotifyChannel {
	t.Helper()
	raw, _ := json.Marshal(ChannelConfig{URL: url, Secret: "test-secret"})
	c := store.NotifyChannel{Name: "test", Kind: "webhook", Config: raw, Enabled: true, CreatedAt: time.Now().Unix()}
	if err := db.SaveNotifyChannel(context.Background(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func localSender(server *httptest.Server) *Sender {
	return &Sender{Client: server.Client(), TelegramBase: server.URL}
}

func TestSustainedNeedsContiguousCompletedMinutes(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 30, 0, time.UTC)
	ring := &[16]minute{}
	p := Params{Minutes: 10, Percent: 90}
	for i := 0; i < 10; i++ {
		ts := now.Unix()/60 - int64(i)
		ring[ts%16] = minute{TS: ts, CPU: 100, Mem: 95, Disk: 99, Count: 1}
	}
	if sustained(ring, now, "server.cpu", p) {
		t.Fatal("partial current minute counted toward duration")
	}
	ts := now.Unix()/60 - 10
	ring[ts%16] = minute{TS: ts, CPU: 100, Mem: 95, Disk: 99, Count: 1}
	for _, kind := range []string{"server.cpu", "server.mem", "server.disk"} {
		if !sustained(ring, now, kind, p) {
			t.Fatalf("expected sustained %s", kind)
		}
	}
	ring[(ts+4)%16].CPU = 90
	if sustained(ring, now, "server.cpu", p) {
		t.Fatal("threshold equality counted")
	}
	ring[(ts+4)%16].Count = 0
	if complete(ring, now, 10) {
		t.Fatal("gap treated as complete")
	}
	if sustained(ring, now.Add(17*time.Minute), "server.disk", p) {
		t.Fatal("stale ring slots counted")
	}
}

func TestOfflineDedupeRecoveryCooldownAndWebhookSignature(t *testing.T) {
	ctx := context.Background()
	db, id := alertDB(t)
	var sends atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("test-secret"))
		mac.Write(body)
		if r.Header.Get("X-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("bad HMAC")
		}
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil {
			t.Error("bad JSON")
		}
		sends.Add(1)
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	webhook(t, db, receiver.URL)
	registry := hub.NewRegistry(db)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	s := New(db, registry, &hub.Bus{}, localSender(receiver))
	s.now = func() time.Time { return now }
	s.started = now.Add(-time.Hour)
	registry.Update(id, func(st *hub.ServerState) { st.LastSeen = now.Add(-4 * time.Minute) })
	for range 2 {
		if err := s.Evaluate(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	events, total, open, err := db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if err != nil || total != 1 || open != 1 {
		t.Fatalf("dedupe total=%d open=%d err=%v", total, open, err)
	}
	if err := s.Deliver(ctx, now); err != nil {
		t.Fatal(err)
	}
	registry.Update(id, func(st *hub.ServerState) { st.Online = true; st.LastSeen = now })
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx, now); err != nil {
		t.Fatal(err)
	}
	resolved, _ := db.AlertEvent(ctx, events[0].ID)
	if resolved.ResolvedAt == nil || resolved.NotifiedAt == nil || sends.Load() != 2 {
		t.Fatalf("recovery=%+v sends=%d", resolved, sends.Load())
	}
	now = now.Add(5 * time.Minute)
	registry.Update(id, func(st *hub.ServerState) { st.Online = false })
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx, now); err != nil {
		t.Fatal(err)
	}
	events, total, open, err = db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if err != nil || total != 2 || open != 1 || events[0].NotifiedAt != nil || sends.Load() != 2 {
		t.Fatalf("cooldown total=%d open=%d sends=%d err=%v", total, open, sends.Load(), err)
	}
}

func TestDeliveryRestartRetriesPerChannelAndStopsAfterThree(t *testing.T) {
	ctx := context.Background()
	db, id := alertDB(t)
	var good, bad atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			bad.Add(1)
			w.WriteHeader(503)
		} else {
			good.Add(1)
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	webhook(t, db, receiver.URL+"/good")
	webhook(t, db, receiver.URL+"/bad")
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	e := store.AlertEvent{RuleKind: "server.offline", TargetType: "server", TargetID: id, Level: "warning", Title: "offline", FiredAt: now.Unix(), DedupeKey: "server.offline:server:1"}
	if created, err := db.FireAlert(ctx, &e, "", 1800); err != nil || !created {
		t.Fatalf("fire=%v err=%v", created, err)
	}
	for i := 0; i < 5; i++ {
		s := New(db, nil, nil, localSender(receiver))
		s.now = func() time.Time { return now }
		if err := s.Deliver(ctx, now); err != nil {
			t.Fatal(err)
		}
		if err := s.Deliver(ctx, now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(30 * time.Second)
	}
	if good.Load() != 1 || bad.Load() != 3 {
		t.Fatalf("good=%d bad=%d", good.Load(), bad.Load())
	}
	stored, _ := db.AlertEvent(ctx, e.ID)
	if stored.NotifiedAt != nil {
		t.Fatal("partial delivery marked fully notified")
	}
}

func TestFailureCanRecoverOnRetryAndTelegramIsEscaped(t *testing.T) {
	ctx := context.Background()
	db, id := alertDB(t)
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["parse_mode"] != "HTML" || strings.Contains(payload["text"], "<node>") || !strings.Contains(payload["text"], "&lt;node&gt;") {
			t.Errorf("unescaped Telegram payload: %v", payload)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer receiver.Close()
	raw, _ := json.Marshal(ChannelConfig{BotToken: "123:LOCAL_TEST_ONLY", ChatID: "1"})
	channel := store.NotifyChannel{Name: "local Telegram", Kind: "telegram", Config: raw, Enabled: true}
	if err := db.SaveNotifyChannel(ctx, &channel); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	e := store.AlertEvent{RuleKind: "core.apply_failed", TargetType: "server", TargetID: id, Title: "<node>", FiredAt: now.Unix(), DedupeKey: "core:1"}
	if _, err := db.FireAlert(ctx, &e, "", 0); err != nil {
		t.Fatal(err)
	}
	s := New(db, nil, nil, localSender(receiver))
	s.now = func() time.Time { return now }
	if err := s.Deliver(ctx, now); err != nil {
		t.Fatal(err)
	}
	first, _ := db.AlertEvent(ctx, e.ID)
	if first.NotifiedAt != nil {
		t.Fatal("failed send marked notified")
	}
	now = now.Add(30 * time.Second)
	if err := s.Deliver(ctx, now); err != nil {
		t.Fatal(err)
	}
	last, _ := db.AlertEvent(ctx, e.ID)
	if last.NotifiedAt == nil || calls.Load() != 2 {
		t.Fatalf("retry=%+v calls=%d", last, calls.Load())
	}
}

func TestSubscriberThresholdIsOncePerPeriodAndNoRecovery(t *testing.T) {
	ctx := context.Background()
	db, _ := alertDB(t)
	_, err := db.ExecContext(ctx, `INSERT INTO subscribers(id,name,sub_token,uuid,password,ss_user_key,period_start,expire_at,created_at,updated_at) VALUES(1,'subscriber','t','u','p','s',100,'2026-09-14',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	s := New(db, nil, nil, nil)
	event := hub.Event{Kind: "subscriber.quota", TargetType: "subscriber", TargetID: 1, Threshold: 80, At: now}
	for range 2 {
		if err := s.Handle(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, open, err := db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if err != nil || total != 1 || open != 0 || rows[0].ResolvedAt == nil {
		t.Fatalf("threshold total=%d open=%d rows=%v err=%v", total, open, rows, err)
	}
	_, _ = db.ExecContext(ctx, `UPDATE subscribers SET period_start=200 WHERE id=1`)
	event.At = now.Add(24 * time.Hour)
	if err := s.Handle(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := s.Handle(ctx, hub.Event{Kind: "subscriber.restored", TargetType: "subscriber", TargetID: 1, At: now}); err != nil {
		t.Fatal(err)
	}
	_, total, open, _ = db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if total != 2 || open != 0 {
		t.Fatalf("new period total=%d open=%d", total, open)
	}
}

func TestRuleAndChannelValidation(t *testing.T) {
	for _, r := range []store.AlertRule{{Kind: "server.cpu", Params: json.RawMessage(`{"percent":90,"minutes":16}`)}, {Kind: "server.traffic", Params: json.RawMessage(`{"percents":[80,80]}`)}, {Kind: "subscriber.quota", Params: json.RawMessage(`{"percents":[90]}`)}, {Kind: "core.down", Params: json.RawMessage(`{"minutes":1,"extra":1}`)}} {
		if _, err := ParseParams(r); err == nil {
			t.Fatalf("invalid rule accepted: %+v", r)
		}
	}
	for _, raw := range []string{`{"url":"file:///tmp/a"}`, `{"url":"http://user:pass@example.com"}`, `{"url":"http://example.com/#fragment"}`, `{"url":"http://example.com/","bot_token":"secret"}`} {
		if _, err := DecodeChannel("webhook", json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid config accepted: %s", raw)
		}
	}
}

func TestPingAndCoreStateRecoveryAndDisabledRules(t *testing.T) {
	ctx := context.Background()
	db, id := alertDB(t)
	registry := hub.NewRegistry(db)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	stamp := now.Unix()
	loss := 50.0
	running := false
	registry.Update(id, func(st *hub.ServerState) { st.Online = true; st.LastSeen = now })
	registry.SetPingSource(func(int64) []hub.PingView {
		return []hub.PingView{{TaskID: 7, Name: "local", Loss: loss, LastTS: &stamp}}
	})
	registry.SetCoreSource(func(int64) any { return map[string]any{"installed": true, "running": running} })
	s := New(db, registry, nil, nil)
	s.now = func() time.Time { return now }
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	stamp = now.Unix()
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	events, _, open, err := db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if err != nil || open != 2 {
		t.Fatalf("ping/core events=%+v open=%d err=%v", events, open, err)
	}
	registry.Update(id, func(st *hub.ServerState) { st.Online = false })
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	_, _, open, _ = db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if open != 2 {
		t.Fatal("missing observations fabricated recovery")
	}
	registry.Update(id, func(st *hub.ServerState) { st.Online = true })
	loss = 0
	running = true
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	_, _, open, _ = db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if open != 0 {
		t.Fatal("observed recovery left events open")
	}
	loss = 50
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	_, _ = db.ExecContext(ctx, `UPDATE alert_rules SET enabled=0 WHERE kind='ping.loss'`)
	if err := s.Evaluate(ctx, now); err != nil {
		t.Fatal(err)
	}
	_, _, open, _ = db.ListAlertEvents(ctx, store.AlertFilter{Page: 1})
	if open != 0 {
		t.Fatal("disabled rule remained open")
	}
}
