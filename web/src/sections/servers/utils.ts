// 节点页专用的换算与文案。
//
// 通用的容量 / 速率 / 价格 / 到期天数在 src/utils/format.ts（监控总览页也要用）；
// 这里只留表单里那套「GB / TB 双向换算」，它是本页表单独有的。

import type { TrafficMode } from 'src/types/server';

import { REGION_LABELS, TRAFFIC_MODE_LABELS } from 'src/constants/server';

// ----------------------------------------------------------------------

/** 流量单位按 1024 进制（和面板里其他容量显示保持一致）。服务端只认字节。 */
export const GB = 1024 ** 3;
export const TB = 1024 ** 4;

export type TrafficUnit = 'GB' | 'TB';

export const TRAFFIC_UNITS: TrafficUnit[] = ['GB', 'TB'];

/** 把字节拆成表单里的「数值 + 单位」。能整除 TB 就用 TB，否则用 GB。 */
export function splitTraffic(bytes: number): { value: number; unit: TrafficUnit } {
  if (!bytes || bytes <= 0) {
    return { value: 0, unit: 'GB' };
  }
  if (bytes % TB === 0) {
    return { value: bytes / TB, unit: 'TB' };
  }
  return { value: round2(bytes / GB), unit: 'GB' };
}

/** 表单里的「数值 + 单位」换算回字节。 */
export function joinTraffic(value: number, unit: TrafficUnit): number {
  if (!value || value <= 0) {
    return 0;
  }
  return Math.round(value * (unit === 'TB' ? TB : GB));
}

/** 列表里显示的流量上限：0 显示「不限」。 */
export function formatTrafficLimit(bytes: number): string {
  if (!bytes) {
    return '不限';
  }
  const { value, unit } = splitTraffic(bytes);
  return `${value} ${unit}`;
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

function round2(n: number): number {
  return Math.round(n * 100) / 100;
}
