package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"vpsmon/proto"
	"vpsmon/server/internal/agentdist"
	"vpsmon/server/internal/audit"
)

func agentArch(arch string) string {
	switch arch {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return ""
}
func (d *Deps) agentVersion(w http.ResponseWriter, r *http.Request) {
	version, _ := agentdist.CurrentVersion(filepath.Join(d.DataDir, agentDirName))
	items := []map[string]any{}
	servers, err := d.DB.ListServers(r.Context())
	if err != nil {
		serverError(w, "list agent versions", err)
		return
	}
	for _, s := range servers {
		item := map[string]any{"id": s.ID, "online": false, "supported": false, "update_available": false, "version": ""}
		if d.Hub != nil {
			st, ok := d.Hub.Registry.Get(s.ID)
			if ok {
				item["online"] = st.Online
				item["supported"] = st.AgentUpdateCapable
				item["version"] = st.AgentVersion
				item["update_available"] = st.Online && st.AgentUpdateCapable && agentArch(st.Host.Arch) != "" && proto.AgentVersionNewer(version, st.AgentVersion)
			}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"version": version, "servers": items})
}
func (d *Deps) queueAgentUpdate(id int64) error {
	if d.Hub == nil {
		return fmt.Errorf("节点未连接")
	}
	st, ok := d.Hub.Registry.Get(id)
	if !ok || !st.Online {
		return fmt.Errorf("节点未连接")
	}
	if !st.AgentUpdateCapable {
		return fmt.Errorf("此Agent未声明更新协议支持，请手动重装一次")
	}
	a, err := agentdist.UpdateArtifact(filepath.Join(d.DataDir, agentDirName), agentArch(st.Host.Arch), st.AgentVersion)
	if err != nil {
		return err
	}
	if !d.Hub.Agents.SendTo(id, a) {
		return fmt.Errorf("Agent未连接或下发队列已满")
	}
	return nil
}
func (d *Deps) updateAgent(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}
	if err := d.queueAgentUpdate(s.ID); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	audit.Record(r.Context(), d.DB, "agent.update_requested", "server", strconv.FormatInt(s.ID, 10), nil, nil)
	writeJSON(w, 202, map[string]any{"queued": true, "message": "已下发；以节点重新上报的版本为成功依据"})
}
func (d *Deps) updateAllAgents(w http.ResponseWriter, r *http.Request) {
	servers, err := d.DB.ListServers(r.Context())
	if err != nil {
		serverError(w, "list servers", err)
		return
	}
	queued := []int64{}
	skipped := []map[string]any{}
	for _, s := range servers {
		if err := d.queueAgentUpdate(s.ID); err != nil {
			skipped = append(skipped, map[string]any{"id": s.ID, "reason": err.Error()})
			continue
		}
		queued = append(queued, s.ID)
		audit.Record(r.Context(), d.DB, "agent.update_requested", "server", strconv.FormatInt(s.ID, 10), nil, nil)
	}
	writeJSON(w, 202, map[string]any{"queued": queued, "skipped": skipped})
}
