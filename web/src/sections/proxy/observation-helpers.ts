import type { ProxyObservations } from 'src/types/proxy-observation';

export function canEditManagedProxy(data?: ProxyObservations) {
  return !!data?.online && (data.management === 'managed' || data.management === 'none');
}
export function observedBytes(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value) || value < 0) return '不可用';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let n = value;
  let index = 0;
  while (n >= 1024 && index < units.length - 1) {
    n /= 1024;
    index += 1;
  }
  return `${n.toLocaleString('zh-CN', { maximumFractionDigits: 2 })} ${units[index]}`;
}
export const observationIssues: Record<string, string> = {
  duplicate_inbound_tag: '配置中存在重复入站标签，实际生效结果需要在原管理器核验',
  container_network_namespace: '代理位于独立网络命名空间：端口为其内部绑定，未映射到宿主机公网端口',
  version_unavailable: '版本暂时无法核验',
  config_path_unknown: '未找到配置路径，可在探针配置中手动绑定',
  config_directory_unreadable: '配置目录不可读',
  config_files_limit: '配置文件数量超过上限',
  config_invalid_jsonc: '配置不是可解析的 JSON/JSONC',
  source_read_failed: '来源文件不可读或超过大小限制',
  source_changed_during_read: '读取时文件发生变化，等待下次采集',
  ports_unavailable: '无法读取该进程的监听端口',
  binary_replaced: '运行中的可执行文件已被替换',
  multiple_config_application_unconfirmed: '已列出多份配置中的声明，尚不能确认合并后的生效结果',
  stats_namespace_unsupported: '此网络命名空间的统计接口暂不支持',
  stats_read_failed: '统计接口暂时无法读取',
  manager_schema_unsupported: '原管理器的数据库结构暂不支持',
  manager_inbound_ambiguous: '原数据库入站无法唯一对应配置',
  manager_inbounds_limit: '原管理器入站超过展示上限',
  inbounds_limit: '入站超过展示上限',
  process_changed_during_read: '读取时进程发生变化，当前为待核验快照',
  ports_limit: '监听端口超过展示上限',
};
