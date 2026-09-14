package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/store"
)

var settingDefaults = map[string]any{
	"site.title": "VPS Monitor", "site.tz": "Asia/Shanghai", "site.bytes_base": 1000,
	"retention.metrics_minute_days": 7, "retention.metrics_hour_days": 365, "retention.ping_days": 30, "retention.audit_days": 365,
	"alert.cooldown_minutes": 30, "enforce.count_mode": "sum", "sub.clash_template": "",
}

func (d *Deps) readSettings(ctx context.Context) (map[string]any, error) {
	out := make(map[string]any, len(settingDefaults))
	for key, def := range settingDefaults {
		if key == "site.tz" {
			def = clock.Location().String()
		}
		if v, ok := d.SettingsDefaults[key]; ok {
			def = v
		}
		value := def
		if _, err := d.DB.GetSetting(ctx, key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}
func (d *Deps) getSettings(w http.ResponseWriter, r *http.Request) {
	values, err := d.readSettings(r.Context())
	if err != nil {
		writeError(w, 500, "读取设置失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, values)
}
func (d *Deps) validateSetting(key string, raw json.RawMessage) error {
	if _, ok := settingDefaults[key]; !ok {
		return fmt.Errorf("不允许修改设置 %s", key)
	}
	if string(raw) == "null" {
		return fmt.Errorf("%s 不能为空", key)
	}
	var s string
	var n int
	switch key {
	case "site.title":
		if json.Unmarshal(raw, &s) != nil || utf8.RuneCountInString(strings.TrimSpace(s)) < 1 || utf8.RuneCountInString(s) > 80 {
			return fmt.Errorf("站点标题须为1–80字")
		}
	case "site.tz":
		if json.Unmarshal(raw, &s) != nil || len(s) > 80 {
			return fmt.Errorf("时区无效")
		}
		if _, err := time.LoadLocation(s); err != nil || s == "" || s == "Local" {
			return fmt.Errorf("请输入有效的 IANA 时区")
		}
	case "site.bytes_base":
		if json.Unmarshal(raw, &n) != nil || (n != 1000 && n != 1024) {
			return fmt.Errorf("流量单位必须是1000或1024")
		}
	case "enforce.count_mode":
		if json.Unmarshal(raw, &s) != nil || (s != "sum" && s != "download") {
			return fmt.Errorf("统计口径必须为sum或download")
		}
	case "sub.clash_template":
		if json.Unmarshal(raw, &s) != nil || len(s) > 256<<10 {
			return fmt.Errorf("订阅模板须为不超过256KiB的字符串")
		}
		if d.ValidateSetting == nil {
			return fmt.Errorf("订阅模板验证器未装配")
		}
	case "alert.cooldown_minutes":
		if json.Unmarshal(raw, &n) != nil || n < 1 || n > 10080 {
			return fmt.Errorf("冷却时间须为1–10080分钟")
		}
	default:
		if json.Unmarshal(raw, &n) != nil || n < 1 || n > 3650 {
			return fmt.Errorf("保留期须为1–3650天")
		}
	}
	if d.ValidateSetting != nil {
		return d.ValidateSetting(key, raw)
	}
	return nil
}
func (d *Deps) putSettings(w http.ResponseWriter, r *http.Request) {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	var values map[string]json.RawMessage
	if !decodeJSON(w, r, &values) {
		return
	}
	if len(values) == 0 {
		writeError(w, 400, "没有要修改的设置")
		return
	}
	for key, raw := range values {
		if err := d.validateSetting(key, raw); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	p, _ := auth.PrincipalFromContext(r.Context())
	entry := store.AuditEntry{TS: time.Now().Unix(), Actor: p.Name, Action: "settings.update", TargetType: "settings", TargetID: "site", IP: audit.ClientIP(r)}
	if err := d.DB.SetSettingsAudited(r.Context(), values, entry); err != nil {
		writeError(w, 500, "保存设置及审计失败")
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	if raw, ok := values["site.tz"]; ok {
		var tz string
		_ = json.Unmarshal(raw, &tz)
		loc, _ := time.LoadLocation(tz)
		clock.SetLocation(loc)
	}
	if d.SettingsChanged != nil {
		d.SettingsChanged(keys)
	}
	d.getSettings(w, r)
}
