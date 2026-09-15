package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/store"
	"vpsmon/server/internal/traffic"
)

// serverConfigDTO 是节点的持久化配置。字段名用蛇形，与设计方案 §7.3 的快照结构一致，
// 步骤 06 的前端可以用同一套类型。（auth 的 user 对象是 camelCase，那是为了对齐前端 starter。）
type serverConfigDTO struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Region           string   `json:"region"`
	GroupName        string   `json:"group_name"`
	Tags             []string `json:"tags"`
	SortOrder        int64    `json:"sort_order"`
	PublicHost       string   `json:"public_host"`
	Price            float64  `json:"price"`
	Currency         string   `json:"currency"`
	BillingCycle     string   `json:"billing_cycle"`
	ExpireAt         *string  `json:"expire_at"`
	AutoRenew        bool     `json:"auto_renew"`
	TrafficLimit     int64    `json:"traffic_limit"`
	TrafficResetDay  int      `json:"traffic_reset_day"`
	TrafficResetMode string   `json:"traffic_reset_mode"`
	TrafficMode      string   `json:"traffic_mode"`
	BandwidthLabel   string   `json:"bandwidth_label"`
	Note             string   `json:"note"`
	CreatedAt        int64    `json:"created_at"`
	UpdatedAt        int64    `json:"updated_at"`
}

// serverDTO 是配置加上实时状态。online / last_seen 来自 hub 的内存态（步骤 05 起）。
type serverDTO struct {
	serverConfigDTO
	Online                bool     `json:"online"`
	LastSeen              *int64   `json:"last_seen"`
	Host                  *hostDTO `json:"host"`
	TrafficUsed           int64    `json:"traffic_used"`
	TrafficPeriodStart    string   `json:"traffic_period_start"`
	TrafficNextReset      string   `json:"traffic_next_reset"`
	TrafficExpectedStart  int64    `json:"traffic_expected_start"`
	TrafficPeriodRevision int64    `json:"traffic_period_revision"`
}

// hostDTO 是 agent 上报的静态信息（步骤 05 起才有值）。
type hostDTO struct {
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Arch         string `json:"arch"`
	CPUModel     string `json:"cpu_model"`
	Cores        int    `json:"cores"`
	MemTotal     int64  `json:"mem_total"`
	DiskTotal    int64  `json:"disk_total"`
	BootTime     int64  `json:"boot_time"`
	IPv4         bool   `json:"ipv4"`
	IPv6         bool   `json:"ipv6"`
	PublicIP     string `json:"public_ip"`
	AgentVersion string `json:"agent_version"`
	UpdatedAt    int64  `json:"updated_at"`
}

