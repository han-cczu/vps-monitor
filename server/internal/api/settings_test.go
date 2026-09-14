package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"vpsmon/server/internal/clock"
)

func TestSettingsWhitelistAtomicMergeAndHook(t *testing.T) {
	oldLoc := clock.Location()
	defer clock.SetLocation(oldLoc)
	changed := []string{}
	e := newTestEnv(t, func(d *Deps) {
		d.SettingsDefaults = map[string]any{"sub.clash_template": "default"}
		d.ValidateSetting = func(k string, v json.RawMessage) error {
			if k == "sub.clash_template" && string(v) != `"valid"` {
				return fmt.Errorf("invalid template")
			}
			return nil
		}
		d.SettingsChanged = func(keys []string) { changed = keys }
	})
	token := e.adminToken(t)
	ctx := context.Background()
	_ = e.db.SetSetting(ctx, "internal.secret", "hidden")
	resp, b := e.do(t, "GET", "/api/settings", token, nil)
	if resp.StatusCode != 200 || b["internal.secret"] != nil || b["sub.clash_template"] != "default" {
		t.Fatal(b)
	}
	for _, bad := range []map[string]any{{"site.title": "changed", "internal.secret": "oops"}, {"site.bytes_base": 1025}, {"retention.audit_days": 0}, {"site.tz": "Local"}, {"enforce.count_mode": "max"}, {"sub.clash_template": "invalid"}} {
		resp, b = e.do(t, "PUT", "/api/settings", token, bad)
		if resp.StatusCode != 400 {
			t.Fatal(b)
		}
	}
	_, b = e.do(t, "GET", "/api/settings", token, nil)
	if b["site.title"] != "VPS Monitor" {
		t.Fatal("partial write survived rejected request")
	}
	resp, b = e.do(t, "PUT", "/api/settings", token, map[string]any{"site.title": "Panel", "site.tz": "UTC", "site.bytes_base": 1024, "enforce.count_mode": "download", "sub.clash_template": "valid"})
	if resp.StatusCode != 200 || len(changed) != 5 || b["site.bytes_base"] != float64(1024) {
		t.Fatal(b)
	}
	var hidden string
	_, _ = e.db.GetSetting(ctx, "internal.secret", &hidden)
	if hidden != "hidden" {
		t.Fatal("unrelated setting erased")
	}
	resp, b = e.do(t, "GET", "/api/audit?actor=admin&action=settings.update&size=1", token, nil)
	if resp.StatusCode != 200 || b["total"] != float64(1) {
		t.Fatal(b)
	}
	for _, path := range []string{"/api/settings", "/api/audit"} {
		resp, _ = e.do(t, "GET", path, "", nil)
		if resp.StatusCode != 401 {
			t.Fatal("unprotected " + path)
		}
	}
	resp, _ = e.do(t, "GET", "/api/audit?from=bad", token, nil)
	if resp.StatusCode != 400 {
		t.Fatal("bad audit timestamp accepted")
	}
}
