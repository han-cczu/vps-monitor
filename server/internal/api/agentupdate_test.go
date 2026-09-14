package api

import (
	"os"
	"path/filepath"
	"testing"
	"vpsmon/server/internal/hub"
)

func TestAgentUpdateRequiresCapabilityAndNewStableVersion(t *testing.T) {
	var h *hub.Hub
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "agent"), 0700)
	_ = os.WriteFile(filepath.Join(dir, "agent", "VERSION"), []byte("v1.1.0"), 0600)
	e := newTestEnv(t, func(d *Deps) { h = hub.New(d.DB, d.Tokens); d.Hub = h; d.DataDir = dir })
	token := e.adminToken(t)
	id, _, _ := e.createServer(t, token, newServerBody())
	h.Registry.Update(id, func(st *hub.ServerState) { st.Online = true; st.AgentVersion = "v1.0.0"; st.Host.Arch = "x86_64" })
	_, b := e.do(t, "GET", "/api/agent-version", token, nil)
	rows := b["servers"].([]any)
	row := rows[0].(map[string]any)
	if row["update_available"] != false {
		t.Fatal("legacy agent offered update")
	}
	h.Registry.Update(id, func(st *hub.ServerState) { st.AgentUpdateCapable = true })
	_, b = e.do(t, "GET", "/api/agent-version", token, nil)
	row = b["servers"].([]any)[0].(map[string]any)
	if row["update_available"] != true {
		t.Fatal(b)
	}
	for _, p := range []string{"/api/servers/1/agent/update", "/api/servers/agent/update-all"} {
		resp, _ := e.do(t, "POST", p, "", nil)
		if resp.StatusCode != 401 {
			t.Fatal("unprotected " + p)
		}
	}
	resp, b := e.do(t, "POST", "/api/servers/agent/update-all", token, nil)
	if resp.StatusCode != 202 || len(b["queued"].([]any)) != 0 {
		t.Fatal("missing binary falsely queued")
	}
}
