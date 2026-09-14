package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

// ----------------------------------------------------------------------

// pingTaskDTO 是 ping 任务的对外结构。
type pingTaskDTO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Target      string `json:"target"`
	Kind        string `json:"kind"`
	IntervalSec int    `json:"interval_sec"`
	// null 表示作用于全部节点
	ServerIDs []int64 `json:"server_ids"`
	Enabled   bool    `json:"enabled"`
	SortOrder int64   `json:"sort_order"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

func toPingTaskDTO(t *store.PingTask) pingTaskDTO {
	return pingTaskDTO{
		ID: t.ID, Name: t.Name, Target: t.Target, Kind: t.Kind,
		IntervalSec: t.IntervalSec, ServerIDs: t.ServerIDs, Enabled: t.Enabled,
		SortOrder: t.SortOrder, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

// pingTaskRequest 是创建 / 更新的请求体。
type pingTaskRequest struct {
	Name        string  `json:"name"`
	Target      string  `json:"target"`
	Kind        string  `json:"kind"`
	IntervalSec int     `json:"interval_sec"`
	ServerIDs   []int64 `json:"server_ids"`
	Enabled     *bool   `json:"enabled"`
	SortOrder   int64   `json:"sort_order"`
}

// 间隔的取值范围。和 agent 侧的 clampInterval 保持一致，改一边记得改另一边。
const (
	minPingInterval = 10
	maxPingInterval = 3600
)

func (r *pingTaskRequest) toInput() (store.PingTaskInput, error) {
	in := store.PingTaskInput{
		Name:        strings.TrimSpace(r.Name),
		Target:      strings.TrimSpace(r.Target),
		Kind:        strings.TrimSpace(r.Kind),
		IntervalSec: r.IntervalSec,
		ServerIDs:   r.ServerIDs,
		Enabled:     true,
		SortOrder:   r.SortOrder,
	}
	if r.Enabled != nil {
		in.Enabled = *r.Enabled
	}
	if in.Kind == "" {
		in.Kind = "icmp"
	}

	if len(in.ServerIDs) > 1000 {
		return in, errors.New("作用节点最多 1000 台")
	}
	seen := map[int64]bool{}
	for _, id := range in.ServerIDs {
		if id <= 0 || seen[id] {
			return in, errors.New("作用节点 ID 必须为正数且不能重复")
		}
		seen[id] = true
	}
	if in.SortOrder < -1000000 || in.SortOrder > 1000000 {
		return in, errors.New("排序值超出范围")
	}
	if in.Name == "" {
		return in, errors.New("名称不能为空")
	}
	if len([]rune(in.Name)) > 32 {
		return in, errors.New("名称最多 32 个字")
	}
	if in.IntervalSec < minPingInterval || in.IntervalSec > maxPingInterval {
		return in, fmt.Errorf("间隔必须在 %d–%d 秒之间", minPingInterval, maxPingInterval)
	}
	if err := validatePingTarget(in.Kind, in.Target); err != nil {
		return in, err
	}
	return in, nil
}

// validatePingTarget 校验探测目标。
//
// icmp 的目标是 IP 或域名，**不能带端口**——带了也没用，ICMP 没有端口概念，
// 而用户很容易把 `1.2.3.4:80` 填进来然后奇怪为什么一直丢包。
func validatePingTarget(kind, target string) error {
	if target == "" {
		return errors.New("目标不能为空")
	}

	switch kind {
	case "icmp":
		if _, _, err := net.SplitHostPort(target); err == nil {
			return errors.New("ICMP 目标不要带端口，直接填 IP 或域名")
		}
		if !isHostLike(target) {
			return errors.New("目标必须是 IP 或域名")
		}
	case "tcp":
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			return errors.New("TCP 目标必须是 host:port，例如 example.com:443")
		}
		if !isHostLike(host) {
			return errors.New("TCP 目标里的主机部分必须是 IP 或域名")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("端口必须是 1–65535")
		}
	default:
		return errors.New("类型只能是 icmp 或 tcp")
	}
	return nil
}

// isHostLike 粗判一个字符串像不像 IP 或域名。
// 不做严格的域名语法校验——真填错了，探测结果会是丢包，比在这里堵死更直观。
func isHostLike(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------------

// listPingTasks 处理 GET /api/ping-tasks。
func (d *Deps) listPingTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := d.DB.ListPingTasks(r.Context())
	if err != nil {
		serverError(w, "list ping tasks", err)
		return
	}

	out := make([]pingTaskDTO, 0, len(tasks))
	for i := range tasks {
		out = append(out, toPingTaskDTO(&tasks[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// createPingTask 处理 POST /api/ping-tasks。
func (d *Deps) createPingTask(w http.ResponseWriter, r *http.Request) {
	var req pingTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	task, err := d.DB.CreatePingTask(r.Context(), in)
	if err != nil {
		serverError(w, "create ping task", err)
		return
	}

	audit.Record(r.Context(), d.DB, "ping_task.create", "ping_task",
		strconv.FormatInt(task.ID, 10), nil, toPingTaskDTO(task))
	slog.Info("ping task created", "task_id", task.ID, "name", task.Name)

	writeJSON(w, http.StatusCreated, map[string]any{
		"task":   toPingTaskDTO(task),
		"pushed": d.pushPingTasks(r),
	})
}

// updatePingTask 处理 PUT /api/ping-tasks/{id}。
func (d *Deps) updatePingTask(w http.ResponseWriter, r *http.Request) {
	id, ok := d.pingTaskID(w, r)
	if !ok {
		return
	}

	before, err := d.DB.GetPingTask(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFoundPingTask(w)
			return
		}
		serverError(w, "get ping task", err)
		return
	}

	var req pingTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	task, err := d.DB.UpdatePingTask(r.Context(), id, in)
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFoundPingTask(w)
		return
	case err != nil:
		serverError(w, "update ping task", err)
		return
	}

	audit.Record(r.Context(), d.DB, "ping_task.update", "ping_task",
		strconv.FormatInt(id, 10), toPingTaskDTO(before), toPingTaskDTO(task))
	slog.Info("ping task updated", "task_id", id, "name", task.Name)

	writeJSON(w, http.StatusOK, map[string]any{
		"task":   toPingTaskDTO(task),
		"pushed": d.pushPingTasks(r),
	})
}

// deletePingTask 处理 DELETE /api/ping-tasks/{id}。结果靠外键级联删除。
func (d *Deps) deletePingTask(w http.ResponseWriter, r *http.Request) {
	id, ok := d.pingTaskID(w, r)
	if !ok {
		return
	}

	before, err := d.DB.GetPingTask(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFoundPingTask(w)
			return
		}
		serverError(w, "get ping task", err)
		return
	}

	if err := d.DB.DeletePingTask(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFoundPingTask(w)
			return
		}
		serverError(w, "delete ping task", err)
		return
	}

	audit.Record(r.Context(), d.DB, "ping_task.delete", "ping_task",
		strconv.FormatInt(id, 10), toPingTaskDTO(before), nil)
	slog.Info("ping task deleted", "task_id", id, "name", before.Name)

	d.pushPingTasks(r)
	w.WriteHeader(http.StatusNoContent)
}

// pushPingTasks 重新读任务并下发给所有在线 agent，返回已入发送队列的节点数。
//
// 增删改之后都要调：agent 收到新的 config 就会把本地任务对齐过来，不用重连。
func (d *Deps) pushPingTasks(r *http.Request) int {
	if d.Ping == nil {
		return 0
	}
	if err := d.Ping.ReloadTasks(r.Context()); err != nil {
		slog.Error("重新读取 ping 任务失败", "err", err)
		return 0
	}
	return d.Ping.BroadcastConfig(hub.DefaultReportInterval)
}

func (d *Deps) pingTaskID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		notFoundPingTask(w)
		return 0, false
	}
	return id, true
}

func notFoundPingTask(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "任务不存在")
}

// ----------------------------------------------------------------------

// pingRecentResponse 是 GET /api/servers/{id}/ping/recent 的响应。
type pingRecentResponse struct {
	Tasks []pingRecentTask `json:"tasks"`
}

type pingRecentTask struct {
	TaskID  int64             `json:"task_id"`
	Name    string            `json:"name"`
	Results []pingRecentPoint `json:"results"`
}

type pingRecentPoint struct {
	TS      int64    `json:"ts"`
	Latency *float64 `json:"latency"` // null = 丢包
}

// pingRecent 处理 GET /api/servers/{id}/ping/recent?n=30。
//
// 方块序列不进每秒的快照（那是每台每任务 30 个点，广播太浪费），
// 前端单独拉这个接口，一分钟刷一次。
func (d *Deps) pingRecent(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}
	if d.Ping == nil {
		writeJSON(w, http.StatusOK, pingRecentResponse{Tasks: []pingRecentTask{}})
		return
	}

	n := 30
	if raw := r.URL.Query().Get("n"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 30 {
			writeError(w, http.StatusBadRequest, "n 必须是 1–30")
			return
		}
		n = v
	}

	resp := pingRecentResponse{Tasks: []pingRecentTask{}}
	for _, t := range d.Ping.Tasks() {
		if !t.AppliesTo(s.ID) {
			continue
		}

		item := pingRecentTask{TaskID: t.ID, Name: t.Name, Results: []pingRecentPoint{}}
		results, err := d.Ping.Window(r.Context(), s.ID, t.ID, n)
		if err != nil {
			serverError(w, "read recent ping", err)
			return
		}
		for _, res := range results {
			item.Results = append(item.Results, pingRecentPoint{TS: res.TS, Latency: res.LatencyMS})
		}
		resp.Tasks = append(resp.Tasks, item)
	}

	writeJSON(w, http.StatusOK, resp)
}

// ----------------------------------------------------------------------

// pingHistoryRange 是延迟曲线的时间窗与分桶。
type pingHistoryRange struct {
	span   time.Duration
	bucket int64 // 秒
}

// 24h 按分钟原始点，7d 按 10 分钟，30d 按小时——三档的点数都在 1000 上下。
var pingHistoryRanges = map[string]pingHistoryRange{
	"1h":  {span: time.Hour, bucket: 60},
	"24h": {span: 24 * time.Hour, bucket: 60},
	"7d":  {span: 7 * 24 * time.Hour, bucket: 600},
	"30d": {span: 30 * 24 * time.Hour, bucket: 3600},
}

type pingHistoryResponse struct {
	Step   int                `json:"step"`
	From   int64              `json:"from"`
	To     int64              `json:"to"`
	TaskID int64              `json:"task_id"`
	Name   string             `json:"name"`
	Points []pingHistoryPoint `json:"points"`
}

type pingHistoryPoint struct {
	TS int64 `json:"ts"`
	// 桶内成功探测的平均 / 最大延迟；全丢包时为 null
	Avg  *float64 `json:"avg"`
	Max  *float64 `json:"max"`
	Loss float64  `json:"loss"` // 0–100
}

// pingHistory 处理 GET /api/servers/{id}/ping/history?task={id}&range=24h|7d|30d。
func (d *Deps) pingHistory(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}

	taskID, err := strconv.ParseInt(r.URL.Query().Get("task"), 10, 64)
	if err != nil || taskID <= 0 {
		writeError(w, http.StatusBadRequest, "缺少 task 参数")
		return
	}
	task, err := d.DB.GetPingTask(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFoundPingTask(w)
			return
		}
		serverError(w, "get ping task", err)
		return
	}

	key := r.URL.Query().Get("range")
	if key == "" {
		key = "24h"
	}
	rng, ok := pingHistoryRanges[key]
	if !ok {
		writeError(w, http.StatusBadRequest, "range 只能是 1h、24h、7d、30d")
		return
	}

	now := time.Now()
	to := now.Unix()
	from := now.Add(-rng.span).Unix() / rng.bucket * rng.bucket

	buckets, err := d.DB.PingHistory(r.Context(), s.ID, taskID, from, to, rng.bucket)
	if err != nil {
		serverError(w, "ping history", err)
		return
	}

	resp := pingHistoryResponse{
		Step:   int(rng.bucket),
		From:   from,
		To:     to,
		TaskID: taskID,
		Name:   task.Name,
		Points: make([]pingHistoryPoint, 0, len(buckets)),
	}
	for _, b := range buckets {
		resp.Points = append(resp.Points, pingHistoryPoint{
			TS:   b.TS,
			Avg:  roundPtr(b.Avg),
			Max:  roundPtr(b.Max),
			Loss: round2(b.LossPct),
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func roundPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := round2(*v)
	return &r
}
