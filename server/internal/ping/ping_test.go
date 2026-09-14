package ping

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

func fixture(t *testing.T) (*Service, *store.DB, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ping.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	server, err := db.CreateServer(ctx, store.ServerInput{Name: "node", Currency: "CNY", BillingCycle: "month", TrafficResetDay: 1, TrafficMode: "sum"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, nil)
	if err = s.ReloadTasks(ctx); err != nil {
		t.Fatal(err)
	}
	return s, db, server.ID
}
func report(t *testing.T, s *Service, server, task int64, latency *float64) {
	t.Helper()
	raw, err := json.Marshal(proto.PingResult{Type: proto.TypePing, TaskID: task, LatencyMS: latency})
	if err != nil {
		t.Fatal(err)
	}
	s.OnPing(server, raw)
}
func TestWindowMergesHistoryAfterLiveResultAndSmallRead(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	ms := 12.0
	rows := []store.PingResult{}
	for i := int64(1); i <= 35; i++ {
		var latency *float64
		if i%2 == 0 {
			latency = &ms
		}
		rows = append(rows, store.PingResult{ServerID: id, TaskID: 1, TS: i, LatencyMS: latency})
	}
	if err := db.InsertPingResults(ctx, rows); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(36, 0) }
	report(t, s, id, 1, &ms)
	small, err := s.Window(ctx, id, 1, 1)
	if err != nil || len(small) != 1 || small[0].TS != 36 {
		t.Fatalf("small=%v err=%v", small, err)
	}
	all, err := s.Window(ctx, id, 1, 30)
	if err != nil || len(all) != 30 || all[0].TS != 7 || all[29].TS != 36 {
		t.Fatalf("all=%v err=%v", all, err)
	}
	view := s.SnapshotFor(id)[0]
	if view.Loss != 50 || view.LastTS == nil || *view.LastTS != 36 {
		t.Fatalf("view=%+v", view)
	}
	// 同秒替换不增加窗口样本数。
	report(t, s, id, 1, nil)
	all, _ = s.Window(ctx, id, 1, 30)
	if len(all) != 30 || all[29].LatencyMS != nil {
		t.Fatalf("duplicate=%v", all)
	}
}
func TestWarmRestoresFirstSnapshotAndScope(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	ms := 8.0
	if err := db.InsertPingResults(ctx, []store.PingResult{{ServerID: id, TaskID: 1, TS: 10, LatencyMS: &ms}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	if v := s.SnapshotFor(id)[0]; v.Latency == nil || *v.Latency != 8 {
		t.Fatalf("warm=%+v", v)
	}
	task, _ := db.GetPingTask(ctx, 1)
	_, err := db.UpdatePingTask(ctx, 1, store.PingTaskInput{Name: task.Name, Target: task.Target, Kind: task.Kind, IntervalSec: 60, Enabled: true, ServerIDs: []int64{999}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReloadTasks(ctx); err != nil {
		t.Fatal(err)
	}
	report(t, s, id, 1, &ms)
	report(t, s, id, 999, &ms)
	if len(s.buffer) != 0 || len(s.ConfigFor(id)) != 2 {
		t.Fatal("out of scope report accepted")
	}
	if _, err = db.UpdatePingTask(ctx, 2, store.PingTaskInput{Name: "disabled", Target: "127.0.0.1", Kind: "icmp", IntervalSec: 60, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	s.ReloadTasks(ctx)
	report(t, s, id, 2, &ms)
	if len(s.buffer) != 0 || len(s.ConfigFor(id)) != 1 {
		t.Fatal("disabled report accepted")
	}
}

type flakyStore struct {
	*store.DB
	once   func()
	failed bool
}

func (f *flakyStore) InsertPingResults(ctx context.Context, rows []store.PingResult) error {
	if !f.failed {
		f.failed = true
		f.once()
		return errors.New("temporary write failure")
	}
	return f.DB.InsertPingResults(ctx, rows)
}
func TestFlushRetriesWithoutLosingConcurrentResults(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	ms := 12.0
	s.now = func() time.Time { return time.Unix(1000, 0) }
	fault := &flakyStore{DB: db, once: func() { report(t, s, id, 2, &ms) }}
	s.db = fault
	report(t, s, id, 1, &ms)
	if s.Flush(ctx) == nil {
		t.Fatal("expected write failure")
	}
	if len(s.buffer) != 2 {
		t.Fatalf("buffer=%d", len(s.buffer))
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM ping_results").Scan(&count)
	if count != 2 || len(s.buffer) != 0 {
		t.Fatalf("rows=%d pending=%d", count, len(s.buffer))
	}
}
func TestFlushSkipsDeletedTaskAndNodeWithoutPoisoningBatch(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	ms := 1.0
	report(t, s, id, 1, &ms)
	report(t, s, id, 2, &ms)
	if err := db.DeletePingTask(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM ping_results WHERE task_id=2").Scan(&count)
	if count != 1 {
		t.Fatal("valid result lost with deleted task")
	}
	report(t, s, id, 2, &ms)
	db.DeleteServer(ctx, id)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestRunFlushesOnShutdownAndRejectsLaterReports(t *testing.T) {
	s, db, id := fixture(t)
	ms := 1.0
	report(t, s, id, 1, &ms)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx)
	rows, err := db.RecentPingResults(context.Background(), id, 1, 30)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	report(t, s, id, 2, &ms)
	if len(s.buffer) != 0 {
		t.Fatal("accepted after shutdown")
	}
}
func TestCleanupRetentionBoundary(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	now := int64(40 * 86400)
	s.now = func() time.Time { return time.Unix(now, 0) }
	cutoff := now - 30*86400
	if err := db.InsertPingResults(ctx, []store.PingResult{{ServerID: id, TaskID: 1, TS: cutoff - 1}, {ServerID: id, TaskID: 1, TS: cutoff}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.RecentPingResults(ctx, id, 1, 30)
	if len(rows) != 1 || rows[0].TS != cutoff {
		t.Fatalf("rows=%v", rows)
	}
}

type configRecorder struct {
	sent     chan proto.Config
	serverID int64
}

func (h *configRecorder) Broadcast(build func(int64) any) int {
	h.sent <- build(h.serverID).(proto.Config)
	return 1
}
func TestRunFlushesAtFiveSecondsAndRefreshesChangedTasks(t *testing.T) {
	s, db, id := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &configRecorder{sent: make(chan proto.Config, 1), serverID: id}
	s.hub = recorder
	ms := 10.0
	report(t, s, id, 1, &ms)
	rows, _ := db.RecentPingResults(ctx, id, 1, 30)
	if len(rows) != 0 {
		t.Fatal("result written before flush")
	}
	task, _ := db.GetPingTask(ctx, 1)
	if _, err := db.UpdatePingTask(ctx, 1, store.PingTaskInput{Name: task.Name, Target: task.Target, Kind: task.Kind, IntervalSec: 60, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case cfg := <-recorder.sent:
		if len(cfg.PingTasks) != 2 {
			t.Fatalf("config=%+v", cfg)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("task reload timer did not run")
	}
	deadline := time.Now().Add(time.Second)
	for {
		rows, err := db.RecentPingResults(ctx, id, 1, 30)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("flush timer did not persist result")
		}
		time.Sleep(time.Millisecond)
	}
}
