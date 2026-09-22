import type {
  ObservedInstance,
  ProxyObservations,
  ProxyObservationScan,
} from 'src/types/proxy-observation';

export function canEditManagedProxy(data?: ProxyObservations) {
  return !!data?.online && (data.management === 'managed' || data.management === 'none');
}
export function observedBytes(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value) || value < 0) return '不可用';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let n = value;
  let index = 0;
  while (n >= 1000 && index < units.length - 1) {
    n /= 1000;
    index += 1;
  }
  return `${n.toLocaleString('zh-CN', { maximumFractionDigits: 2 })} ${units[index]}`;
}

/**
 * 把一条观测记录补齐成页面可以安全渲染的形状。
 *
 * 早期版本或人工导入的记录可能缺少数组字段（JSON 里是 null）；直接 .map/.join
 * 会让整页白屏，所以读之前统一补成空数组，并把缺失的版本号显示成"版本未知"。
 */
export function normalizeInstance(item: ObservedInstance): ObservedInstance {
  return {
    ...item,
    version: item.version ?? '',
    config_paths: item.config_paths ?? [],
    ports: item.ports ?? [],
    inbounds: item.inbounds ?? [],
    issues: item.issues ?? [],
  };
}

/**
 * 把观测记录拆成当前实例和历史记录。
 *
 * 只有「完整扫描里没出现」的记录才会被服务端标记 absent；不完整扫描、离线、超时都不会。
 * 所以 absent 是历史记录的唯一依据，不能拿 running/stale 来代替——进程停了或者快照过期，
 * 都不等于实例消失。
 */
export function splitObservations(instances: ObservedInstance[]) {
  const current: ObservedInstance[] = [];
  const history: ObservedInstance[] = [];
  for (const raw of instances ?? []) {
    const item = normalizeInstance(raw);
    (item.absent ? history : current).push(item);
  }
  return { current, history };
}

export type InstanceStatus = { label: string; color: 'success' | 'warning' | 'default' };

/**
 * 当前卡片的状态标签。running=false 表示进程没在跑（二进制/服务还在），不是已卸载；
 * stale 表示这份快照还没被核验，需要在卡片里另外说明哪些字段是旧的。
 *
 * 节点不再确认这一条时（离线，或它没出现在最近那一轮扫描里）标签也要跟着变：
 * 「运行中」这句话在实例层面已经不成立了，只能说明最后一次看到它时在运行。
 */
export function instanceStatus(
  item: ObservedInstance,
  context: { online: boolean; fromLatest: boolean } = { online: true, fromLatest: true }
): InstanceStatus {
  if (!context.online || !context.fromLatest) {
    return { label: item.running ? '运行中 · 未确认' : '未运行 · 未确认', color: 'default' };
  }
  if (item.stale) return { label: '待核验快照', color: 'warning' };
  if (item.running) return { label: '运行中', color: 'success' };
  return { label: '未运行', color: 'default' };
}

/** 探针在读取配置时可能报出的问题；出现这些说明配置摘要没有本轮结果。 */
const configReadIssues = [
  'config_path_unknown',
  'config_directory_unreadable',
  'config_files_limit',
  'config_invalid_jsonc',
  'source_read_failed',
  'source_changed_during_read',
  'process_changed_during_read',
];

export type SnapshotNotice = { severity: 'warning'; text: string };

/** 快照时间戳按面板本地时区显示；0 表示从未成功采集。 */
function stamp(value: number) {
  return value > 0 ? new Date(value * 1000).toLocaleString('zh-CN') : '未记录';
}

/**
 * 这一条快照是不是节点最近一次提交的那一轮采集的结果。
 *
 * 节点级扫描状态每一轮提交都会更新，不完整扫描也算一次提交；但某一轮没有上报的实例，
 * 它的 received_at 会停在原地。scan.received_at 比它新，就说明这一条不在最近那一轮里：
 * 卡片里的 PID、端口绑定、入站都只能按历史读数看，哪怕节点本身一直在正常上报。
 */
export function instanceFromLatestScan(
  item: ObservedInstance,
  scan: ProxyObservationScan | null
) {
  return !!scan && item.received_at >= scan.received_at;
}

/**
 * 单张卡片里哪些字段还能当实时信息看。
 *
 * 判断分两层，缺一不可：
 *
 * - 节点层：离线或最近一次扫描过期时，整份快照都是旧的。
 * - 实例层：节点还在上报，但这一条没出现在最近那一轮里（例如节点连续几轮不完整
 *   扫描都只报出别的实例），它的进程、端口、入站同样不是本轮结果。只看节点级
 *   scan.stale 是不够的——节点级状态一直在更新，过期的是这一条自己。
 *
 * 剩下的情况再按这一轮的实际采集结果说明：配置没读到时摘要与入站来自上一次成功
 * 读取，而用量是每轮单独采集的（读不到会被清空，也可能单独重新读取），不能一概
 * 说成"上一次成功读取的结果"。
 */
