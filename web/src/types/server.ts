// 节点相关类型。字段名与服务端 REST / WS 保持一致（蛇形），见 docs/protocol.md。

// ----------------------------------------------------------------------

/** 账单周期 */
export type BillingCycle = 'month' | 'quarter' | 'year' | 'once';

/** 流量统计模式：出站 / 入站 / 出+入 / 取较大者 */
export type TrafficMode = 'out' | 'in' | 'sum' | 'max';

/** agent 上报的静态信息，步骤 05 起才有值 */
export type ServerHostInfo = {
  hostname: string;
  os: string;
  kernel: string;
  arch: string;
  cpu_model: string;
  cores: number;
  mem_total: number;
  disk_total: number;
  boot_time: number;
  ipv4: boolean;
  ipv6: boolean;
  public_ip: string;
  agent_version: string;
  updated_at: number;
};

/** 节点的可写字段，创建与更新用同一份（PUT 是全量覆盖） */
export type ServerPayload = {
  name: string;
  region: string;
  group_name: string;
  tags: string[];
  sort_order: number;
  public_host: string;
  price: number;
  currency: string;
  billing_cycle: BillingCycle;
  /** YYYY-MM-DD；null 表示不设到期 */
  expire_at: string | null;
  auto_renew: boolean;
  /** 字节，0 = 不限 */
  traffic_limit: number;
  traffic_reset_day: number;
  traffic_mode: TrafficMode;
  bandwidth_label: string;
  note: string;
};

/** GET /api/servers 里的一行 */
export type ServerItem = ServerPayload & {
  id: number;
  created_at: number;
  updated_at: number;
  /** 实时状态：步骤 05 起来自 hub 内存态 */
  online: boolean;
  last_seen: number | null;
  host: ServerHostInfo | null;
  traffic_used: number;
};

/** 创建与重置 token 的响应：明文 token 只在这里出现一次 */
export type ServerTokenResult = {
  token: string;
  install_command: string;
};

export type ServerCreateResult = ServerTokenResult & {
  server: ServerItem;
};

export type TrafficSnapshot = {
  used: number;
  limit: number;
  mode: TrafficMode;
  in: number;
  out: number;
  period_start: number;
  period_end_expected: number;
};

export type TrafficPeriod = {
  period_start: number;
  period_end: number | null;
  in: number;
  out: number;
  used: number;
};
