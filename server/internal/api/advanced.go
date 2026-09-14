package api

import (
	"encoding/json"
	"net/http"
)

func (d *Deps) checkAdvanced(w http.ResponseWriter, r *http.Request) {
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
	if proxyError(w, d.Proxy.CheckAdvanced(r.Context(), id, in.ExtraJSON, d.AdvancedCheck)) {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (d *Deps) relayAdvanced(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var in struct {
		TargetServerID  int64 `json:"target_server_id"`
		TargetInboundID int64 `json:"target_inbound_id"`
	}
	if !decodeProxy(w, r, &in, false) {
		return
	}
	advanced, subscriberID, err := d.Proxy.Relay(r.Context(), id, in.TargetServerID, in.TargetInboundID, d.AdvancedCheck)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"advanced": advanced, "relay_subscriber_id": subscriberID})
}

func (d *Deps) removeRelayAdvanced(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	advanced, err := d.Proxy.RemoveRelay(r.Context(), id, d.AdvancedCheck)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"advanced": advanced})
}

func (d *Deps) nodeSubscriberTraffic(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	items, err := d.DB.NodeSubscriberTraffic(r.Context(), id)
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"subscribers": items})
}
