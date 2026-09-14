package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/proxy/render"
	"vpsmon/server/internal/store"
)

type sentCore struct {
	id  int64
	msg any
}
type fakeAgents struct {
	online   atomic.Bool
	messages chan sentCore
}

func (f *fakeAgents) Connected(int64) bool { return f.online.Load() }
func (f *fakeAgents) SendTo(id int64, m any) bool {
	if !f.Connected(id) {
		return false
	}
	f.messages <- sentCore{id, m}
	return true
}
func nextMessage(t *testing.T, f *fakeAgents) any {
	t.Helper()
	select {
	case m := <-f.messages:
		return m.msg
	case <-time.After(2 * time.Second):
		t.Fatal("missing core message")
		return nil
	}
}
func noMessage(t *testing.T, f *fakeAgents) {
	t.Helper()
	select {
	case m := <-f.messages:
		t.Fatalf("unexpected message %T", m.msg)
	case <-time.After(25 * time.Millisecond):
	}
}
func ack(t *testing.T, r *Reconciler, id int64, a proto.CoreApply, err *string) {
	t.Helper()
	if e := r.OnCoreState(context.Background(), id, proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: a.Version, AppliedRevision: a.Revision, ConfigSHA256: a.ConfigSHA256, Running: true, Listening: a.Ports, Firewall: "none", ReqID: a.ReqID, Error: err}); e != nil {
		t.Fatal(e)
	}
}
func reconcileSetup(t *testing.T) (*Reconciler, *Service, *store.DB, *fakeAgents, int64, *store.Inbound, *store.Subscriber) {
	t.Helper()
	s, db, _, nodes := setup(t)
	id := nodes[0]
	i := makeInbound(t, s, id, "shadowsocks", 24443)
	sub := makeSubscriber(t, s)
	if _, err := s.Assign(context.Background(), sub.ID, []int64{i.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(context.Background(), corefiles.CurrentKey, "v1.14.0"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertHostInfo(context.Background(), store.HostInfo{ServerID: id, Arch: "amd64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE node_core SET installed_version='v1.14.0' WHERE server_id=?", id); err != nil {
		t.Fatal(err)
	}
	f := &fakeAgents{messages: make(chan sentCore, 100)}
	r := NewReconciler(db, ReconcilerOptions{Agents: f, Check: func(context.Context, string, []byte) error { return nil }, Artifact: func(context.Context, string, string) (corefiles.Artifact, error) {
		return corefiles.Artifact{Arch: "amd64", SHA256: strings.Repeat("a", 64)}, nil
	}, Debounce: 60 * time.Millisecond, ApplyTimeout: time.Second, LogTimeout: 80 * time.Millisecond, RetryDelays: []time.Duration{40 * time.Millisecond, 50 * time.Millisecond, 60 * time.Millisecond}})
	t.Cleanup(r.Close)
	return r, s, db, f, id, i, sub
}
func TestReconcileOfflineAcknowledgementAndNewerRevision(t *testing.T) {
	r, s, db, f, id, i, _ := reconcileSetup(t)
	ctx := context.Background()
	first, err := r.Reconcile(ctx, id, false)
	if err != nil || first.Revision != 1 {
		t.Fatal(first, err)
	}
	noMessage(t, f)
	same, err := r.Reconcile(ctx, id, false)
	if err != nil || same.Revision != 1 {
		t.Fatal(same, err)
	}
	f.online.Store(true)
	r.OnAgentHello(id)
	noMessage(t, f)
	if err = r.OnCoreState(ctx, id, proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: "v1.14.0", Firewall: "none"}); err != nil {
		t.Fatal(err)
	}
	a := nextMessage(t, f).(proto.CoreApply)
	if a.ConfigSHA256 != first.SHA256 || a.Revision != 1 {
		t.Fatal("wrong desired payload")
	}
	if err = r.OnCoreState(ctx, id, proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: "v1.14.0", Firewall: "none"}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Reconcile(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	noMessage(t, f)
	if _, err = s.SaveInbound(ctx, 0, i.ID, InboundInput{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	second, err := r.Reconcile(ctx, id, false)
	if err != nil || second.Revision != 2 {
		t.Fatal(second, err)
	}
	noMessage(t, f)
	ack(t, r, id, a, nil)
	a = nextMessage(t, f).(proto.CoreApply)
	if a.Revision != 2 || len(a.Ports) != 0 {
		t.Fatal("did not send latest after older ack")
	}
	ack(t, r, id, a, nil)
	view, err := r.View(ctx, id)
	if err != nil || view.Pending || !view.Running {
		t.Fatal(view, err)
	}
	if _, err = r.Reconcile(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	forced := nextMessage(t, f).(proto.CoreApply)
	if forced.Revision != 2 {
		t.Fatal("resend created revision")
	}
	ack(t, r, id, forced, nil)
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM config_revisions").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
func TestReconcileDebounceAndPrecheckCAS(t *testing.T) {
	r, s, db, f, id, i, _ := reconcileSetup(t)
	ctx := context.Background()
	var checks atomic.Int32
	r.options.Check = func(context.Context, string, []byte) error { checks.Add(1); return nil }
	s.notifier = r
	for _, p := range []int{24444, 24445, 24446} {
		if _, err := s.SaveInbound(ctx, 0, i.ID, InboundInput{ListenPort: &p}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(2 * time.Second)
	for {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM config_revisions").Scan(&n)
		if n == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("debounced render never completed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if checks.Load() != 1 {
		t.Fatal("multiple renders for rapid edits", checks.Load())
	}
	noMessage(t, f)
	// A write during check must not be blocked by a writer transaction and must
	// invalidate the checked snapshot before any revision is committed.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.options.Check = func(context.Context, string, []byte) error { once.Do(func() { close(entered); <-release }); return nil }
	s.notifier = NoopNotifier{}
	if _, err := s.SaveInbound(ctx, 0, i.ID, InboundInput{ListenPort: ptr(25000)}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.Reconcile(ctx, id, false); done <- err }()
	<-entered
	if _, err := s.SaveInbound(ctx, 0, i.ID, InboundInput{ListenPort: ptr(25001)}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	latest, err := db.Proxy().Revision(ctx, id, 0)
	if err != nil || latest.Revision != 2 || !strings.Contains(string(latest.ConfigJSON), "25001") {
		t.Fatal("stale precheck committed", err)
	}
}
func TestReconcilePrecheckAndAuditFailures(t *testing.T) {
	r, _, db, _, id, _, _ := reconcileSetup(t)
	ctx := context.Background()
	r.options.Check = func(context.Context, string, []byte) error { return render.ErrCheck }
	if _, err := r.Reconcile(ctx, id, false); !errors.Is(err, render.ErrCheck) {
		t.Fatal(err)
	}
	if _, err := db.Proxy().Revision(ctx, id, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("invalid revision saved")
	}
	r.options.Check = func(context.Context, string, []byte) error { return nil }
	if _, err := db.Exec(`CREATE TRIGGER reject_core_audit BEFORE INSERT ON audit_log WHEN NEW.action='core.revision' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, id, false); err == nil {
		t.Fatal("audit failure accepted")
	}
	c, err := db.Proxy().Core(ctx, id)
	if err != nil || c.DesiredRevision != 0 {
		t.Fatal("partial desired revision")
	}
}
func TestReconcileRetryCorrelationAndExhaustion(t *testing.T) {
	r, _, _, f, id, _, _ := reconcileSetup(t)
	ctx := context.Background()
	f.online.Store(true)
	if _, err := r.Reconcile(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	a := nextMessage(t, f).(proto.CoreApply)
	wrong := a
	wrong.ReqID = "unrelated"
	ack(t, r, id, wrong, ptr("ignore"))
	noMessage(t, f)
	for attempt := 0; attempt < 4; attempt++ {
		ack(t, r, id, a, ptr("check failed"))
		if _, err := r.Reconcile(ctx, id, false); err != nil {
			t.Fatal(err)
		}
		if attempt < 3 {
			next := nextMessage(t, f).(proto.CoreApply)
			if next.Revision != a.Revision || next.ReqID == a.ReqID {
				t.Fatal("retry payload or correlation wrong")
			}
			a = next
		}
	}
	noMessage(t, f)
	n := r.node(id)
	n.op.Lock()
	exhausted := n.exhausted
	n.op.Unlock()
	if !exhausted {
		t.Fatal("unbounded retries")
	}
	if _, err := r.Reconcile(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	a = nextMessage(t, f).(proto.CoreApply)
	ack(t, r, id, a, nil)
}
func TestCoreVersionRollbackRetentionAndLogs(t *testing.T) {
	r, s, db, f, id, i, _ := reconcileSetup(t)
	ctx := context.Background()
	first, err := r.Reconcile(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	for p := 25000; p < 25022; p++ {
		if _, err = s.SaveInbound(ctx, 0, i.ID, InboundInput{ListenPort: &p}); err != nil {
			t.Fatal(err)
		}
		if _, err = r.Reconcile(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	revisions, err := db.Proxy().Revisions(ctx, id)
	if err != nil || len(revisions) != 20 || revisions[0].Revision != 23 || revisions[19].Revision != 4 {
		t.Fatal("retention", err, len(revisions))
	}
	source, err := db.Proxy().Revision(ctx, id, 4)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := r.Rollback(ctx, id, 4)
	if err != nil || rolled.Revision != 24 || rolled.SHA256 != source.SHA256 {
		t.Fatal("rollback", err)
	}
	again, err := r.Reconcile(ctx, id, false)
	if err != nil || again.Revision != rolled.Revision {
		t.Fatal("periodic render undid rollback", err)
	}
	if _, err = r.Rollback(ctx, id, first.Revision); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("pruned revision rollback accepted")
	}
	if err = db.SetSetting(ctx, corefiles.CurrentKey, "v1.14.1"); err != nil {
		t.Fatal(err)
	}
	f.online.Store(true)
	newer, err := r.Reconcile(ctx, id, false)
	if err != nil || newer.Version != "v1.14.1" {
		t.Fatal(err)
	}
	noMessage(t, f)
	req, err := r.Action(ctx, id, "install")
	if err != nil {
		t.Fatal(err)
	}
	action := nextMessage(t, f).(proto.CoreAction)
	if action.ReqID != req || action.File != "sing-box-linux-amd64" || action.Version != "v1.14.1" {
		t.Fatal("wrong install")
	}
	if err = r.OnCoreState(ctx, id, proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: "v1.14.1", Firewall: "none", ReqID: req}); err != nil {
		t.Fatal(err)
	}
	a := nextMessage(t, f).(proto.CoreApply)
	ack(t, r, id, a, nil)
	done := make(chan error, 1)
	go func() {
		text, err := r.Logs(ctx, id, 200)
		if text != "line\n" && err == nil {
			err = errors.New("logs text mismatch")
		}
		done <- err
	}()
	logReq := nextMessage(t, f).(proto.CoreLogsReq)
	r.OnLogs(id+1, proto.CoreLogs{Type: proto.TypeCoreLogs, Kind: "error", ReqID: logReq.ReqID, Text: "wrong"})
	r.OnLogs(id, proto.CoreLogs{Type: proto.TypeCoreLogs, Kind: "error", ReqID: logReq.ReqID, Text: "line\n"})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { _, err := r.Logs(ctx, id, 200); done <- err }()
	nextMessage(t, f)
	if err := <-done; !errors.Is(err, ErrCoreTimeout) {
		t.Fatal(err)
	}
	f.online.Store(false)
	if _, err := r.Logs(ctx, id, 200); !errors.Is(err, ErrOffline) {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r.SnapshotFor(id))
	if strings.Contains(string(b), "password") || !strings.Contains(string(b), `"installed":true`) {
		t.Fatal("invalid snapshot")
	}
}

func TestApplyTimeoutAndRestoredPanelRevision(t *testing.T) {
	r, _, db, f, id, _, _ := reconcileSetup(t)
	ctx := context.Background()
	r.options.ApplyTimeout = 150 * time.Millisecond
	f.online.Store(true)
	if _, err := r.Reconcile(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	first := nextMessage(t, f).(proto.CoreApply)
	retry := nextMessage(t, f).(proto.CoreApply)
	if retry.Revision != first.Revision || retry.ReqID == first.ReqID {
		t.Fatal("timeout retry correlation")
	}
	ack(t, r, id, first, nil) // An obsolete correlated ACK cannot clear the new flight.
	n := r.node(id)
	n.op.Lock()
	active := n.inflight != nil && n.inflight.reqID == retry.ReqID
	n.op.Unlock()
	if !active {
		t.Fatal("late ACK cleared current flight")
	}
	ack(t, r, id, retry, nil)
	f.online.Store(false)
	r.OnAgentHello(id)
	f.online.Store(true)
	if err := r.OnCoreState(ctx, id, proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: "v1.14.0", Running: true, AppliedRevision: 10, ConfigSHA256: strings.Repeat("c", 64), Firewall: "none"}); err != nil {
		t.Fatal(err)
	}
	ahead := nextMessage(t, f).(proto.CoreApply)
	if ahead.Revision != 11 {
		t.Fatal("sent older revision after panel restore", ahead.Revision)
	}
	ack(t, r, id, ahead, nil)
	if _, err := r.Reconcile(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	a := nextMessage(t, f).(proto.CoreApply)
	ack(t, r, id, a, nil)
	entries, err := db.ListAudit(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		found = found || e.Action == "core.apply"
	}
	if !found {
		t.Fatal("resend action missing audit")
	}
}
