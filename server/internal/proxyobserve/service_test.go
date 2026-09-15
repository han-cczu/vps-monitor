package proxyobserve

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

func fixture(t *testing.T) (*Service, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	node, err := db.CreateServer(context.Background(), store.ServerInput{Name: "observed"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	return New(db), node.ID
}
func frame(t *testing.T, seq int64, page, pages int, items ...proto.ObservedInstance) []byte {
	t.Helper()
	b, err := json.Marshal(proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: "current", Sequence: seq, Page: page, Pages: pages, ScanComplete: true, CollectedAt: 1, Instances: items})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func instance(id string) proto.ObservedInstance {
	return proto.ObservedInstance{ID: id, Core: "xray", Ownership: "external", Source: "generic", StatsStatus: "not_configured", Inbounds: []proto.ObservedInbound{{ID: "a", Protocol: "vless", Port: "1234", InConfig: true}}, ConfigReadAt: 1, LastSuccess: 1}
}
func TestAtomicPagesReplayAndLastKnownSnapshots(t *testing.T) {
	s, id := fixture(t)
	ctx := context.Background()
	item := instance("one")
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 2, item)); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.List(ctx, id, true)
	if len(rows) != 0 {
		t.Fatal("partial scan published")
	}
	part := item
	part.Inbounds = []proto.ObservedInbound{{ID: "b", Protocol: "hysteria"}}
	if err := s.Receive(ctx, id, "current", frame(t, 1, 1, 2, part)); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.List(ctx, id, true)
	if len(rows) != 1 || len(rows[0].Inbounds) != 2 {
		t.Fatal("pages not assembled")
	}
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 1, item)); err == nil {
		t.Fatal("replay accepted")
	}
	if err := s.Receive(ctx, id, "replacement", frame(t, 2, 0, 1, item)); err == nil {
		t.Fatal("old connection accepted")
	}
	if err := s.Receive(ctx, id, "current", frame(t, 2, 1, 2, item)); err == nil {
		t.Fatal("orphan page accepted")
	}
	item.Stale = true
	item.ConfigReadAt = 0
	item.LastSuccess = 0
	item.Inbounds = nil
	if err := s.Receive(ctx, id, "current", frame(t, 3, 0, 1, item)); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.List(ctx, id, true)
	if len(rows[0].Inbounds) != 2 || rows[0].ConfigReadAt != 1 {
		t.Fatal("restart parse failure discarded last known inbounds")
	}
	if err := s.Receive(ctx, id, "current", frame(t, 4, 0, 1)); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.List(ctx, id, true)
	if !rows[0].Absent || !rows[0].Stale {
		t.Fatal("absent snapshot not marked")
	}
}
func TestRejectCredentialsLimitsAndPageMixing(t *testing.T) {
	s, id := fixture(t)
	ctx := context.Background()
	i := instance("one")
	raw := frame(t, 1, 0, 1, i)
	var message map[string]any
	json.Unmarshal(raw, &message)
	message["password"] = "never-store"
	bad, _ := json.Marshal(message)
	if s.Receive(ctx, id, "current", bad) == nil {
		t.Fatal("unknown secret field accepted")
	}
	if s.Receive(ctx, id, "current", make([]byte, proto.ProxyMaxFrame+1)) == nil {
		t.Fatal("oversized frame accepted")
	}
	if s.Receive(ctx, id, "current", frame(t, 1, 0, 2, i)) != nil {
		t.Fatal("first page rejected")
	}
	i.Version = "changed"
	i.Inbounds = []proto.ObservedInbound{{ID: "b"}}
	if s.Receive(ctx, id, "current", frame(t, 1, 1, 2, i)) == nil {
		t.Fatal("mixed instance metadata accepted")
	}
	s.now = func() time.Time { return time.Now().Add(time.Minute) }
	i.Version = ""
	if s.Receive(ctx, id, "current", frame(t, 1, 1, 2, i)) == nil {
		t.Fatal("expired partial snapshot accepted")
	}
	rows, _ := s.List(ctx, id, true)
	if len(rows) != 0 {
		t.Fatal("invalid scan persisted")
	}
}
func TestPartialDiscoveryDoesNotMarkMissingAndTrafficIsolation(t *testing.T) {
	s, id := fixture(t)
	ctx := context.Background()
	i := instance("one")
	u, d := int64(200), int64(400)
	i.Inbounds[0].Usage = &proto.ObservedUsage{Source: "x_ui_database", Scope: "manager_total", Up: &u, Down: &d, CollectedAt: 1}
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 1, i)); err != nil {
		t.Fatal(err)
	}
	var p proto.ProxyObservation
	json.Unmarshal(frame(t, 2, 0, 1), &p)
	p.ScanComplete = false
	raw, _ := json.Marshal(p)
	if err := s.Receive(ctx, id, "current", raw); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.List(ctx, id, true)
	if len(rows) != 1 || rows[0].Absent || *rows[0].Inbounds[0].Usage.Down != 400 {
		t.Fatal("partial discovery erased known instance")
	}
	var n int
	for _, table := range []string{"subscriber_traffic", "traffic_periods", "config_revisions"} {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("observation changed %s: %d %v", table, n, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.List(ctx, id, true)
	if len(rows) != 0 {
		t.Fatal("deleted node retained observations")
	}
}
