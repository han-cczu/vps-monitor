package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"vpsmon/proto"
	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/proxyobserve"

	"github.com/go-chi/chi/v5"
)

func (d *Deps) proxyObservations(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.DB.Proxy().ServerExists(r.Context(), id)) {
		return
	}
	if d.ProxyObserve == nil || d.Hub == nil {
		writeError(w, 503, "代理观测服务未启动")
		return
	}
	mode := d.Hub.Agents.ProxyManagement(id)
	online := d.Hub.Agents.Connected(id)
	rows, scan, err := d.ProxyObserve.List(r.Context(), id, online)
	if proxyError(w, err) {
		return
	}
	if r.URL.Query().Get("summary") == "1" {
		for i := range rows {
			rows[i].Inbounds = nil
		}
	}
	writeJSON(w, 200, map[string]any{"management": mode, "online": online, "supported": d.Hub.Agents.ProxyObserveSupported(id), "instances": rows, "scan": scan})
}

func (d *Deps) proxyInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.DB.Proxy().ServerExists(r.Context(), id)) {
		return
	}
	if d.ProxyObserve == nil || d.Hub == nil {
		writeError(w, 503, "代理观测服务未启动")
		return
	}
	rows, _, err := d.ProxyObserve.List(r.Context(), id, d.Hub.Agents.Connected(id))
	if proxyError(w, err) {
		return
	}
	for _, item := range rows {
		if item.ID != chi.URLParam(r, "instance") {
			continue
		}
		if chi.URLParam(r, "section") == "inbounds" {
			offset, limit := 0, 100
			var e error
			if raw := r.URL.Query().Get("offset"); raw != "" {
				offset, e = strconv.Atoi(raw)
				if e != nil || offset < 0 {
					writeError(w, 400, "offset 必须为非负整数")
					return
				}
			}
			if raw := r.URL.Query().Get("limit"); raw != "" {
				limit, e = strconv.Atoi(raw)
				if e != nil || limit < 1 || limit > 100 {
					writeError(w, 400, "limit 必须为 1–100")
					return
				}
			}
			start := min(offset, len(item.Inbounds))
			end := start + min(limit, len(item.Inbounds)-start)
			writeJSON(w, 200, map[string]any{"inbounds": item.Inbounds[start:end], "total": len(item.Inbounds), "stale": item.Stale})
			return
		}
		item.Inbounds = nil
		writeJSON(w, 200, map[string]any{"instance": item})
		return
	}
	writeError(w, 404, "代理实例不存在")
}

// proxyDeleteObservation 处理 DELETE /api/servers/{id}/proxy-observations/{instance}：
// 只删除面板保存的这条观测记录。探针、代理进程、配置与托管记录都不受影响；
// 节点离线也允许清理，因为这是面板自己的历史数据。
func (d *Deps) proxyDeleteObservation(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.DB.Proxy().ServerExists(r.Context(), id)) {
		return
	}
	if d.ProxyObserve == nil {
		writeError(w, 503, "代理观测服务未启动")
		return
	}
	instance := chi.URLParam(r, "instance")
	if !proxyobserve.ValidInstanceIdentifier(instance) {
		writeError(w, 400, "代理实例标识不合法")
		return
	}
	found, err := d.ProxyObserve.HandleDelete(r.Context(), id, instance)
	if proxyError(w, err) {
		return
	}
	if !found {
		writeError(w, 404, "该观测记录不存在或已被删除")
		return
	}
	audit.Record(r.Context(), d.DB, "proxy_observation.delete", "server", strconv.FormatInt(id, 10), map[string]string{"instance": instance}, nil)
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// proxyResetObservations 处理 POST /api/servers/{id}/proxy-observations/reset：
// 清空本节点在面板保存的观测快照。在线且已协商观测能力时另外请求一次重新采集，
// 但 202 只表示请求已经下发，不表示采集完成——重新采集的结果要靠前端重新拉取。
func (d *Deps) proxyResetObservations(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.DB.Proxy().ServerExists(r.Context(), id)) {
		return
	}
	var empty struct{}
	if !decodeProxy(w, r, &empty, true) {
		return
	}
	if d.ProxyObserve == nil {
		writeError(w, 503, "代理观测服务未启动")
		return
	}
	before := d.ProxyObserve.Count(r.Context(), id)
	if proxyError(w, d.ProxyObserve.HandleReset(r.Context(), id)) {
		return
	}
	// 清空时观测服务已经重新协商过会话；再请求一次重新采集，让在线节点尽快给出新一轮结果。
	requested := false
	if d.Hub != nil && d.Hub.Agents.SendTo(id, proto.Envelope{Type: proto.TypeProxyRefresh}) {
		requested = true
	}
	audit.Record(r.Context(), d.DB, "proxy_observation.reset", "server", strconv.FormatInt(id, 10),
		map[string]int{"records": before}, map[string]any{"requested": requested, "online": d.Hub != nil && d.Hub.Agents.Connected(id)})
	slog.Info("代理观测记录已清空", "server_id", id, "records", before, "rescan_requested", requested)
	writeJSON(w, 200, map[string]any{"cleared": true, "requested": requested, "online": d.Hub != nil && d.Hub.Agents.Connected(id)})
}

func (d *Deps) proxyObservationsRefresh(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.DB.Proxy().ServerExists(r.Context(), id)) {
		return
	}
	var empty struct{}
	if !decodeProxy(w, r, &empty, true) {
		return
	}
	if d.Hub == nil || !d.Hub.Agents.SendTo(id, proto.Envelope{Type: proto.TypeProxyRefresh}) {
		writeError(w, 409, "探针离线或未支持代理观测，请升级探针")
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}
