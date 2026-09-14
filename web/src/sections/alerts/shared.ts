export const RULE_INFO: Record<string, { name: string; description: string }> = {
  'server.offline': { name: '节点离线', description: '超过设定分钟未收到指标；上线后恢复。' },
  'server.cpu': {
    name: 'CPU 持续过高',
    description: '连续完整分钟的平均 CPU 使用率均超阈值；缺少采样不算达标。',
  },
  'server.mem': { name: '内存持续过高', description: '连续完整分钟的平均内存使用率均超阈值。' },
  'server.disk': { name: '磁盘持续过高', description: '连续完整分钟的磁盘使用率均超阈值。' },
  'server.traffic': {
    name: '节点流量阈值',
    description: '可选择 80、90、100；每账期每个阈值提醒一次。',
  },
  'server.expire': { name: '节点即将到期', description: '可选择提前 7、3、1 天；每日去重。' },
  'ping.loss': { name: 'Ping 丢包过高', description: '最近 30 次窗口丢包率超过阈值，恢复后通知。' },
  'subscriber.quota': {
    name: '订阅用量阈值',
    description: '可选择 80、100；100 表示套餐超额自动停用。',
  },
  'subscriber.expired': {
    name: '订阅用户到期',
    description: '收到订阅用户到期并自动停用事件时提醒。',
  },
  'core.apply_failed': { name: '核心应用失败', description: '配置应用失败或确认超时后提醒。' },
  'core.down': {
    name: '核心停止运行',
    description: '在线节点已安装 sing-box，但持续未运行；恢复后通知。',
  },
};

export function parseThresholds(value: string, allowed: number[]): number[] {
  const items = value
    .split(/[,，\s]+/)
    .filter(Boolean)
    .map(Number);
  if (
    !items.length ||
    items.some((n) => !allowed.includes(n)) ||
    new Set(items).size !== items.length
  ) {
    throw new Error(`阈值须从 ${allowed.join('、')} 中选择且不得重复`);
  }
  return items;
}
