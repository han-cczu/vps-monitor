import type { TrafficMode, BillingCycle } from 'src/types/server';

import {
  REGION_LABELS,
  CURRENCY_SYMBOLS,
  TRAFFIC_MODE_LABELS,
  BILLING_CYCLE_LABELS,
} from './constants';

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

/** 价格 + 周期，如 `$10.79 / 月`；价格为 0 时显示 `—`。 */
export function formatPrice(price: number, currency: string, cycle: BillingCycle): string {
  if (!price) {
    return '—';
  }
  const symbol = CURRENCY_SYMBOLS[currency] ?? `${currency} `;
  const amount = Number.isInteger(price) ? String(price) : round2(price).toFixed(2);
  const cycleLabel = cycle === 'once' ? '一次性' : BILLING_CYCLE_LABELS[cycle].replace('付', '');
  return `${symbol}${amount} / ${cycleLabel}`;
}

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

/** 到期日剩余天数；没设到期返回 null。 */
export function daysUntil(expireAt: string | null): number | null {
  if (!expireAt) {
    return null;
  }
  const target = new Date(`${expireAt}T00:00:00`);
  if (Number.isNaN(target.getTime())) {
    return null;
  }
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  return Math.round((target.getTime() - today.getTime()) / 86_400_000);
}

function round2(n: number): number {
  return Math.round(n * 100) / 100;
}
