package proxy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"vpsmon/proto"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/proxy/keys"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/proxy/render"
	"vpsmon/server/internal/store"
)

// Hello precedes the Agent's unsolicited core.state. Wait for that fresh state
// instead of treating the database's previous observation as an acknowledgement.
func (r *Reconciler) OnAgentHello(id int64) {
	n := r.node(id)
	n.op.Lock()
	defer n.op.Unlock()
	n.inflight = nil
	n.attempts = 0
	n.exhausted = false
	n.retryPending = false
	n.awaitState = true
	r.cancelTimer(timerKey("timeout", id))
	r.cancelTimer(timerKey("retry", id))
	r.NodeChanged(id, "hello")
}
func (r *Reconciler) Handle(ctx context.Context, id int64, raw []byte) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return
	}
	var env proto.Envelope
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	var err error
	switch env.Type {
	case proto.TypeCoreState:
		var s proto.CoreState
		err = model.StrictJSON(raw, &s)
		if err == nil {
			err = r.OnCoreState(ctx, id, s)
		}
	case proto.TypeCoreStats:
		var s proto.CoreStats
		err = model.StrictJSON(raw, &s)
		if err == nil {
			err = r.Stats.Ingest(ctx, id, s)
		}
	case proto.TypeCoreLogs:
		var s proto.CoreLogs
		err = model.StrictJSON(raw, &s)
		if err == nil {
			r.OnLogs(id, s)
		}
	}
	if err != nil && ctx.Err() == nil {
		slog.Warn("核心消息未接受", "server_id", id, "type", env.Type, "err", err)
	}
}
func validateState(s proto.CoreState) error {
	if s.Type != proto.TypeCoreState || s.Core != "sing-box" || s.AppliedRevision < 0 || len(s.ReqID) > 128 || len(s.Listening) > 1024 {
		return invalid(fmt.Errorf("core.state 字段不合法"))
	}
	if s.InstalledVersion != "" && !corefiles.ValidVersion(s.InstalledVersion) {
		return invalid(fmt.Errorf("核心版本不合法"))
	}
	if s.ConfigSHA256 != "" {
		h, e := hex.DecodeString(s.ConfigSHA256)
		if e != nil || len(h) != 32 || strings.ToLower(s.ConfigSHA256) != s.ConfigSHA256 {
			return invalid(fmt.Errorf("核心哈希不合法"))
		}
	}
	if s.AppliedRevision > 0 && s.ConfigSHA256 == "" {
		return invalid(fmt.Errorf("已应用修订缺少哈希"))
	}
	if !slices.Contains([]string{"none", "ufw", "firewalld"}, s.Firewall) {
		return invalid(fmt.Errorf("防火墙状态不合法"))
	}
	seen := map[string]bool{}
	for _, p := range s.Listening {
		parts := strings.Split(p, "/")
		if len(parts) != 2 {
			return invalid(fmt.Errorf("监听端口格式不合法"))
		}
		n, e := strconv.Atoi(parts[0])
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != parts[0] || (parts[1] != "tcp" && parts[1] != "udp") || seen[p] {
			return invalid(fmt.Errorf("监听端口格式不合法"))
		}
		seen[p] = true
	}
	if s.Error != nil && len(*s.Error) > 2048 {
		return invalid(fmt.Errorf("核心错误信息过长"))
	}
	return nil
}
func (r *Reconciler) OnCoreState(ctx context.Context, id int64, s proto.CoreState) error {
	if err := validateState(s); err != nil {
		return err
	}
	if s.Listening == nil {
		s.Listening = []string{}
	}
	// A logs failure must only complete its own request, never an apply flight.
	if s.ReqID != "" && s.Error != nil {
		r.deliverLog(id, s.ReqID, logResult{err: errors.New(*s.Error)})
	}
	n := r.node(id)
	n.op.Lock()
	defer n.op.Unlock()
	matched := n.inflight != nil && n.inflight.reqID == s.ReqID
	initial := n.awaitState && s.ReqID == ""
	action := ""
	if matched {
		action = n.inflight.action
		if action == "apply" && s.Error == nil {
			rev, err := r.db.Proxy().Revision(ctx, id, n.inflight.revision)
			if err != nil || s.AppliedRevision != n.inflight.revision || s.ConfigSHA256 != rev.SHA256 || s.InstalledVersion != rev.Version || !s.Running {
				message := "Agent 确认与已发送修订不一致"
				s.Error = &message
			}
		}
	}
	// Ignore stale correlated responses after timeout/reconnect; a periodic state
	// can refresh observations, but cannot acknowledge an in-flight request.
	if s.ReqID != "" && !matched {
		return nil
	}
	aligned := false
	err := r.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		if err := q.ServerExists(ctx, id); err != nil {
			return err
		}
		if s.ReqID == "" && n.inflight == nil && s.Error == nil && s.Running {
			current, err := loadSnapshot(ctx, q, id)
			if err != nil {
				return err
			}
			aligned = current.latest != nil && s.AppliedRevision == current.latest.Revision && s.ConfigSHA256 == current.latest.SHA256 && s.InstalledVersion == current.latest.Version && current.inputHash == inputHash(current.data)
		}
		return q.SaveCoreState(ctx, id, s, r.now().Unix(), matched || initial || aligned || s.Error != nil)
	})
	if err != nil {
		return err
	}
	if initial || matched {
		n.awaitState = false
	}
	if matched {
		n.inflight = nil
		r.cancelTimer(timerKey("timeout", id))
	}
	if matched && s.Error != nil && action == "apply" {
		r.retry(id, n)
	} else if initial || aligned || matched && s.Error == nil {
		n.attempts = 0
		n.exhausted = false
		n.retryPending = false
		r.cancelTimer(timerKey("retry", id))
		rev, err := r.db.Proxy().Revision(ctx, id, 0)
		if err == nil {
			r.sendDesired(ctx, id, n, rev, false)
		}
	}
	// A restored panel database may trail the Agent's durable revision counter.
	c, err := r.db.Proxy().Core(ctx, id)
	if err == nil && s.AppliedRevision > c.DesiredRevision {
		r.NodeChanged(id, "agent-ahead")
	}
	r.publish(ctx, id)
	return nil
}
func (r *Reconciler) Action(ctx context.Context, id int64, action string) (string, error) {
	if action != "install" && action != "restart" {
		return "", invalid(fmt.Errorf("不支持的核心操作"))
	}
	n := r.node(id)
	n.op.Lock()
	defer n.op.Unlock()
	if err := r.db.Proxy().ServerExists(ctx, id); err != nil {
		return "", err
	}
	if !r.connected(id) {
		return "", ErrOffline
	}
	if n.inflight != nil {
		return "", ErrCoreBusy
	}
	reqID := keys.SubToken()
	msg := proto.CoreAction{Type: proto.TypeCoreAction, Action: action, ReqID: reqID}
	if action == "install" {
		var version string
		_, err := r.db.GetSetting(ctx, corefiles.CurrentKey, &version)
		if err != nil {
			return "", err
		}
		if !corefiles.ValidVersion(version) {
			return "", ErrNoCurrentCore
		}
		host, err := r.db.GetHostInfo(ctx, id)
		if err != nil {
			return "", err
		}
		arch := host.Arch
		switch arch {
		case "x86_64":
			arch = "amd64"
		case "aarch64":
			arch = "arm64"
		}
		if !corefiles.ValidArch(arch) {
			return "", invalid(fmt.Errorf("Agent 尚未上报受支持的 amd64/arm64 架构"))
		}
		if r.options.Artifact == nil {
			return "", ErrNoCurrentCore
		}
		artifact, err := r.options.Artifact(ctx, version, arch)
		if err != nil {
			return "", err
		}
		msg.Version = version
		msg.File = "sing-box-linux-" + arch
		msg.SHA256 = artifact.SHA256
	}
	if err := r.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		return r.audit(ctx, q, id, "core."+action, map[string]any{"req_id": reqID, "version": msg.Version, "dispatch": "requested"})
	}); err != nil {
		return "", err
	}
	n.inflight = &flight{reqID: reqID, action: action}
	n.exhausted = false
	if !r.options.Agents.SendTo(id, msg) {
		n.inflight = nil
		r.setError(ctx, id, ErrOffline)
		return "", ErrOffline
	}
	r.armTimeout(id, reqID)
	return reqID, nil
}
func (r *Reconciler) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines < 1 || lines > 1000 {
		return "", invalid(fmt.Errorf("lines 必须为 1–1000"))
	}
	if err := r.db.Proxy().ServerExists(ctx, id); err != nil {
		return "", err
	}
	if !r.connected(id) {
		return "", ErrOffline
	}
	reqID := keys.SubToken()
	w := logWait{serverID: id, ch: make(chan logResult, 1)}
	r.mu.Lock()
	if r.closed || len(r.logs) >= 128 {
		r.mu.Unlock()
		return "", ErrCoreBusy
	}
	r.logs[reqID] = w
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.logs, reqID); r.mu.Unlock() }()
	if !r.options.Agents.SendTo(id, proto.CoreLogsReq{Type: proto.TypeCoreLogs, Kind: "error", Lines: lines, ReqID: reqID}) {
		return "", ErrOffline
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.options.LogTimeout)
	defer cancel()
	select {
	case result := <-w.ch:
		return result.text, result.err
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrCoreTimeout
	}
}
func (r *Reconciler) OnLogs(id int64, s proto.CoreLogs) {
	if s.Type != proto.TypeCoreLogs || s.Kind != "error" || len(s.Text) > 64<<10 || s.ReqID == "" || len(s.ReqID) > 128 {
		return
	}
	r.deliverLog(id, s.ReqID, logResult{text: s.Text})
}
func (r *Reconciler) deliverLog(id int64, reqID string, result logResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.logs[reqID]; ok && w.serverID == id {
		select {
		case w.ch <- result:
		default:
		}
	}
}
func (r *Reconciler) Rollback(ctx context.Context, id, revision int64) (*store.Revision, error) {
	n := r.node(id)
	n.op.Lock()
	defer n.op.Unlock()
	source, err := r.db.Proxy().Revision(ctx, id, revision)
	if err != nil {
		return nil, err
	}
	out, err := render.Inspect(source.ConfigJSON)
	if err != nil || out.SHA256 != source.SHA256 {
		return nil, invalid(fmt.Errorf("历史修订配置或哈希损坏"))
	}
	if err = r.check(ctx, source.Version, source.ConfigJSON); err != nil && !errors.Is(err, render.ErrNoLocalCore) {
		return nil, err
	}
	var result *store.Revision
	err = r.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		s, err := loadSnapshot(ctx, q, id)
		if err != nil {
			return err
		}
		result = &store.Revision{ServerID: id, Revision: max(revisionNumber(s.latest), s.core.AppliedRevision) + 1, ConfigJSON: source.ConfigJSON, SHA256: source.SHA256, Version: source.Version, Ports: out.Ports, CreatedAt: r.now().Unix(), CreatedBy: "rollback:" + strconv.FormatInt(revision, 10)}
		if err = q.SaveRevision(ctx, result, inputHash(s.data)); err != nil {
			return err
		}
		return r.audit(ctx, q, id, "core.rollback", map[string]any{"source_revision": revision, "revision": result.Revision, "sha256": result.SHA256})
	})
	if err != nil {
		return nil, err
	}
	n.attempts = 0
	n.exhausted = false
	n.retryPending = false
	r.cancelTimer(timerKey("retry", id))
	r.cancelTimer(timerKey("render", id))
	r.sendDesired(ctx, id, n, result, true)
	r.publish(ctx, id)
	return result, nil
}
