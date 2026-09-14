// Package ping handles task delivery, recent windows and batched persistence.
package ping

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

const (
	WindowSize           = 30
	flushInterval        = 5 * time.Second
	DefaultRetentionDays = 30
	SettingRetentionDays = "retention.ping_days"
	// 数据库长时间不可用时最多保留十万条待写入结果，避免无限占用内存。
	maxBufferedResults = 100000
)

type Store interface {
	ListPingTasks(context.Context) ([]store.PingTask, error)
	ListServers(context.Context) ([]store.Server, error)
	InsertPingResults(context.Context, []store.PingResult) error
	RecentPingResults(context.Context, int64, int64, int) ([]store.PingResult, error)
	DeletePingResultsBefore(context.Context, int64) (int64, error)
	GetSetting(context.Context, string, any) (bool, error)
}
type agentHub interface{ Broadcast(func(int64) any) int }
type sample struct {
	ts      int64
	latency *float64
}
type windowKey struct{ server, task int64 }

type Service struct {
	db  Store
	hub agentHub
	now func() time.Time
	// reloadMu 串行化读取任务，防止较早的查询覆盖较新的缓存。
	reloadMu sync.Mutex
	flushMu  sync.Mutex
	mu       sync.Mutex
	tasks    []store.PingTask
	window   map[windowKey][]sample
	loaded   map[windowKey]bool
	buffer   []store.PingResult
	stopped  bool
}

func New(db Store, h agentHub) *Service {
	return &Service{db: db, hub: h, now: time.Now, window: map[windowKey][]sample{}, loaded: map[windowKey]bool{}}
}

func (s *Service) ReloadTasks(ctx context.Context) error {
	_, err := s.reloadTasks(ctx)
	return err
}
func (s *Service) reloadTasks(ctx context.Context) (bool, error) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	tasks, err := s.db.ListPingTasks(ctx)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !reflect.DeepEqual(s.tasks, tasks)
	s.tasks = tasks
	exists := map[int64]bool{}
	for _, t := range tasks {
		exists[t.ID] = true
	}
	for k := range s.window {
		if !exists[k.task] {
			delete(s.window, k)
			delete(s.loaded, k)
		}
	}
	kept := s.buffer[:0]
	for _, r := range s.buffer {
		if exists[r.TaskID] {
			kept = append(kept, r)
		}
	}
	s.buffer = kept
	return changed, nil
}

func (s *Service) ConfigFor(serverID int64) []proto.PingTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]proto.PingTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.AppliesTo(serverID) {
			out = append(out, proto.PingTask{ID: t.ID, Name: t.Name, Target: t.Target, Kind: t.Kind, Interval: t.IntervalSec})
		}
	}
	return out
}
func (s *Service) BuildConfig(serverID int64, reportInterval int) proto.Config {
	return proto.Config{Type: proto.TypeConfig, ReportInterval: reportInterval, PingTasks: s.ConfigFor(serverID)}
}

// 返回已入发送队列的节点数，不代表 agent 已确认执行。
func (s *Service) BroadcastConfig(reportInterval int) int {
	if s.hub == nil {
		return 0
	}
	return s.hub.Broadcast(func(id int64) any { return s.BuildConfig(id, reportInterval) })
}
func (s *Service) allowedLocked(serverID, taskID int64) bool {
	for _, t := range s.tasks {
		if t.ID == taskID {
			return t.AppliesTo(serverID)
		}
	}
	return false
}

