package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/proxy/keys"
	"vpsmon/server/internal/proxy/render"
	"vpsmon/server/internal/store"
)

var (
	ErrReadOnly      = errors.New("该节点由外部管理，或尚未完成所有权核验；仅允许只读观测，请先升级探针")
	ErrOffline       = errors.New("Agent 离线或发送队列不可用")
	ErrNoCurrentCore = errors.New("请先选择当前托管核心版本")
	ErrCoreBusy      = errors.New("核心操作正在进行，请稍后重试")
	ErrCoreTimeout   = errors.New("等待 Agent 响应超时")
)

type AgentSender interface {
	SendTo(int64, any) bool
	Connected(int64) bool
}
type ReconcilerOptions struct {
	AllowManage  func(int64, bool) bool
	Agents       AgentSender
	Check        func(context.Context, string, []byte) error
	Artifact     func(context.Context, string, string) (corefiles.Artifact, error)
	Publish      func(int64, CoreSummary)
	Failed       func(int64)
	AfterStats   func(context.Context) error
	Debounce     time.Duration
	ApplyTimeout time.Duration
	LogTimeout   time.Duration
	RetryDelays  []time.Duration
}
type CoreSummary struct {
	Installed bool    `json:"installed"`
	Running   bool    `json:"running"`
	Version   string  `json:"version"`
	Users     int     `json:"users"`
	Pending   bool    `json:"pending"`
	Error     *string `json:"error"`
}
type CoreView struct {
	*store.NodeCore
	Online         bool            `json:"online"`
	CurrentVersion string          `json:"current_version"`
	Pending        bool            `json:"pending"`
	Inbounds       []proto.Counter `json:"inbounds"`
}
type flight struct {
	reqID    string
	revision int64
	action   string
}
type nodeWork struct {
	op           sync.Mutex
	inflight     *flight
	attempts     int
	exhausted    bool
	retryPending bool
	awaitState   bool
}
type logWait struct {
	serverID int64
	ch       chan logResult
}
type logResult struct {
	text string
	err  error
}
type Reconciler struct {
	db        *store.DB
	options   ReconcilerOptions
	Stats     *Stats
	ctx       context.Context
	cancel    context.CancelFunc
	now       func() time.Time
	mu        sync.Mutex
	nodes     map[int64]*nodeWork
	timers    map[string]*time.Timer
	logs      map[string]logWait
	summaries map[int64]CoreSummary
	closed    bool
	wg        sync.WaitGroup
}