func toServerConfigDTO(s *store.Server) serverConfigDTO {
	return serverConfigDTO{
		ID:               s.ID,
		Name:             s.Name,
		Region:           s.Region,
		GroupName:        s.GroupName,
		Tags:             s.Tags,
		SortOrder:        s.SortOrder,
		PublicHost:       s.PublicHost,
		Price:            s.Price,
		Currency:         s.Currency,
		BillingCycle:     s.BillingCycle,
		ExpireAt:         s.ExpireAt,
		AutoRenew:        s.AutoRenew,
		TrafficLimit:     s.TrafficLimit,
		TrafficResetDay:  s.TrafficResetDay,
		TrafficResetMode: s.TrafficResetMode,
		TrafficMode:      s.TrafficMode,
		BandwidthLabel:   s.BandwidthLabel,
		Note:             s.Note,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}

func toServerDTO(s *store.Server, h *store.HostInfo, online bool, lastSeen *int64) serverDTO {
	dto := serverDTO{serverConfigDTO: toServerConfigDTO(s), Online: online, LastSeen: lastSeen}
	if h != nil {
		dto.Host = &hostDTO{
			Hostname:     h.Hostname,
			OS:           h.OS,
			Kernel:       h.Kernel,
			Arch:         h.Arch,
			CPUModel:     h.CPUModel,
			Cores:        h.Cores,
			MemTotal:     h.MemTotal,
			DiskTotal:    h.DiskTotal,
			BootTime:     h.BootTime,
			IPv4:         h.IPv4,
			IPv6:         h.IPv6,
			PublicIP:     h.PublicIP,
			AgentVersion: h.AgentVersion,
			UpdatedAt:    h.UpdatedAt,
		}
	}
	return dto
}

// listServers 处理 GET /api/servers。
func (d *Deps) listServers(w http.ResponseWriter, r *http.Request) {
	servers, err := d.DB.ListServers(r.Context())
	if err != nil {
		serverError(w, "list servers", err)
		return
	}
	hosts, err := d.DB.ListHostInfo(r.Context())
	if err != nil {
		serverError(w, "list host info", err)
		return
	}

	out := make([]serverDTO, 0, len(servers))
	for i := range servers {
		s := &servers[i]
		var host *store.HostInfo
		if h, ok := hosts[s.ID]; ok {
			host = &h
		}
		online, lastSeen := d.Hub.Status(s.ID)
		out = append(out, d.withTraffic(toServerDTO(s, host, online, lastSeen)))
	}

	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// getServer 处理 GET /api/servers/{id}。
func (d *Deps) getServer(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}

	host, err := d.DB.GetHostInfo(r.Context(), s.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		serverError(w, "get host info", err)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		host = nil
	}

	online, lastSeen := d.Hub.Status(s.ID)
	writeJSON(w, http.StatusOK, map[string]any{"server": d.withTraffic(toServerDTO(s, host, online, lastSeen))})
}

// createServer 处理 POST /api/servers。token 明文只在这里返回一次。
func (d *Deps) createServer(w http.ResponseWriter, r *http.Request) {
	var req serverRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	schedule, err := req.scheduleInput(in, clock.Now(), true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.TrafficPeriod = &store.TrafficPeriod{Start: schedule.Start, NextReset: schedule.NextReset}

	token, err := auth.NewAgentToken()
	if err != nil {
		serverError(w, "create server: new token", err)
		return
	}

	s, err := d.DB.CreateServer(r.Context(), in, auth.HashAgentToken(token))
	if err != nil {
		serverError(w, "create server", err)
		return
	}

	after := toServerConfigDTO(s)
	audit.Record(r.Context(), d.DB, "server.create", "server", strconv.FormatInt(s.ID, 10), nil, after)
	slog.Info("server created", "server_id", s.ID, "name", s.Name)
	if d.Traffic != nil {
		if err := d.Traffic.Reload(r.Context()); err != nil {
			slog.Error("reload traffic", "err", err)
		}
	}

	d.Hub.InvalidateConfig()

	// 刚创建的节点还没有 agent 连过，online=false、host=null 是事实，不是占位。
	writeJSON(w, http.StatusCreated, map[string]any{
		"server":          d.withTraffic(toServerDTO(s, nil, false, nil)),
		"token":           token,
		"install_command": installCommand(d.publicBase(r), token),
	})
}

// updateServer 处理 PUT /api/servers/{id}：全量覆盖可写字段，token 不变。
func (d *Deps) updateServer(w http.ResponseWriter, r *http.Request) {
	before, ok := d.lookupServer(w, r)
	if !ok {
		return
	}

	var req serverRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Old clients do not know about reset modes; editing another field must not
	// silently turn an existing 30-day schedule into a monthly one.
	if req.TrafficResetMode == "" {
		req.TrafficResetMode = before.TrafficResetMode
	}
	in, err := req.toInput()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	schedule, err := req.scheduleInput(in, clock.Now(), false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var updated *store.Server
	if d.Traffic != nil {
		updated, err = d.Traffic.UpdateServer(r.Context(), before.ID, in, schedule)
	} else if schedule != nil {
		writeError(w, http.StatusServiceUnavailable, "流量统计尚未就绪，请稍后修改流量周期")
		return
	} else {
		updated, err = d.DB.UpdateServer(r.Context(), before.ID, in)
	}
	switch {
	case errors.Is(err, traffic.ErrScheduleChanged), errors.Is(err, store.ErrTrafficConfigChanged):
		writeError(w, http.StatusConflict, traffic.ErrScheduleChanged.Error())
		return
	case errors.Is(err, traffic.ErrScheduleDates), errors.Is(err, store.ErrTrafficPeriodOverlap):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, store.ErrNotFound):
		notFoundServer(w)
		return
	case err != nil:
		serverError(w, "update server", err)
		return
	}

	audit.Record(r.Context(), d.DB, "server.update", "server", strconv.FormatInt(updated.ID, 10),
		toServerConfigDTO(before), toServerConfigDTO(updated))
	slog.Info("server updated", "server_id", updated.ID, "name", updated.Name)
	if d.Traffic != nil {
		if err := d.Traffic.Reload(r.Context()); err != nil {
			slog.Error("reload traffic", "err", err)
		}
	}

	d.Hub.InvalidateConfig()

	// 这里要把 host 一起查出来：之前写死 nil，PUT 的返回体和 GET 对不上，
	// 前端按同一个类型用这个返回值，会把已有的 host 抹掉。
	host, err := d.DB.GetHostInfo(r.Context(), updated.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			serverError(w, "get host info", err)
			return
		}
		host = nil
	}
	online, lastSeen := d.Hub.Status(updated.ID)

	writeJSON(w, http.StatusOK, map[string]any{"server": d.withTraffic(toServerDTO(updated, host, online, lastSeen))})
}

// deleteServer 处理 DELETE /api/servers/{id}。子表靠外键级联删除。
func (d *Deps) deleteServer(w http.ResponseWriter, r *http.Request) {
	before, ok := d.lookupServer(w, r)
	if !ok {
		return
	}

	err := d.DB.DeleteServer(r.Context(), before.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFoundServer(w)
		return
	case err != nil:
		serverError(w, "delete server", err)
		return
	}

	audit.Record(r.Context(), d.DB, "server.delete", "server", strconv.FormatInt(before.ID, 10),
		toServerConfigDTO(before), nil)
	slog.Info("server deleted", "server_id", before.ID, "name", before.Name)

	d.Hub.Disconnect(before.ID, "server deleted")
	d.Hub.Remove(before.ID)
	if d.Traffic != nil {
		d.Traffic.Forget(before.ID)
	}
	if d.Reconciler != nil {
		d.Reconciler.Forget(before.ID)
	}
	if d.Ping != nil {
		d.Ping.Forget(before.ID)
	}
	d.Hub.InvalidateConfig()

	w.WriteHeader(http.StatusNoContent)
}

// resetServerToken 处理 POST /api/servers/{id}/token：换一个新 token，旧的立即作废。
func (d *Deps) resetServerToken(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}

	token, err := auth.NewAgentToken()
	if err != nil {
		serverError(w, "reset token: new token", err)
		return
	}

	err = d.DB.UpdateServerTokenHash(r.Context(), s.ID, auth.HashAgentToken(token))
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFoundServer(w)
		return
	case err != nil:
		serverError(w, "reset token", err)
		return
	}

	// 审计只记"换过了"，前后都不含 token 与哈希
	audit.Record(r.Context(), d.DB, "server.token_reset", "server", strconv.FormatInt(s.ID, 10), nil, nil)
	slog.Info("server token reset", "server_id", s.ID, "name", s.Name)

	// 旧 token 立即作废，连着的 agent 也要踢下去——鉴权只在握手时做过一次。
	d.Hub.Disconnect(s.ID, "token reset")

	writeJSON(w, http.StatusOK, map[string]any{
		"token":           token,
		"install_command": installCommand(d.publicBase(r), token),
	})
}