// Warm 在开放 WS 接入前恢复全部节点的窗口，使第一帧快照也包含历史数据。
func (s *Service) Warm(ctx context.Context) error {
	servers, err := s.db.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, server := range servers {
		for _, task := range s.ConfigFor(server.ID) {
			if _, err := s.Window(ctx, server.ID, task.ID, WindowSize); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) OnPing(serverID int64, raw []byte) {
	var r proto.PingResult
	if err := json.Unmarshal(raw, &r); err != nil {
		slog.Warn("ping 结果格式无效", "server_id", serverID)
		return
	}
	if r.TaskID <= 0 || (r.LatencyMS != nil && (*r.LatencyMS < 0 || math.IsInf(*r.LatencyMS, 0) || math.IsNaN(*r.LatencyMS))) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || !s.allowedLocked(serverID, r.TaskID) {
		return
	}
	key := windowKey{serverID, r.TaskID}
	// 与数据库主键一致，同一秒的重复结果只算一次。
	ts := s.now().Unix()
	w := s.window[key]
	if len(w) > 0 && w[len(w)-1].ts == ts {
		w[len(w)-1].latency = r.LatencyMS
	} else {
		w = append(w, sample{ts, r.LatencyMS})
	}
	if len(w) > WindowSize {
		w = append([]sample(nil), w[len(w)-WindowSize:]...)
	}
	s.window[key] = w
	s.buffer = append(s.buffer, store.PingResult{ServerID: serverID, TaskID: r.TaskID, TS: ts, LatencyMS: r.LatencyMS})
	s.limitBufferLocked()
}
func (s *Service) limitBufferLocked() {
	if n := len(s.buffer) - maxBufferedResults; n > 0 {
		slog.Error("ping 待写入缓冲已满，丢弃最老结果", "dropped", n)
		s.buffer = append([]store.PingResult(nil), s.buffer[n:]...)
	}
}

func (s *Service) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	batch := s.buffer
	s.buffer = nil
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := s.db.InsertPingResults(ctx, batch); err != nil {
		s.mu.Lock()
		// 失败批次放回新数据之前；删除任务的结果在存储层跳过。
		s.buffer = append(batch, s.buffer...)
		s.limitBufferLocked()
		s.mu.Unlock()
		return err
	}
	slog.Debug("ping 结果已落库", "rows", len(batch))
	return nil
}
func (s *Service) Run(ctx context.Context) {
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	if err := s.Cleanup(ctx); err != nil {
		slog.Error("清理过期 ping 结果失败", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.stopped = true
			s.mu.Unlock()
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Flush(flushCtx); err != nil {
				slog.Error("关停时落库 ping 结果失败", "err", err)
			}
			return
		case <-flush.C:
			if changed, err := s.reloadTasks(ctx); err != nil {
				slog.Error("刷新 ping 任务失败，将重试", "err", err)
			} else if changed {
				s.BroadcastConfig(hub.DefaultReportInterval)
			}
			if err := s.Flush(ctx); err != nil {
				slog.Error("ping 结果落库失败，将重试", "err", err)
			}
		case <-cleanup.C:
			if err := s.Cleanup(ctx); err != nil {
				slog.Error("清理过期 ping 结果失败", "err", err)
			}
		}
	}
}
func (s *Service) Cleanup(ctx context.Context) error {
	days := DefaultRetentionDays
	var configured int
	found, err := s.db.GetSetting(ctx, SettingRetentionDays, &configured)
	if err != nil {
		return err
	}
	if found && configured >= 1 && configured <= 3650 {
		days = configured
	}
	_, err = s.db.DeletePingResultsBefore(ctx, s.now().Unix()-int64(days)*86400)
	return err
}
func (s *Service) SnapshotFor(serverID int64) []hub.PingView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hub.PingView, 0, len(s.tasks))
	for _, t := range s.tasks {
		if !t.AppliesTo(serverID) {
			continue
		}
		view := hub.PingView{TaskID: t.ID, Name: t.Name}
		if w := s.window[windowKey{serverID, t.ID}]; len(w) > 0 {
			last := w[len(w)-1]
			view.Latency = last.latency
			view.Loss = lossPercent(w)
			view.LastTS = &last.ts
		}
		out = append(out, view)
	}
	return out
}

// Window 每个节点/任务首次读取时加载完整 30 点，与先到的新结果按时间合并。
// n 只裁剪响应，不能决定缓存大小；查询失败保留未加载标记，下一次仍可重试。
func (s *Service) Window(ctx context.Context, serverID, taskID int64, n int) ([]store.PingResult, error) {
	if n < 1 || n > WindowSize {
		n = WindowSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := windowKey{serverID, taskID}
	if !s.loaded[key] {
		rows, err := s.db.RecentPingResults(ctx, serverID, taskID, WindowSize)
		if err != nil {
			return nil, err
		}
		merged := map[int64]*float64{}
		for _, r := range rows {
			merged[r.TS] = r.LatencyMS
		}
		for _, r := range s.window[key] {
			merged[r.ts] = r.latency
		}
		w := make([]sample, 0, len(merged))
		for ts, latency := range merged {
			w = append(w, sample{ts, latency})
		}
		sort.Slice(w, func(i, j int) bool { return w[i].ts < w[j].ts })
		if len(w) > WindowSize {
			w = w[len(w)-WindowSize:]
		}
		s.window[key] = w
		s.loaded[key] = true
	}
	w := s.window[key]
	if len(w) > n {
		w = w[len(w)-n:]
	}
	out := make([]store.PingResult, 0, len(w))
	for _, v := range w {
		out = append(out, store.PingResult{ServerID: serverID, TaskID: taskID, TS: v.ts, LatencyMS: v.latency})
	}
	return out, nil
}
func (s *Service) Tasks() []store.PingTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := append([]store.PingTask(nil), s.tasks...)
	for i := range tasks {
		if tasks[i].ServerIDs != nil {
			tasks[i].ServerIDs = append([]int64{}, tasks[i].ServerIDs...)
		}
	}
	return tasks
}
func (s *Service) Forget(serverID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.window {
		if key.server == serverID {
			delete(s.window, key)
			delete(s.loaded, key)
		}
	}
	kept := s.buffer[:0]
	for _, r := range s.buffer {
		if r.ServerID != serverID {
			kept = append(kept, r)
		}
	}
	s.buffer = kept
}
func lossPercent(w []sample) float64 {
	if len(w) == 0 {
		return 0
	}
	lost := 0
	for _, v := range w {
		if v.latency == nil {
			lost++
		}
	}
	return float64(lost) * 100 / float64(len(w))
}