export function snapshotNotice(
  item: ObservedInstance,
  { online, scan }: { online: boolean; scan: ProxyObservationScan | null }
): SnapshotNotice | null {
  if (!online || !!scan?.stale) {
    return {
      severity: 'warning',
      text: '面板保存的是最后一次收到的快照，不是节点现在的状态：这里的 PID、内存、端口绑定、入站和用量都来自那次读取。探针恢复上报并完成新的完整扫描后才会更新。',
    };
  }
  // 节点还在上报，但这一条不在最近那一轮里：整体按历史读数说明，不能声称"本次读取"。
  if (!instanceFromLatestScan(item, scan)) {
    const config =
      item.config_read_at > 0
        ? `配置摘要与入站来自 ${stamp(item.config_read_at)} 那次成功读取`
        : '配置摘要与入站没有成功读取过';
    return {
      severity: 'warning',
      text: `这一条不是节点最近一次采集的结果：它最后一次收到于 ${stamp(item.received_at)}，节点最近一次扫描在 ${stamp(scan?.received_at ?? 0)}。卡片里的 PID、内存、端口绑定、${config}，用量按那次上报的结果展示（读不到时是空的）——都不能当作本轮状态。`,
    };
  }
  if (!item.stale) return null;
  const issues = new Set(item.issues);
  const configFailed = configReadIssues.some((issue) => issues.has(issue));
  const processChanged = issues.has('process_changed_during_read');
  const process = processChanged
    ? '读取期间进程发生变化，PID、内存和端口绑定是待核验快照，可能不属于当前进程'
    : item.running
      ? 'PID、内存和端口绑定是本次读取的结果'
      : '进程没有在运行，本次没有可读的绑定端口';
  const config = configFailed
    ? item.config_read_at > 0
      ? `配置摘要与入站仍是 ${stamp(item.config_read_at)} 那次成功读取的结果`
      : '配置摘要与入站没有成功读取过'
    : '配置摘要与入站按本次读取结果展示';
  if (processChanged || configFailed) {
    return {
      severity: 'warning',
      text: `${process}；${config}；用量按本次实际采集到的结果展示，读不到或已被清空的显示为不可用，不沿用旧值。`,
    };
  }
  return {
    severity: 'warning',
    text: '这一份快照尚未完成核验：卡片里的进程、端口绑定、入站和用量按最后读取到的结果展示，不能当作当前状态。',
  };
}

export type ScanNotice = { severity: 'info' | 'warning'; text: string };

/**
 * 节点级扫描状态说明。
 *
 * 「探针离线」「能力未协商」「等待首个快照」「采集不完整」「完整扫描后确实没有实例」
 * 是五种不同结论，只靠实例数组是否为空区分不了，必须结合在线状态、能力协商和
 * 服务端保存的扫描状态。「当前未发现代理实例」这句话只允许出现在
 * 在线 + 能力已协商 + 完整 + 未过期的扫描上；过期的空结果只能说明上次没发现。
 */
export function scanNotice({
  scan,
  online,
  supported,
  hasCurrent,
  hasHistory,
}: {
  scan: ProxyObservationScan | null;
  online: boolean;
  supported: boolean;
  hasCurrent: boolean;
  hasHistory: boolean;
}): ScanNotice | null {
  // 离线优先：无论能力协商结果如何，离线时都无法确认当前实例。
  if (!online) {
    return {
      severity: 'info',
      text: `探针离线，无法确认当前实例。显示的是面板保存的最后记录，不代表节点现在的状态。${
        hasHistory ? '历史记录可以在这里清理，清理不影响节点本身。' : ''
      }`,
    };
  }
  if (!supported) {
    return { severity: 'warning', text: '探针已连接，但尚未完成观测能力协商（可能需要升级探针）。' };
  }
  if (!scan) {
    return { severity: 'info', text: '正在等待首个完整观测快照，尚未引入实例列表。' };
  }
  if (scan.stale) {
    return {
      severity: 'warning',
      text: `面板保存的扫描结果已超过 90 秒未更新，无法确认节点当前状态。${
        scan.complete && !scan.instances
          ? '上一次完整扫描没有发现代理实例，但那是当时的结论。'
          : '这里保留最后一次完整扫描的结果。'
      }`,
    };
  }
  if (!scan.complete) {
    return {
      severity: 'warning',
      text: `最近一次采集不完整（${scan.instances} 个实例）：缺少分页、超时或权限不足时无法据此认定实例已消失，这里保留上次完整扫描的结果，不能算作本轮成功。`,
    };
  }
  if (!scan.instances && !hasCurrent) {
    return {
      severity: 'info',
      text: `当前未发现代理实例（最近一次完整扫描未发现任何实例）。${
        hasHistory ? '此前的记录保留在历史观测记录里。' : ''
      }`,
    };
  }
  return null;
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