func NewReconciler(db *store.DB, o ReconcilerOptions) *Reconciler {
	if o.Debounce <= 0 {
		o.Debounce = 5 * time.Second
	}
	if o.ApplyTimeout <= 0 {
		o.ApplyTimeout = 60 * time.Second
	}
	if o.LogTimeout <= 0 {
		o.LogTimeout = 10 * time.Second
	}
	if o.RetryDelays == nil {
		o.RetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Reconciler{db: db, options: o, Stats: NewStats(db), ctx: ctx, cancel: cancel, now: clock.Now, nodes: map[int64]*nodeWork{}, timers: map[string]*time.Timer{}, logs: map[string]logWait{}, summaries: map[int64]CoreSummary{}}
}
func (r *Reconciler) node(id int64) *nodeWork {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nodes[id]
	if n == nil {
		n = &nodeWork{}
		r.nodes[id] = n
	}
	return n
}
func (r *Reconciler) schedule(key string, delay time.Duration, fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if t := r.timers[key]; t != nil {
		t.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(delay, func() {
		r.mu.Lock()
		if r.closed || r.timers[key] != t {
			r.mu.Unlock()
			return
		}
		delete(r.timers, key)
		r.wg.Add(1)
		r.mu.Unlock()
		defer r.wg.Done()
		fn()
	})
	r.timers[key] = t
}
func timerKey(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }
func (r *Reconciler) cancelTimer(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.timers[key]; t != nil {
		t.Stop()
		delete(r.timers, key)
	}
}
func (r *Reconciler) NodeChanged(id int64, reason string) {
	if r.options.AllowManage != nil && !r.options.AllowManage(id, true) {
		return
	}
	r.schedule(timerKey("render", id), r.options.Debounce, func() {
		ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		defer cancel()
		if _, err := r.Reconcile(ctx, id, false); err != nil && !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			slog.Warn("核心对齐未完成", "server_id", id, "reason", reason, "err", err)
		}
	})
}
func (r *Reconciler) Run(ctx context.Context) {
	if r.options.AfterStats != nil {
		if err := r.options.AfterStats(ctx); err != nil {
			slog.Error("启动订阅策略检查失败", "err", err)
		}
	}
	r.refreshAll(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer r.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Stats.Flush(ctx); err != nil {
				slog.Error("代理流量落库失败，保留重试", "err", err)
			} else if r.options.AfterStats != nil {
				if err := r.options.AfterStats(ctx); err != nil {
					slog.Error("订阅策略检查失败", "err", err)
				}
			}
			r.refreshAll(ctx)
		}
	}
}
func (r *Reconciler) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.cancel()
	for _, t := range r.timers {
		t.Stop()
	}
	r.timers = map[string]*time.Timer{}
	for _, w := range r.logs {
		select {
		case w.ch <- logResult{err: context.Canceled}:
		default:
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Stats.Close(ctx); err != nil {
		slog.Error("退出时代理流量落库失败", "err", err)
	}
}
func (r *Reconciler) refreshAll(ctx context.Context) {
	nodes, err := r.db.ListServers(ctx)
	if err != nil {
		return
	}
	for _, n := range nodes {
		r.publish(ctx, n.ID)
		r.NodeChanged(n.ID, "periodic")
	}
}
func (r *Reconciler) CurrentChanged(ctx context.Context) { r.refreshAll(ctx) }
func (r *Reconciler) Forget(id int64) {
	for _, kind := range []string{"render", "retry", "timeout"} {
		r.cancelTimer(timerKey(kind, id))
	}
	r.Stats.Forget(id)
	r.mu.Lock()
	delete(r.summaries, id)
	r.mu.Unlock()
	// Keep the per-node mutex until in-flight callers leave; IDs are never reused.
}

type renderSnapshot struct {
	data      store.RenderData
	core      *store.NodeCore
	latest    *store.Revision
	inputHash string
}

func loadSnapshot(ctx context.Context, q store.ProxyQueries, id int64) (s renderSnapshot, err error) {
	s.data, err = q.RenderData(ctx, id)
	if err != nil {
		return
	}
	s.core, err = q.Core(ctx, id)
	if err != nil {
		return
	}
	s.inputHash, err = q.InputHash(ctx, id)
	if err != nil {
		return
	}
	s.latest, err = q.Revision(ctx, id, 0)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return
}
func (r *Reconciler) snapshot(ctx context.Context, id int64) (renderSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return renderSnapshot{}, err
	}
	defer tx.Rollback()
	s, err := loadSnapshot(ctx, store.ProxyQueries{DB: tx}, id)
	if err == nil {
		err = tx.Commit()
	}
	return s, err
}
func inputHash(d store.RenderData) string {
	// Only configuration inputs; usage changes do not invalidate the snapshot.
	b, _ := json.Marshal(d)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func build(d store.RenderData) (render.Output, error) {
	return render.Render(render.Input{Server: d.Server, Inbounds: d.Inbounds, UsersByInbound: d.UsersByInbound, Cert: d.Cert, Extra: d.Extra, Version: d.Version})
}
func actor(ctx context.Context) string {
	if p, ok := auth.PrincipalFromContext(ctx); ok {
		return p.Name
	}
	return "system"
}
func (r *Reconciler) check(ctx context.Context, version string, config []byte) error {
	if r.options.Check == nil {
		return render.ErrNoLocalCore
	}
	return r.options.Check(ctx, version, config)
}

// Reconcile checks outside the SQLite writer transaction, then compares a fresh
// input snapshot before committing. Concurrent edits cannot publish stale input.
func (r *Reconciler) Reconcile(ctx context.Context, id int64, force bool) (*store.Revision, error) {
	if r.options.AllowManage != nil && !r.options.AllowManage(id, true) {
		return nil, ErrReadOnly
	}
	n := r.node(id)
	n.op.Lock()
	defer n.op.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		s, err := r.snapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		if !corefiles.ValidVersion(s.data.Version) {
			r.setError(ctx, id, ErrNoCurrentCore)
			return nil, ErrNoCurrentCore
		}
		hash := inputHash(s.data)
		if !force && s.latest != nil && s.inputHash == hash && s.core.AppliedRevision <= s.latest.Revision {
			r.sendDesired(ctx, id, n, s.latest, false)
			return s.latest, nil
		}
		out, err := build(s.data)
		if err != nil {
			r.setError(ctx, id, err)
			return nil, invalid(err)
		}
		changed := s.latest == nil || out.SHA256 != s.latest.SHA256 || s.latest.Version != s.data.Version || s.core.AppliedRevision > s.latest.Revision
		if changed {
			err = r.check(ctx, s.data.Version, out.Config)
			if errors.Is(err, render.ErrNoLocalCore) {
				slog.Warn("跳过面板预检，Agent 仍会执行 check", "server_id", id, "version", s.data.Version)
			} else if err != nil {
				r.setError(ctx, id, err)
				return nil, err
			}
		}
		stale := false
		var result *store.Revision
		err = r.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
			current, err := loadSnapshot(ctx, q, id)
			if err != nil {
				return err
			}
			if inputHash(current.data) != hash || revisionNumber(current.latest) != revisionNumber(s.latest) || current.core.AppliedRevision > s.core.AppliedRevision {
				stale = true
				return nil
			}
			if !changed {
				_, err = q.DB.ExecContext(ctx, "UPDATE node_core SET input_sha256=? WHERE server_id=?", hash, id)
				result = current.latest
				if err == nil && force {
					err = r.audit(ctx, q, id, "core.apply", map[string]any{"revision": result.Revision, "sha256": result.SHA256})
				}
				return err
			}
			result = &store.Revision{ServerID: id, Revision: max(revisionNumber(current.latest), current.core.AppliedRevision) + 1, ConfigJSON: out.Config, SHA256: out.SHA256, Version: s.data.Version, Ports: out.Ports, CreatedAt: r.now().Unix(), CreatedBy: actor(ctx)}
			if err = q.SaveRevision(ctx, result, hash); err != nil {
				return err
			}
			if err = r.audit(ctx, q, id, "core.revision", map[string]any{"revision": result.Revision, "sha256": result.SHA256, "version": result.Version}); err != nil {
				return err
			}
			if force {
				return r.audit(ctx, q, id, "core.apply", map[string]any{"revision": result.Revision, "sha256": result.SHA256})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if stale {
			continue
		}
		for _, w := range out.Warnings {
			slog.Warn(w, "server_id", id)
		}
		if changed || force {
			n.attempts = 0
			n.exhausted = false
			n.retryPending = false
			r.cancelTimer(timerKey("retry", id))
		}
		r.sendDesired(ctx, id, n, result, force)
		r.publish(ctx, id)
		return result, nil
	}
	r.NodeChanged(id, "concurrent-edit")
	return nil, ErrCoreBusy
}
func revisionNumber(r *store.Revision) int64 {
	if r == nil {
		return 0
	}
	return r.Revision
}
func pending(c *store.NodeCore, rev *store.Revision) bool {
	return rev != nil && (c.AppliedRevision != rev.Revision || c.ConfigSHA256 == nil || *c.ConfigSHA256 != rev.SHA256 || c.InstalledVersion == nil || *c.InstalledVersion != rev.Version)
}
func (r *Reconciler) connected(id int64) bool {
	return r.options.Agents != nil && r.options.Agents.Connected(id)
}
func (r *Reconciler) sendDesired(ctx context.Context, id int64, n *nodeWork, rev *store.Revision, force bool) {
	if r.options.AllowManage != nil && !r.options.AllowManage(id, false) {
		return
	}
	if rev == nil || n.inflight != nil || n.awaitState || n.exhausted || n.retryPending || !r.connected(id) {
		return
	}
	c, err := r.db.Proxy().Core(ctx, id)
	if err != nil {
		return
	}
	if c.AppliedRevision > rev.Revision {
		r.NodeChanged(id, "agent-ahead")
		return
	}
	if c.InstalledVersion == nil || *c.InstalledVersion != rev.Version {
		r.setError(ctx, id, fmt.Errorf("目标版本 %s 尚未安装，请显式安装核心", rev.Version))
		return
	}
	if !force && !pending(c, rev) {
		return
	}
	reqID := keys.SubToken()
	msg := proto.CoreApply{Type: proto.TypeCoreApply, Core: "sing-box", Revision: rev.Revision, Version: rev.Version, ConfigSHA256: rev.SHA256, Ports: rev.Ports, Config: rev.ConfigJSON, ReqID: reqID}
	n.inflight = &flight{reqID: reqID, revision: rev.Revision, action: "apply"}
	if !r.options.Agents.SendTo(id, msg) {
		n.inflight = nil
		r.setError(ctx, id, ErrOffline)
		return
	}
	r.armTimeout(id, reqID)
}
func (r *Reconciler) armTimeout(id int64, reqID string) {
	r.schedule(timerKey("timeout", id), r.options.ApplyTimeout, func() {
		n := r.node(id)
		n.op.Lock()
		defer n.op.Unlock()
		if n.inflight == nil || n.inflight.reqID != reqID {
			return
		}
		action := n.inflight.action
		n.inflight = nil
		r.setError(r.ctx, id, ErrCoreTimeout)
		if action == "apply" {
			r.retry(id, n)
		}
	})
}
func (r *Reconciler) retry(id int64, n *nodeWork) {
	if r.options.Failed != nil {
		r.options.Failed(id)
	}
	if n.attempts >= len(r.options.RetryDelays) {
		n.exhausted = true
		return
	}
	delay := r.options.RetryDelays[n.attempts]
	n.attempts++
	n.retryPending = true
	r.schedule(timerKey("retry", id), delay, func() {
		n := r.node(id)
		n.op.Lock()
		defer n.op.Unlock()
		n.retryPending = false
		rev, err := r.db.Proxy().Revision(r.ctx, id, 0)
		if err == nil {
			r.sendDesired(r.ctx, id, n, rev, true)
		}
	})
}
func (r *Reconciler) setError(ctx context.Context, id int64, err error) {
	message := err.Error()
	if len(message) > 2048 {
		message = message[:2048]
	}
	if e := r.db.Proxy().CoreError(ctx, id, &message); e != nil {
		slog.Error("记录核心错误失败", "server_id", id, "err", e)
	}
	r.publish(ctx, id)
}
func (r *Reconciler) View(ctx context.Context, id int64) (CoreView, error) {
	c, err := r.db.Proxy().Core(ctx, id)
	if err != nil {
		return CoreView{}, err
	}
	rev, err := r.db.Proxy().Revision(ctx, id, 0)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return CoreView{}, err
	}
	var version string
	_, err = r.db.GetSetting(ctx, corefiles.CurrentKey, &version)
	if err != nil {
		return CoreView{}, err
	}
	return CoreView{NodeCore: c, Online: r.connected(id), CurrentVersion: version, Pending: pending(c, rev), Inbounds: r.Stats.Inbounds(id)}, nil
}
func (r *Reconciler) publish(ctx context.Context, id int64) {
	v, err := r.View(ctx, id)
	if err != nil {
		return
	}
	s := CoreSummary{Installed: v.InstalledVersion != nil, Running: v.Running, Pending: v.Pending, Error: v.LastError}
	if v.InstalledVersion != nil {
		s.Version = *v.InstalledVersion
	}
	if rev, err := r.db.Proxy().Revision(ctx, id, 0); err == nil {
		if info, err := render.Inspect(rev.ConfigJSON); err == nil {
			s.Users = len(info.UserNames)
		}
	}
	r.mu.Lock()
	r.summaries[id] = s
	r.mu.Unlock()
	if r.options.Publish != nil {
		r.options.Publish(id, s)
	}
}
func (r *Reconciler) SnapshotFor(id int64) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summaries[id]
}
func (r *Reconciler) audit(ctx context.Context, q store.ProxyQueries, id int64, action string, payload any) error {
	// Only operation metadata is supplied here, never config or log contents.
	return New(r.db, nil).record(ctx, q, action, "server", id, nil, payload)
}
