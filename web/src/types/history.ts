// 历史曲线的类型。与服务端 api/history.go 的响应一一对应，见 docs/protocol.md。
//
// 字段名比库里的短（`cpu` 而不是 `cpu_avg`）：24 小时是 1440 个点，
// 字段名占的字节比数值还多。

// ----------------------------------------------------------------------

/** 可选的时间窗。1h / 24h 走分钟表，7d / 30d 走小时表。 */
export type HistoryRange = '1h' | '24h' | '7d' | '30d';

export const HISTORY_RANGES: { value: HistoryRange; label: string }[] = [
  { value: '1h', label: '1 小时' },
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
];

/** 曲线上的一个点。ts 是区间起点（Unix 秒）。 */
export type HistoryPoint = {
  ts: number;

  /** 区间平均，0–100 */
  cpu: number;
  /** 区间峰值 */
  cpu_max: number;
  /** 字节 */
  mem: number;
  swap: number;
  disk: number;
  load1: number;

  /** 字节/秒 */
  rx: number;
  rx_max: number;
  tx: number;
  tx_max: number;

  tcp: number;
  udp: number;
  procs: number;
};

export type HistoryResponse = {
  /** 步长，60（分钟表）或 3600（小时表） */
  step: number;
  from: number;
  to: number;
  points: HistoryPoint[];

  /** 画百分比用的总量，来自 host info；节点没上报过是 0 */
  mem_total: number;
  disk_total: number;
};
