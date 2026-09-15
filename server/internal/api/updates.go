package api

import (
	"net/http"
	"path/filepath"

	"vpsmon/server/internal/agentdist"
	"vpsmon/server/internal/updates"
)

// updatePayload 是「检查更新」对外的完整视图：GitHub 侧的检查结果，加上本机两个版本。
//
// PanelStatus / AgentStatus 由 Latest 与本地版本比较得出，Latest 为空时一律是 unknown，
// 所以界面只要看到 State 不是 ok 就不能把状态文案当成有效结论。
type updatePayload struct {
	updates.Result
	PanelVersion string `json:"panel_version"`
	PanelStatus  string `json:"panel_status"`
	AgentVersion string `json:"agent_version"`
	AgentStatus  string `json:"agent_status"`
}

// getUpdates 只读缓存：打开页面不会触发 GitHub 查询。
func (d *Deps) getUpdates(w http.ResponseWriter, r *http.Request) {
	d.writeUpdates(w, d.Updates.Snapshot())
}

// checkUpdates 发起一次检查，受检查器自身的合并与缓存约束（成功 10 分钟、失败 1 分钟）。
// 它只查询公开发布信息：不下载、不安装、也不向任何节点下发升级。
func (d *Deps) checkUpdates(w http.ResponseWriter, r *http.Request) {
	d.writeUpdates(w, d.Updates.Check(r.Context()))
}

func (d *Deps) writeUpdates(w http.ResponseWriter, result updates.Result) {
	// InstalledVersion 连 dev/自定义版本一起返回；能否自动升级仍由 CurrentVersion 决定。
	agentVersion, _ := agentdist.InstalledVersion(filepath.Join(d.DataDir, agentDirName))
	latest := ""
	if result.Latest != nil {
		latest = result.Latest.Version
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, updatePayload{
		Result:       result,
		PanelVersion: d.Version,
		PanelStatus:  updates.Compare(latest, d.Version),
		AgentVersion: agentVersion,
		AgentStatus:  updates.Compare(latest, agentVersion),
	})
}
