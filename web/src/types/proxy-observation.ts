export type ObservedUsage = {
  source: string;
  scope: 'manager_total' | 'reference';
  up: number | null;
  down: number | null;
  limit: number | null;
  expire_at: number | null;
  collected_at: number;
};
export type ObservedInbound = {
  id: string;
  tag: string;
  protocol: string;
  listen: string;
  port: string;
  transport: string;
  tls: boolean;
  reality: boolean;
  users: number | null;
  enabled: boolean | null;
  in_config: boolean;
  listening: boolean | null;
  usage: ObservedUsage | null;
};
export type ObservedInstance = {
  id: string;
  core: 'sing-box' | 'xray';
  source: string;
  ownership: 'managed' | 'external';
  version: string;
  running: boolean;
  pid: number;
  process_start: string;
  manager_pid: number;
  manager_running: boolean | null;
  service: string;
  binary: string;
  config_paths: string[];
  namespace: string;
  rss_bytes: number | null;
  ports: { address: string; port: number; network: string }[];
  inbounds: ObservedInbound[];
  stats_status: string;
  issues: string[];
  config_read_at: number;
  last_success: number;
  received_at: number;
  /** 面板记录这条快照的时刻，不是探针采集时刻 */
  absent_at: number;
  stale: boolean;
  absent: boolean;
  truncated: boolean;
};
/**
 * 节点级扫描状态。区分「还没收到过快照」「最近一次采集不完整」「完整扫描后确实没有实例」，
 * 只靠 instances 数组是否为空无法区分这三种情况。
 */
export type ProxyObservationScan = {
  complete: boolean;
  collected_at: number;
  received_at: number;
  instances: number;
  stale: boolean;
};
export type ProxyObservations = {
  management: 'managed' | 'external' | 'none' | 'unknown';
  online: boolean;
  supported: boolean;
  scan: ProxyObservationScan | null;
  instances: ObservedInstance[];
};
export type ProxyObservationResetResult = {
  cleared: boolean;
  /** 面板已向在线的探针下发重新采集请求；true 只表示请求已发出，不代表采集完成 */
  requested: boolean;
  online: boolean;
};
