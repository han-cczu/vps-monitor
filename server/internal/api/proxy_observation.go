package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
	"vpsmon/proto"
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
	rows, err := d.ProxyObserve.List(r.Context(), id, online)
	if proxyError(w, err) {
		return
	}
	if r.URL.Query().Get("summary") == "1" {
		for i := range rows {
			rows[i].Inbounds = nil
		}
	}
	writeJSON(w, 200, map[string]any{"management": mode, "online": online, "supported": d.Hub.Agents.ProxyObserveSupported(id), "instances": rows})
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
	rows, err := d.ProxyObserve.List(r.Context(), id, d.Hub.Agents.Connected(id))
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
