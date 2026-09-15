package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"vpsmon/server/internal/proxy"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

func (d *Deps) proxyRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/servers/{id}/inbounds", d.proxyListInbounds)
		r.Get("/servers/{id}/proxy-observations", d.proxyObservations)
		r.Get("/servers/{id}/proxy-instances", d.proxyObservations)
		r.Get("/servers/{id}/proxy-instances/{instance}", d.proxyInstance)
		r.Get("/servers/{id}/proxy-instances/{instance}/{section:inbounds}", d.proxyInstance)
		r.Post("/servers/{id}/proxy-observations/refresh", d.proxyObservationsRefresh)
		r.Get("/servers/{id}/subscriber-traffic", d.nodeSubscriberTraffic)
		r.Post("/servers/{id}/inbounds", d.proxyCreateInbound)
		r.Get("/inbounds/{id}", d.proxyGetInbound)
		r.Put("/inbounds/{id}", d.proxyUpdateInbound)
		r.Delete("/inbounds/{id}", d.proxyDeleteInbound)
		r.Post("/inbounds/{id}/regenerate-keys", d.proxyRegenerateKeys)
		r.Get("/servers/{id}/cert", d.proxyGetCert)
		r.Post("/servers/{id}/cert/regenerate", d.proxyRegenerateCert)
		r.Get("/servers/{id}/advanced", d.proxyGetAdvanced)
		r.Put("/servers/{id}/advanced", d.proxyPutAdvanced)
		r.Post("/servers/{id}/advanced/check", d.checkAdvanced)
		r.Post("/servers/{id}/advanced/relay", d.relayAdvanced)
		r.Delete("/servers/{id}/advanced/relay", d.removeRelayAdvanced)
		r.Get("/servers/{id}/core", d.proxyGetCore)
		d.coreRoutes(r)
		r.Get("/subscribers", d.proxyListSubscribers)
		r.Post("/subscribers", d.proxyCreateSubscriber)
		r.Get("/subscribers/{id}", d.proxyGetSubscriber)
		r.Get("/subscribers/{id}/traffic", d.subscriberTraffic)
		r.Put("/subscribers/{id}", d.proxyUpdateSubscriber)
		r.Delete("/subscribers/{id}", d.proxyDeleteSubscriber)
		r.Put("/subscribers/{id}/assignments", d.proxyAssignments)
		for _, action := range []string{"reset-token", "regenerate-credentials", "reset-usage"} {
			r.Post("/subscribers/{id}/"+action, d.proxySubscriberAction(action))
		}
	})
}
func proxyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "ID 必须为正整数")
		return 0, false
	}
	return id, true
}
func proxyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var validation *proxy.ValidationError
	switch {
	case errors.Is(err, proxy.ErrReadOnly):
		writeError(w, 409, err.Error())
	case errors.As(err, &validation):
		writeError(w, 400, validation.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "节点、入站或订阅用户不存在")
	case errors.Is(err, proxy.ErrConflict):
		writeError(w, 409, err.Error())
	case errors.Is(err, proxy.ErrAdvancedChanged):
		writeError(w, 409, err.Error())
	default:
		serverError(w, "proxy operation", err)
	}
	return true
}
func decodeProxy(w http.ResponseWriter, r *http.Request, dst any, emptyOK bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "请求体读取失败或超过 1 MiB")
		return false
	}
	if emptyOK && len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	obj, err := model.Object(raw, maxBodyBytes)
	if err != nil {
		writeError(w, 400, err.Error())
		return false
	}
	for key, v := range obj {
		if key != "expire_at" && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			writeError(w, 400, "字段不能为 null（expire_at 除外）")
			return false
		}
	}
	if err = model.StrictJSON(raw, dst); err != nil {
		writeError(w, 400, err.Error())
		return false
	}
	return true
}
func (d *Deps) proxyListInbounds(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	items, err := d.Proxy.Inbounds(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"inbounds": items})
}
func (d *Deps) proxyGetInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	item, err := d.Proxy.Inbound(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"inbound": item})
}
func (d *Deps) proxyCreateInbound(w http.ResponseWriter, r *http.Request) {
	d.proxySaveInbound(w, r, true)
}
func (d *Deps) proxyUpdateInbound(w http.ResponseWriter, r *http.Request) {
	d.proxySaveInbound(w, r, false)
}
func (d *Deps) proxySaveInbound(w http.ResponseWriter, r *http.Request, create bool) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var in proxy.InboundInput
	if !decodeProxy(w, r, &in, false) {
		return
	}
	serverID, inboundID := int64(0), id
	code := 200
	if create {
		serverID = id
		inboundID = 0
		code = 201
	}
	item, err := d.Proxy.SaveInbound(r.Context(), serverID, inboundID, in)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, code, map[string]any{"inbound": item})
}
func (d *Deps) proxyDeleteInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.Proxy.DeleteInbound(r.Context(), id)) {
		return
	}
	w.WriteHeader(204)
}
func (d *Deps) proxyRegenerateKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if !decodeProxy(w, r, &empty, true) {
		return
	}
	item, err := d.Proxy.RegenerateKeys(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"inbound": item})
}
func (d *Deps) proxyGetCert(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	cert, err := d.Proxy.Cert(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"cert": cert})
}
func (d *Deps) proxyRegenerateCert(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var in struct {
		SNI string `json:"sni"`
	}
	if !decodeProxy(w, r, &in, true) {
		return
	}
	cert, err := d.Proxy.RegenerateCert(r.Context(), id, in.SNI)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"cert": cert})
}
func (d *Deps) proxyGetAdvanced(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	advanced, err := d.Proxy.Advanced(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"advanced": advanced})
}
func (d *Deps) proxyPutAdvanced(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var in struct {
		ExtraJSON json.RawMessage `json:"extra_json"`
	}
	if !decodeProxy(w, r, &in, false) {
		return
	}
	advanced, err := d.Proxy.SaveAdvancedChecked(r.Context(), id, in.ExtraJSON, d.AdvancedCheck)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"advanced": advanced})
}
func (d *Deps) proxyGetCore(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if d.Reconciler != nil {
		core, err := d.Reconciler.View(r.Context(), id)
		if coreOperationError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"core": core})
		return
	}
	core, err := d.Proxy.Core(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"core": core})
}
func (d *Deps) proxyListSubscribers(w http.ResponseWriter, r *http.Request) {
	items, err := d.Proxy.Subscribers(r.Context())
	if proxyError(w, err) {
		return
	}
	if r.URL.Query().Get("include_relay") != "1" {
		filtered := make([]*store.Subscriber, 0, len(items))
		for _, item := range items {
			if item.Kind != "relay" {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, 200, map[string]any{"subscribers": items})
}
func (d *Deps) proxyGetSubscriber(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	item, err := d.Proxy.Subscriber(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"subscriber": item})
}
func (d *Deps) proxyCreateSubscriber(w http.ResponseWriter, r *http.Request) {
	d.proxySaveSubscriber(w, r, true)
}
func (d *Deps) proxyUpdateSubscriber(w http.ResponseWriter, r *http.Request) {
	d.proxySaveSubscriber(w, r, false)
}
func (d *Deps) proxySaveSubscriber(w http.ResponseWriter, r *http.Request, create bool) {
	id := int64(0)
	if !create {
		var ok bool
		id, ok = proxyID(w, r)
		if !ok {
			return
		}
	}
	var in proxy.SubscriberInput
	if !decodeProxy(w, r, &in, false) {
		return
	}
	item, err := d.Proxy.SaveSubscriber(r.Context(), id, in)
	if proxyError(w, err) {
		return
	}
	code := 200
	if create {
		code = 201
	}
	writeJSON(w, code, map[string]any{"subscriber": item})
}
func (d *Deps) proxyDeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if proxyError(w, d.Proxy.DeleteSubscriber(r.Context(), id)) {
		return
	}
	w.WriteHeader(204)
}
func (d *Deps) proxyAssignments(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var in struct {
		InboundIDs []int64 `json:"inbound_ids"`
	}
	if !decodeProxy(w, r, &in, false) {
		return
	}
	item, err := d.Proxy.Assign(r.Context(), id, in.InboundIDs)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"subscriber": item})
}
func (d *Deps) proxySubscriberAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := proxyID(w, r)
		if !ok {
			return
		}
		var empty struct{}
		if !decodeProxy(w, r, &empty, true) {
			return
		}
		item, err := d.Proxy.SubscriberAction(r.Context(), id, action)
		if proxyError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"subscriber": item})
	}
}
