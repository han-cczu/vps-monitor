// 监控面板的通用格式化。
//
// 与 format-time.ts 分工：那边是日期时间（dayjs），这边是容量 / 速率 / 百分比 / 时长 / 价格。
// 节点页与监控总览页都用这一套，改了两边一起变。

import type { BillingCycle } from 'src/types/server';

import { CURRENCY_SYMBOLS, BILLING_CYCLE_LABELS } from 'src/constants/server';

// ----------------------------------------------------------------------

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const;
const BINARY_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'] as const;
let preferredBase: 1000 | 1024 = 1000;
let preferredTimezone: string | undefined;
export function setFormatPreferences(base: 1000 | 1024, timezone: string) {
  preferredBase = base === 1024 ? 1024 : 1000;
  try {
    new Intl.DateTimeFormat('en', { timeZone: timezone }).format();
    preferredTimezone = timezone;
  } catch {
    preferredTimezone = undefined;
  }
}

/**
 * 字节数转可读容量。
 *
 * 内存、磁盘容量跟随站点显示设置，默认 1000 进制。
 * 流量使用 formatTrafficBytes，固定按十进制显示。
 *
 * @example formatBytes(1_500_000_000) => '1.5 GB'
 * @example formatBytes(1_073_741_824, { base: 1024 }) => '1 GiB'
 */
export function formatBytes(bytes: number, options?: { base?: 1000 | 1024 }): string {
  const base = options?.base ?? preferredBase;

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

  return `${trimNumber(value)} ${base === 1024 ? BINARY_UNITS[unit] : BYTE_UNITS[unit]}`;
}

/** 流量固定按十进制 GB/TB 显示，不受内存、磁盘显示设置影响。 */
export function formatTrafficBytes(bytes: number): string {
  return formatBytes(bytes, { base: 1000 });
}

/**
 * 网络速率，单位是字节每秒，固定按十进制显示。
 *
 * @example formatRate(303) => '303 B/s'
 * @example formatRate(1_500_000) => '1.5 MB/s'
 */
export function formatRate(bytesPerSecond: number): string {
  return `${formatTrafficBytes(bytesPerSecond)}/s`;
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

  const target = Date.parse(`${expireAt}T00:00:00Z`);
  if (!Number.isFinite(target)) return null;
  const parts = new Intl.DateTimeFormat('en', {
    timeZone: preferredTimezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date());
  const part = (type: string) => parts.find((p) => p.type === type)?.value ?? '';
  const today = Date.parse(`${part('year')}-${part('month')}-${part('day')}T00:00:00Z`);
  return Math.round((target - today) / 86_400_000);
}

/** 价格 + 周期，如 `$10.79 / 月`；价格为 0 时显示 `—`。 */
export function formatPrice(price: number, currency: string, cycle: BillingCycle): string {
  if (!price) {
    return '—';
  }

  const symbol = CURRENCY_SYMBOLS[currency] ?? `${currency} `;
  const amount = Number.isInteger(price) ? String(price) : trimNumber(price);
  // 兜一下未知周期：库里这一列没有 CHECK 约束，真混进别的值也只该显示得难看，不该白屏
  const cycleLabel =
    cycle === 'once' ? '一次性' : (BILLING_CYCLE_LABELS[cycle] ?? cycle).replace('付', '');

  return `${symbol}${amount} / ${cycleLabel}`;
}

// ----------------------------------------------------------------------

/** 最多两位小数，并去掉尾随的 0（1.50 → 1.5，2.00 → 2）。 */
function trimNumber(value: number): string {
  return String(Math.round(value * 100) / 100);
}

/** Calendar date in the configured panel timezone (input: Unix seconds). */
export function formatPanelDate(unixSeconds: number): string {
  if (!Number.isFinite(unixSeconds)) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: preferredTimezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(unixSeconds * 1000));
}
