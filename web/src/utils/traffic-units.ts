/** 流量套餐统一按十进制换算，服务端存储原始字节。 */
export const TRAFFIC_BASE = 1000;
export const GB = TRAFFIC_BASE ** 3;
export const TB = TRAFFIC_BASE ** 4;
export const TRAFFIC_UNITS = ['GB', 'TB'] as const;
export type TrafficUnit = (typeof TRAFFIC_UNITS)[number];

/** 保留已有配额的精度，避免只编辑名称时改变流量上限。 */
export function splitTraffic(bytes: number): { value: number; unit: TrafficUnit } {
  if (!bytes || bytes <= 0) return { value: 0, unit: 'GB' };
  if (bytes % TB === 0) return { value: bytes / TB, unit: 'TB' };
  return { value: bytes / GB, unit: 'GB' };
}

export function joinTraffic(value: number, unit: TrafficUnit): number {
  if (!value || value <= 0) return 0;
  return Math.round(value * (unit === 'TB' ? TB : GB));
}
