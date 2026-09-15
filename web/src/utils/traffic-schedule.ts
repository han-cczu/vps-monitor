import type { TrafficResetMode } from '../types/server';

/** Date-only arithmetic avoids differences between the browser and panel timezone. */
export function nextTrafficReset(start: string, mode: TrafficResetMode, day: number): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(start)) return '';
  const date = new Date(`${start}T00:00:00Z`);
  if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== start) return '';
  if (mode === 'days') {
    date.setUTCDate(date.getUTCDate() + 30);
    return date.toISOString().slice(0, 10);
  }
  if (!Number.isInteger(day) || day < 1 || day > 31) return '';
  const boundary = (month: number) => {
    const last = new Date(Date.UTC(date.getUTCFullYear(), month + 1, 0)).getUTCDate();
    return new Date(Date.UTC(date.getUTCFullYear(), month, Math.min(day, last)));
  };
  let next = boundary(date.getUTCMonth());
  if (next <= date) next = boundary(date.getUTCMonth() + 1);
  return next.toISOString().slice(0, 10);
}
