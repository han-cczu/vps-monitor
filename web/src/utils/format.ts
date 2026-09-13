// 监控面板的通用格式化。
//
// 与 format-time.ts 分工：那边是日期时间（dayjs），这边是容量 / 速率 / 百分比 / 时长 / 价格。
// 节点页与监控总览页都用这一套，改了两边一起变。

import type { BillingCycle } from 'src/types/server';

import { CURRENCY_SYMBOLS, BILLING_CYCLE_LABELS } from 'src/constants/server';

// ----------------------------------------------------------------------

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const;

/**
 * 字节数转可读容量。
 *
 * 默认 1000 进制：agent 采的是 /proc 里的原始字节，而商家标称的「1 TB 流量」按 1000 算，
 * 用 1024 会让用户觉得面板少算了。步骤 20 接进设置项后可以按需切换。
 *
 * @example formatBytes(1_500_000_000) => '1.5 GB'
 * @example formatBytes(1_073_741_824, { base: 1024 }) => '1 GB'
 */
export function formatBytes(bytes: number, options?: { base?: 1000 | 1024 }): string {
  const base = options?.base ?? 1000;

  if (!Number.isFinite(bytes) || bytes <= 0) {
    return '0 B';
  }

  let value = bytes;
  let unit = 0;
  while (value >= base && unit < BYTE_UNITS.length - 1) {
    value /= base;
    unit += 1;
  }

  // 进位要看**四舍五入之后**的值：999_999_999 除一次是 999.999999 MB，
  // 不补这一下就会显示成「1000 MB」。
  if (Math.round(value * 100) / 100 >= base && unit < BYTE_UNITS.length - 1) {
    value /= base;
    unit += 1;
  }

  return `${trimNumber(value)} ${BYTE_UNITS[unit]}`;
}

/**
 * 速率，单位是字节每秒。
 *
 * @example formatRate(303) => '303 B/s'
 * @example formatRate(1_500_000) => '1.5 MB/s'
 */
export function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(bytesPerSecond)}/s`;
}

/**
 * 百分比。超出 0–100 会被夹回来——CPU 偶尔会因为采样误差冒出 100.3 这种值。
 *
 * @example formatPercent(3.0234) => '3.02%'
 */
export function formatPercent(value: number, fractionDigits = 2): string {
  const clamped = Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0));
  return `${clamped.toFixed(fractionDigits)}%`;
}

/**
 * 秒数转「N 天 / N 小时 / N 分钟」，只保留最大的那一档。
 *
 * 卡片上的「在线 2 天」不需要更精确，精确到分钟反而每分钟触发一次重渲染。
 *
 * @example formatDuration(172800) => '2 天'
 * @example formatDuration(45) => '不到 1 分钟'
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 60) {
    return '不到 1 分钟';
  }

  const days = Math.floor(seconds / 86_400);
  if (days >= 1) {
    return `${days} 天`;
  }

  const hours = Math.floor(seconds / 3_600);
  if (hours >= 1) {
    return `${hours} 小时`;
  }

  return `${Math.floor(seconds / 60)} 分钟`;
}

/** 到期日剩余天数；没设到期返回 null。负数表示已经过期。 */
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

/** 价格 + 周期，如 `$10.79 / 月`；价格为 0 时显示 `—`。 */
export function formatPrice(price: number, currency: string, cycle: BillingCycle): string {
  if (!price) {
    return '—';
  }

  const symbol = CURRENCY_SYMBOLS[currency] ?? `${currency} `;
  const amount = Number.isInteger(price) ? String(price) : trimNumber(price);
  // 兜一下未知周期：库里这一列没有 CHECK 约束，真混进别的值也只该显示得难看，不该白屏
  const cycleLabel = cycle === 'once' ? '一次性' : (BILLING_CYCLE_LABELS[cycle] ?? cycle).replace('付', '');

  return `${symbol}${amount} / ${cycleLabel}`;
}

// ----------------------------------------------------------------------

/** 最多两位小数，并去掉尾随的 0（1.50 → 1.5，2.00 → 2）。 */
function trimNumber(value: number): string {
  return String(Math.round(value * 100) / 100);
}
