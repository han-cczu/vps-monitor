package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/proxy"
	"vpsmon/server/internal/proxy/render"
)

func (d *Deps) coreRoutes(r chi.Router) {
	r.Post("/servers/{id}/core/install", d.coreAction("install"))
	r.Post("/servers/{id}/core/restart", d.coreAction("restart"))
	r.Post("/servers/{id}/core/apply", d.coreApply)
	r.Get("/servers/{id}/core/logs", d.coreLogs)
	r.Get("/servers/{id}/core/revisions", d.coreRevisions)
	r.Get("/servers/{id}/core/revisions/{rev}", d.coreRevision)
	r.Post("/servers/{id}/core/rollback/{rev}", d.coreRollback)
}
func (d *Deps) reconcilerReady(w http.ResponseWriter) bool {
	if d.Reconciler == nil {
		writeError(w, 503, "核心对齐服务未启动")
		return false
	}
	return true
}
func coreOperationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, proxy.ErrOffline), errors.Is(err, proxy.ErrNoCurrentCore), errors.Is(err, proxy.ErrCoreBusy), errors.Is(err, corefiles.ErrNotFound), errors.Is(err, corefiles.ErrConflict):
		writeError(w, 409, err.Error())
	case errors.Is(err, proxy.ErrCoreTimeout):
		writeError(w, 504, err.Error())
	case errors.Is(err, render.ErrCheck):
		writeError(w, 400, err.Error())
	default:
		return proxyError(w, err)
	}
	return true
}
func (d *Deps) coreAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.reconcilerReady(w) {
			return
		}
		id, ok := proxyID(w, r)
		if !ok {
			return
		}
		var empty struct{}
		if !decodeProxy(w, r, &empty, true) {
			return
		}
		reqID, err := d.Reconciler.Action(r.Context(), id, action)
		if coreOperationError(w, err) {
			return
		}
		writeJSON(w, 202, map[string]any{"req_id": reqID, "queued": true})
	}
}
func (d *Deps) coreApply(w http.ResponseWriter, r *http.Request) {
	if !d.reconcilerReady(w) {
		return
	}
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if !decodeProxy(w, r, &empty, true) {
		return
	}
	rev, err := d.Reconciler.Reconcile(r.Context(), id, true)
	if coreOperationError(w, err) {
		return
	}
	rev.ConfigJSON = nil
	view, err := d.Reconciler.View(r.Context(), id)
	if coreOperationError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"revision": rev, "core": view})
}
func (d *Deps) coreLogs(w http.ResponseWriter, r *http.Request) {
	if !d.reconcilerReady(w) {
		return
	}
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	lines := 200
	if raw := r.URL.Query().Get("lines"); raw != "" {
		var err error
		lines, err = strconv.Atoi(raw)
		if err != nil {
			writeError(w, 400, "lines 必须为 1–1000")
			return
		}
	}
	text, err := d.Reconciler.Logs(r.Context(), id, lines)
	if coreOperationError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"kind": "error", "text": text})
}
func (d *Deps) coreRevisions(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	rows, err := d.DB.Proxy().Revisions(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"revisions": rows})
}
func revisionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "rev"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "修订号必须为正整数")
		return 0, false
	}
	return id, true
}
func (d *Deps) coreRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	rev, ok := revisionID(w, r)
	if !ok {
		return
	}
	row, err := d.DB.Proxy().Revision(r.Context(), id, rev)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"revision": row})
}
func (d *Deps) coreRollback(w http.ResponseWriter, r *http.Request) {
	if !d.reconcilerReady(w) {
		return
	}
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	num, ok := revisionID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if !decodeProxy(w, r, &empty, true) {
		return
	}
	rev, err := d.Reconciler.Rollback(r.Context(), id, num)
	if coreOperationError(w, err) {
		return
	}
	rev.ConfigJSON = nil
	writeJSON(w, 200, map[string]any{"revision": rev})
}
