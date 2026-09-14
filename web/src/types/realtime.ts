// 实时快照的类型。字段与服务端 `hub.ServerView` 一一对应，见 docs/protocol.md §2.2。
//
// 这份结构不是 REST 的 ServerItem：它是服务端为了每秒广播刻意压短、重新分组过的投影
// （`group` 不是 `group_name`、`cycle` 不是 `billing_cycle`），两边都要改的时候别只改一处。
// 服务端那边有 golden 测试（server/internal/hub/testdata/snapshot.json）守着字段名。

import type { PingSummary } from 'src/types/ping';
import type { BillingCycle, TrafficSnapshot } from 'src/types/server';

// ----------------------------------------------------------------------

/** WebSocket 连接状态 */
export type WsStatus = 'connecting' | 'open' | 'closed';

/** 内存 / 磁盘这类「已用 + 总量」 */
export type UsageStat = {
  used: number;
  total: number;
};

/** 交换分区只有已用 */
export type SwapStat = {
  used: number;
};

/** 网络：up / down 是瞬时速率（字节每秒），*_total 是累计字节 */
export type NetStat = {
  up: number;
  down: number;
  out_total: number;
  in_total: number;
};

/** 连接数 */
export type ConnStat = {
  tcp: number;
  udp: number;
};

export type CoreSummary = {
  installed: boolean;
  running: boolean;
  version: string;
  users: number;
  pending: boolean;
  error: string | null;
};

/** 一台节点的实时快照 */
export type ServerSnapshot = {
  id: number;
  name: string;
  region: string;
  group: string;
  tags: string[];
  sort: number;

  online: boolean;
  /** 服务端收到最后一条 metrics 的时刻（秒）；从没连过是 null */
  last_seen: number | null;
  v4: boolean;
  v6: boolean;

  /** 0–100 */
  cpu: number;
  cores: number;
  mem: UsageStat;
  swap: SwapStat;
  disk: UsageStat;
  /** 1 / 5 / 15 分钟负载 */
  load: [number, number, number];
  net: NetStat;
  conn: ConnStat;
  procs: number;
  /** 秒 */
  uptime: number;

  /** YYYY-MM-DD；null 表示不设到期 */
  expire_at: string | null;
  bandwidth: string;
  price: number;
  currency: string;
  cycle: BillingCycle;

  /** 当前节点账期用量；服务未装配时为 null */
  traffic: TrafficSnapshot | null;
  /** 启用的延迟任务摘要；无任务时为空数组 */
  ping: PingSummary[];
  /** sing-box 核心状态；未装配核心服务时为 null */
  core: CoreSummary | null;
};

/** server → 浏览器的整帧快照 */
export type Snapshot = {
  type: 'snapshot';
  /** 服务端组帧时刻（秒） */
  ts: number;
  servers: ServerSnapshot[];
};