// lookupServer 解析 URL 里的 {id} 并取出节点。失败时已经写好响应，调用方直接 return。
func (d *Deps) lookupServer(w http.ResponseWriter, r *http.Request) (*store.Server, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		notFoundServer(w)
		return nil, false
	}

	s, err := d.DB.GetServer(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFoundServer(w)
		return nil, false
	case err != nil:
		serverError(w, "get server", err)
		return nil, false
	}
	return s, true
}

func notFoundServer(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "节点不存在")
}

// serverError 记日志并返回统一的 500。err 不进响应体——里面可能有 SQL 细节。
func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error(what+" failed", "err", err)
	writeError(w, http.StatusInternalServerError, "服务器内部错误")
}

// publicBase 返回面板对外的基地址（不带尾斜杠）。
//
// 优先用 VM_PUBLIC_URL；没配就按这次请求的 Host 推断，方便本地开发。
// 反向代理后面必须配 VM_PUBLIC_URL，否则拼出来的是内网地址——Host 头是客户端可控的，
// 这里只用来拼给管理员看的安装命令，不参与任何鉴权判断。
func (d *Deps) publicBase(r *http.Request) string {
	if d.PublicURL != "" {
		return d.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// 可信代理模式下才采信 X-Forwarded-Proto：直连时这个头是伪造的
	if !d.TrustedProxies.Direct() {
		if proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); proto == "http" || proto == "https" {
			scheme = proto
		}
	}
	return scheme + "://" + r.Host
}

// installCommand 拼一键安装命令。base 形如 https://panel.example.com。
func installCommand(base, token string) string {
	return "curl -fsSL " + base + "/install.sh | bash -s -- --server " + wsURL(base) + "/api/agent/ws --token " + token
}

// wsURL 把 http(s) 基地址换成 ws(s)。
func wsURL(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return base
	}
}
