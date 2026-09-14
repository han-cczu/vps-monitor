package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"vpsmon/server/internal/alert"
	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/store"
)

func (d *Deps) alertRoutes(r chi.Router) {
	r.Get("/alert-rules", d.alertRules)
	r.Put("/alert-rules", d.saveAlertRules)
	r.Get("/notify-channels", d.notifyChannels)
	r.Post("/notify-channels", d.saveNotifyChannel)
	r.Put("/notify-channels/{id}", d.saveNotifyChannel)
	r.Delete("/notify-channels/{id}", d.deleteNotifyChannel)
	r.Post("/notify-channels/{id}/test", d.testNotifyChannel)
	r.Get("/alert-events", d.alertEvents)
	r.Post("/alert-events/{id}/resolve", d.resolveAlert)
}
func (d *Deps) alertRules(w http.ResponseWriter, r *http.Request) {
	rules, err := d.DB.AlertRules(r.Context())
	if err != nil {
		serverError(w, "get alert rules", err)
		return
	}
	writeJSON(w, 200, map[string]any{"rules": rules})
}
func (d *Deps) saveAlertRules(w http.ResponseWriter, r *http.Request) {
	var rules []store.AlertRule
	if !decodeJSON(w, r, &rules) {
		return
	}
	if err := alert.ValidateRules(rules); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	before, err := d.DB.AlertRules(r.Context())
	if err != nil {
		serverError(w, "get alert rules", err)
		return
	}
	if err := d.DB.SaveAlertRules(r.Context(), rules, clock.Now().Unix()); err != nil {
		serverError(w, "save alert rules", err)
		return
	}
	audit.Record(r.Context(), d.DB, "alert.rules.update", "alert_rules", "", before, rules)
	d.alertRules(w, r)
}
func (d *Deps) notifyChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := d.DB.NotifyChannels(r.Context())
	if err != nil {
		serverError(w, "list channels", err)
		return
	}
	out := make([]map[string]any, 0, len(channels))
	for _, c := range channels {
		out = append(out, alert.RedactChannel(c))
	}
	writeJSON(w, 200, map[string]any{"channels": out})
}
func alertID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 404, "记录不存在")
		return 0, false
	}
	return id, true
}
func (d *Deps) saveNotifyChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string          `json:"name"`
		Kind        string          `json:"kind"`
		Config      json.RawMessage `json:"config"`
		Enabled     *bool           `json:"enabled"`
		ClearSecret bool            `json:"clear_secret"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 {
		writeError(w, 400, "渠道名称必须为 1–80 个字符")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	channel := store.NotifyChannel{Name: req.Name, Kind: req.Kind, Config: req.Config, Enabled: enabled, CreatedAt: clock.Now().Unix()}
	var before *store.NotifyChannel
	if r.Method == http.MethodPut {
		id, ok := alertID(w, r)
		if !ok {
			return
		}
		channel.ID = id
		var err error
		before, err = d.DB.NotifyChannel(r.Context(), id)
		if !alertStoreError(w, "get channel", err) {
			return
		}
		if before.Kind != channel.Kind {
			writeError(w, 400, "已有渠道不能更改 kind，请新建渠道")
			return
		}
		var incoming, previous alert.ChannelConfig
		if json.Unmarshal(req.Config, &incoming) != nil {
			writeError(w, 400, "渠道配置格式无效")
			return
		}
		_ = json.Unmarshal(before.Config, &previous)
		if incoming.BotToken == "" {
			incoming.BotToken = previous.BotToken
		}
		if incoming.Secret == "" && !req.ClearSecret {
			incoming.Secret = previous.Secret
		}
		if req.ClearSecret {
			incoming.Secret = ""
		}
		// Validate original keys too, so edit requests do not silently accept typos.
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(req.Config, &keys)
		for key := range keys {
			if key != "bot_token" && key != "chat_id" && key != "url" && key != "secret" {
				writeError(w, 400, "未知渠道配置字段")
				return
			}
		}
		channel.Config, _ = json.Marshal(incoming)
		channel.CreatedAt = before.CreatedAt
	}
	if _, err := alert.DecodeChannel(channel.Kind, channel.Config); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := d.DB.SaveNotifyChannel(r.Context(), &channel); !alertStoreError(w, "save channel", err) {
		return
	}
	// Audit only metadata; URL may contain a signed query, so omit config entirely.
	audit.Record(r.Context(), d.DB, "notify_channel.save", "notify_channel", strconv.FormatInt(channel.ID, 10), nil, map[string]any{"name": channel.Name, "kind": channel.Kind, "enabled": channel.Enabled})
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, map[string]any{"channel": alert.RedactChannel(channel)})
}
func alertStoreError(w http.ResponseWriter, what string, err error) bool {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "记录不存在")
		return false
	}
	if err != nil {
		serverError(w, what, err)
		return false
	}
	return true
}
func (d *Deps) deleteNotifyChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(w, r)
	if !ok {
		return
	}
	if !alertStoreError(w, "delete channel", d.DB.DeleteNotifyChannel(r.Context(), id)) {
		return
	}
	audit.Record(r.Context(), d.DB, "notify_channel.delete", "notify_channel", strconv.FormatInt(id, 10), nil, nil)
	w.WriteHeader(204)
}
func (d *Deps) testNotifyChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(w, r)
	if !ok {
		return
	}
	if d.Alerts == nil {
		writeError(w, 503, "告警服务未装配")
		return
	}
	err := d.Alerts.Test(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "记录不存在")
		return
	}
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	audit.Record(r.Context(), d.DB, "notify_channel.test", "notify_channel", strconv.FormatInt(id, 10), nil, nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (d *Deps) alertEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AlertFilter{Page: 1, Open: q.Get("open") == "1", Kind: q.Get("kind"), Target: q.Get("target")}
	if raw := q.Get("page"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 100000 {
			writeError(w, 400, "page 必须为 1–100000")
			return
		}
		f.Page = p
	}
	if raw := q.Get("open"); raw != "" && raw != "0" && raw != "1" {
		writeError(w, 400, "open 必须为 0 或 1")
		return
	}
	if f.Kind != "" {
		if _, ok := alert.Kinds[f.Kind]; !ok {
			writeError(w, 400, "未知规则 kind")
			return
		}
	}
	if f.Target != "" {
		parts := strings.Split(f.Target, ":")
		if len(parts) != 2 || (parts[0] != "server" && parts[0] != "subscriber") {
			writeError(w, 400, "target 格式为 server:ID 或 subscriber:ID")
			return
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id <= 0 {
			writeError(w, 400, "target ID 无效")
			return
		}
	}
	events, total, open, err := d.DB.ListAlertEvents(r.Context(), f)
	if err != nil {
		serverError(w, "list alert events", err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": events, "total": total, "open_count": open, "page": f.Page, "page_size": 50})
}
func (d *Deps) resolveAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(w, r)
	if !ok {
		return
	}
	event, err := d.DB.AlertEvent(r.Context(), id)
	if !alertStoreError(w, "get alert event", err) {
		return
	}
	if event.ResolvedAt == nil {
		if !alertStoreError(w, "resolve alert", d.DB.ResolveAlert(r.Context(), id, clock.Now().Unix(), false)) {
			return
		}
	}
	audit.Record(r.Context(), d.DB, "alert.resolve", "alert_event", strconv.FormatInt(id, 10), nil, nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
