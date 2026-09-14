package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/server/internal/store"
)

type notices struct {
	mu      sync.Mutex
	ids     []int64
	reasons []string
}

func (n *notices) NodeChanged(id int64, reason string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ids = append(n.ids, id)
	n.reasons = append(n.reasons, reason)
}
func (n *notices) take() []int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	ids := append([]int64(nil), n.ids...)
	n.ids = nil
	return ids
}
func ptr[T any](v T) *T { return &v }
func setup(t *testing.T) (*Service, *store.DB, *notices, []int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "vm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, name := range []string{"one", "two"} {
		node, err := db.CreateServer(ctx, store.ServerInput{Name: name}, name+"-token")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, node.ID)
	}
	n := &notices{}
	s := New(db, n)
	s.now = func() time.Time { return time.Date(2026, 9, 15, 1, 30, 0, 0, time.FixedZone("panel", 8*3600)) }
	return s, db, n, ids
}
func makeInbound(t *testing.T, s *Service, node int64, protocol string, port int) *store.Inbound {
	t.Helper()
	i, err := s.SaveInbound(context.Background(), node, 0, InboundInput{Protocol: protocol, ListenPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func makeSubscriber(t *testing.T, s *Service) *store.Subscriber {
	t.Helper()
	sub, err := s.SaveSubscriber(context.Background(), 0, SubscriberInput{Name: ptr("subscriber")})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestInboundTransactionsPortsCertificateAndAudit(t *testing.T) {
	s, db, n, nodes := setup(t)
	ctx := context.Background()
	ss := makeInbound(t, s, nodes[0], "shadowsocks", 8443)
	n.take()
	if _, err := s.SaveInbound(ctx, nodes[0], 0, InboundInput{Protocol: "tuic", ListenPort: ptr(8443)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UDP conflict accepted: %v", err)
	}
	if len(n.take()) != 0 {
		t.Fatal("notified on failure")
	}
	v := makeInbound(t, s, nodes[0], "vless", 443)
	h := makeInbound(t, s, nodes[0], "hysteria2", 443)
	c, err := s.Cert(ctx, nodes[0])
	if err != nil || c == nil || c.SNI != "www.bing.com" {
		t.Fatal("missing automatic certificate")
	}
	updated, err := s.SaveInbound(ctx, 0, ss.ID, InboundInput{Enabled: ptr(false), Remark: ptr("changed")})
	if err != nil || updated.Enabled || string(updated.Settings) != string(ss.Settings) {
		t.Fatal("ordinary edit rotated keys")
	}
	if _, err = s.SaveInbound(ctx, nodes[0], 0, InboundInput{Protocol: "tuic", ListenPort: ptr(8443)}); !errors.Is(err, ErrConflict) {
		t.Fatal("disabled inbound lost port reservation")
	}
	if _, err = s.SaveInbound(ctx, 0, h.ID, InboundInput{ListenPort: ptr(8443)}); !errors.Is(err, ErrConflict) {
		t.Fatal("update port conflict accepted")
	}
	for _, i := range []*store.Inbound{ss, v, h} {
		rotated, err := s.RegenerateKeys(ctx, i.ID)
		if err != nil || string(rotated.Settings) == string(i.Settings) {
			t.Fatalf("failed key rotation: %v", err)
		}
	}
	if _, err = s.RegenerateCert(ctx, nodes[0], "changed.example.com"); err != nil {
		t.Fatal(err)
	}
	audits, err := db.ListAudit(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]string
	_ = json.Unmarshal(ss.Settings, &raw)
	secrets := []string{raw["server_psk"], c.KeyPEM}
	for _, entry := range audits {
		for _, secret := range secrets {
			if secret != "" && strings.Contains(entry.Before+entry.After, secret) {
				t.Fatal("audit leaked credentials")
			}
		}
	}
	if len(audits) != 9 {
		t.Fatalf("unexpected audit count %d", len(audits))
	}
	if err = s.DeleteInbound(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribersAssignmentsAndReset(t *testing.T) {
	s, db, n, nodes := setup(t)
	ctx := context.Background()
	a := makeInbound(t, s, nodes[0], "vless", 443)
	b := makeInbound(t, s, nodes[1], "tuic", 8444)
	sub := makeSubscriber(t, s)
	if sub.PeriodStart != time.Date(2026, 9, 15, 0, 0, 0, 0, s.now().Location()).Unix() {
		t.Fatal("period did not use panel midnight")
	}
	if sub.UUID == "" || sub.Password == "" || sub.SSUserKey == "" || len(sub.SubToken) != 43 {
		t.Fatal("missing credentials")
	}
	n.take()
	sub, err := s.Assign(ctx, sub.ID, []int64{a.ID, b.ID})
	if err != nil || sub.ServersCount != 2 || len(sub.AssignedInbounds) != 2 || !slices.Equal(n.take(), nodes) {
		t.Fatalf("assignment failed: %v", err)
	}
	sub, err = s.Assign(ctx, sub.ID, []int64{b.ID})
	if err != nil || !slices.Equal(n.take(), nodes) {
		t.Fatal("did not notify old and new nodes")
	}
	if _, err = s.Assign(ctx, sub.ID, []int64{a.ID, 99999}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing inbound accepted")
	}
	current, _ := s.Subscriber(ctx, sub.ID)
	if len(current.AssignedInbounds) != 1 || current.AssignedInbounds[0].InboundID != b.ID {
		t.Fatal("failed assignment was partially applied")
	}
	if len(n.take()) != 0 {
		t.Fatal("notified invalid assignment")
	}
	list, err := s.Subscribers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	for _, secret := range []string{sub.UUID, sub.Password, sub.SubToken, sub.SSUserKey} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("list leaked credentials")
		}
	}
	rotated, err := s.SubscriberAction(ctx, sub.ID, "regenerate-credentials")
	if err != nil || rotated.UUID == sub.UUID || rotated.Password == sub.Password || rotated.SSUserKey == sub.SSUserKey || rotated.SubToken != sub.SubToken {
		t.Fatal("wrong credential rotation")
	}
	if got := n.take(); !slices.Equal(got, []int64{nodes[1]}) {
		t.Fatalf("rotation notifications=%v", got)
	}
	tokenReset, err := s.SubscriberAction(ctx, sub.ID, "reset-token")
	if err != nil || tokenReset.SubToken == sub.SubToken || tokenReset.UUID != rotated.UUID {
		t.Fatal("wrong token rotation")
	}
	if len(n.take()) != 0 {
		t.Fatal("token does not affect core config")
	}
	if _, err = db.ExecContext(ctx, `UPDATE subscribers SET traffic_used=100,auto_disabled='quota' WHERE id=?`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO subscriber_traffic VALUES(?,?,?,40,60)`, sub.ID, nodes[1], sub.PeriodStart); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO subscriber_traffic_daily VALUES(?,'2026-09-15',40,60)`, sub.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := s.SubscriberAction(ctx, sub.ID, "reset-usage")
	if err != nil || reset.TrafficUsed != 0 || reset.AutoDisabled != "none" || reset.PeriodStart != sub.PeriodStart {
		t.Fatal("wrong usage reset")
	}
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM subscriber_traffic WHERE subscriber_id=?", sub.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old per-node counters would restore cleared quota")
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM subscriber_traffic_daily WHERE subscriber_id=?", sub.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("historical chart erased")
	}
	n.take()
	_, err = s.SaveSubscriber(ctx, sub.ID, SubscriberInput{Enabled: ptr(false)})
	if err != nil || !slices.Equal(n.take(), []int64{nodes[1]}) {
		t.Fatal("disable did not notify")
	}
	if err = s.DeleteSubscriber(ctx, sub.ID); err != nil || !slices.Equal(n.take(), []int64{nodes[1]}) {
		t.Fatal("delete did not notify")
	}
}

func TestAuditFailureRollsBackAndNoNotification(t *testing.T) {
	s, db, n, nodes := setup(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `CREATE TRIGGER deny_proxy_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveInbound(ctx, nodes[0], 0, InboundInput{Protocol: "hysteria2", ListenPort: ptr(443)}); err == nil {
		t.Fatal("write succeeded without audit")
	}
	for _, table := range []string{"inbounds", "certs"} {
		var count int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s not rolled back", table)
		}
	}
	if len(n.take()) != 0 {
		t.Fatal("notified rolled-back write")
	}
}

func TestConcurrentPortReservation(t *testing.T) {
	s, db, n, nodes := setup(t)
	other := New(db, n)
	ctx := context.Background()
	ready := make(chan struct{})
	results := make(chan error, 2)
	for _, service := range []*Service{s, other} {
		go func(service *Service) {
			<-ready
			_, err := service.SaveInbound(ctx, nodes[0], 0, InboundInput{Protocol: "shadowsocks", ListenPort: ptr(8443)})
			results <- err
		}(service)
	}
	close(ready)
	success, conflict := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 || conflict != 1 || len(n.take()) != 1 {
		t.Fatal("concurrent duplicate port")
	}
}

func TestMigrationCascadesAndCoreInitialization(t *testing.T) {
	s, db, _, nodes := setup(t)
	ctx := context.Background()
	for _, table := range []string{"inbounds", "certs", "node_core", "config_revisions", "node_advanced", "subscribers", "subscriber_assignments", "subscriber_traffic", "subscriber_traffic_daily"} {
		var name string
		if err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("missing %s", table)
		}
	}
	if c, err := s.Core(ctx, nodes[0]); err != nil || c.Core != "sing-box" || c.AppliedRevision != 0 {
		t.Fatal("new node missing core row")
	}
	i := makeInbound(t, s, nodes[0], "hysteria2", 443)
	sub := makeSubscriber(t, s)
	if _, err := s.Assign(ctx, sub.ID, []int64{i.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAdvanced(ctx, nodes[0], []byte(`{"route":{"rules":[]}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO config_revisions VALUES(?,1,'{}','sha',1,'test')`, nodes[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subscriber_traffic VALUES(?,?,?,1,2)`, sub.ID, nodes[0], sub.PeriodStart); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id=?", nodes[0]); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"inbounds", "certs", "node_core", "node_advanced", "config_revisions"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE server_id=?", nodes[0]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascade failed %s", table)
		}
	}
	got, err := s.Subscriber(ctx, sub.ID)
	if err != nil || len(got.AssignedInbounds) != 0 || got.ServersCount != 0 {
		t.Fatal("assignments survived node deletion")
	}
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM subscriber_traffic").Scan(&count); err != nil || count != 1 {
		t.Fatal("node deletion erased accounted traffic")
	}
	if err = s.DeleteSubscriber(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM subscriber_traffic").Scan(&count); err != nil || count != 0 {
		t.Fatal("subscriber traffic cascade failed")
	}
}

func TestAdvancedValidationAndRedaction(t *testing.T) {
	s, db, n, nodes := setup(t)
	ctx := context.Background()
	for _, raw := range []string{`null`, `[]`, `{"inbounds":[]}`, `{"experimental":{}}`, `{"log":{}}`, `{"outbounds":null}`, `{"outbounds":[{"tag":"direct","type":"direct"}]}`, `{"route":{"rules":[null]}}`, `{"route":{"final":null}}`} {
		if _, err := s.SaveAdvanced(ctx, nodes[0], []byte(raw)); err == nil {
			t.Fatalf("accepted advanced %s", raw)
		}
	}
	_, err := s.SaveAdvanced(ctx, nodes[0], []byte(`{"outbounds":[{"tag":"remote","type":"shadowsocks","password":"sensitive-advanced-password"}],"route":{"final":"remote"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.take(), []int64{nodes[0]}) {
		t.Fatal("advanced not notified")
	}
	entries, err := db.ListAudit(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Before+entry.After, "sensitive-advanced-password") {
			t.Fatal("advanced audit leaked password")
		}
	}
}

type notifierFunc func(int64, string)

func (f notifierFunc) NodeChanged(id int64, reason string) { f(id, reason) }
func TestNotificationsRunAfterCommit(t *testing.T) {
	s, db, _, nodes := setup(t)
	ctx := context.Background()
	observed := false
	s.notifier = notifierFunc(func(id int64, _ string) {
		items, err := db.Proxy().Inbounds(ctx, id)
		if err != nil || len(items) != 1 {
			t.Fatal("notification before commit")
		}
		observed = true
	})
	makeInbound(t, s, nodes[0], "vless", 443)
	if !observed {
		t.Fatal("no notification")
	}
}

func TestUsageResetPreservesExpiredAndHistoricalPeriods(t *testing.T) {
	s, db, _, nodes := setup(t)
	ctx := context.Background()
	sub := makeSubscriber(t, s)
	if _, err := db.ExecContext(ctx, `UPDATE subscribers SET enabled=0,auto_disabled='expired',traffic_used=100 WHERE id=?`, sub.ID); err != nil {
		t.Fatal(err)
	}
	for _, period := range []int64{sub.PeriodStart - 86400, sub.PeriodStart} {
		if _, err := db.ExecContext(ctx, `INSERT INTO subscriber_traffic VALUES(?,?,?,1,2)`, sub.ID, nodes[0], period); err != nil {
			t.Fatal(err)
		}
	}
	reset, err := s.SubscriberAction(ctx, sub.ID, "reset-usage")
	if err != nil || reset.Enabled || reset.AutoDisabled != "expired" || reset.TrafficUsed != 0 {
		t.Fatal("reset incorrectly re-enabled user")
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM subscriber_traffic WHERE period_start=?`, sub.PeriodStart-86400).Scan(&count); err != nil || count != 1 {
		t.Fatal("historical period erased")
	}
}
