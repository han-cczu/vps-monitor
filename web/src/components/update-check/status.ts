import type { UpdateInfo, UpdateStatus } from 'src/api/updates';

type StatusView = Pick<
  UpdateInfo,
  'state' | 'latest' | 'panel_version' | 'agent_version' | 'panel_status' | 'agent_status'
>;

export type UpdateSummary = {
  severity: 'warning' | 'info';
  panel: string;
  agent: string;
  url: string;
};

function panelStatusText(view: StatusView): string {
  const latest = view.latest?.version ?? '';
  switch (view.panel_status) {
    case 'available':
      return `面板有新版本 ${latest}：先备份数据，再部署新版本。本功能只做检查，不自动安装或重启。`;
    case 'current':
      return '面板已是最新稳定版。';
    case 'ahead':
      return `面板版本高于更新源的公开稳定版${latest ? `（${latest}）` : ''}，可能是尚未发布的构建。`;
    default:
      return `面板版本 ${view.panel_version} 无法与最新稳定版自动比较，请对照发布说明确认。`;
  }
}

function agentStatusText(view: StatusView): string {
  const latest = view.latest?.version ?? '';
  const carried = view.agent_version;
  switch (view.agent_status) {
    case 'available':
      return carried
        ? `更新源已有新探针 ${latest}，面板携带的探针还是 ${carried}：先部署携带新探针的面板安装包，再到节点页逐台或批量升级。`
        : `更新源已有新探针 ${latest}，但面板还没有携带任何探针安装包：先部署带探针安装包的面板版本。`;
    case 'current':
      return '面板携带的探针已是最新稳定版，可在节点页查看各节点版本后逐台或批量升级。';
    case 'ahead':
      return `面板携带的探针版本高于更新源的公开稳定版${latest ? `（${latest}）` : ''}。`;
    default:
      return carried
        ? `面板携带的探针版本 ${carried} 无法自动比较，请对照发布说明确认。`
        : '面板尚未携带探针安装包，请先部署稳定版安装包。';
  }
}

// 结论只在"检查成功且更新源确实有稳定版本"时给出：
// 未检查、检查失败、尚无稳定发布都不产出任何"已是最新"的说法。
export function updateSummary(view: StatusView): UpdateSummary | null {
  if (view.state !== 'ok' || !view.latest) return null;
  return {
    severity: alertSeverity(view.panel_status, view.agent_status),
    panel: panelStatusText(view),
    agent: agentStatusText(view),
    url: view.latest.url,
  };
}

export function latestLabel(state: UpdateInfo['state'], hasLatest: boolean): string {
  return state === 'error' && hasLatest ? '最新稳定版（上次成功结果）' : '最新稳定版';
}

export function latestValue(state: UpdateInfo['state'], latest?: string): string {
  if (latest) return latest;
  if (state === 'ok') return '尚未发布';
  return state === 'error' ? '未获取到' : '待检查';
}

// 只要有一条判断是"有更新"，整条提示按 warning 呈现，其余都是普通信息。
export function alertSeverity(...statuses: UpdateStatus[]): 'warning' | 'info' {
  return statuses.includes('available') ? 'warning' : 'info';
}
