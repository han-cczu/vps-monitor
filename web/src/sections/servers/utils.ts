// 节点页专用的换算与文案。
//
// 通用的容量 / 速率 / 价格 / 到期天数在 src/utils/format.ts（监控总览页也要用）；
// 流量套餐的十进制换算由共享工具提供。

import type { TrafficMode } from 'src/types/server';

import { formatTrafficBytes } from 'src/utils/format';

import { REGION_LABELS, TRAFFIC_MODE_LABELS } from 'src/constants/server';

// ----------------------------------------------------------------------

export { GB, TB, joinTraffic, splitTraffic, TRAFFIC_UNITS } from 'src/utils/traffic-units';
export type { TrafficUnit } from 'src/utils/traffic-units';

/** 列表里显示的流量上限：0 显示「不限」。 */
export function formatTrafficLimit(bytes: number): string {
  if (!bytes) {
    return '不限';
  }
  return formatTrafficBytes(bytes);
}

// ----------------------------------------------------------------------

/** 地区显示成「香港 HK」；没配就是空串。 */
export function formatRegion(region: string): string {
  if (!region) {
    return '';
  }
  const label = REGION_LABELS[region];
  return label ? `${label} ${region}` : region;
}

export function formatTrafficMode(mode: TrafficMode): string {
  return TRAFFIC_MODE_LABELS[mode] ?? mode;
}
