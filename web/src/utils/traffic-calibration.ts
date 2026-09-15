import type { TrafficMode } from 'src/types/server';

export const CALIBRATION_UNITS = ['GB', 'TB', 'B'] as const;
export type CalibrationUnit = (typeof CALIBRATION_UNITS)[number];

const FACTORS: Record<CalibrationUnit, bigint> = {
  B: 1n,
  GB: 1_000_000_000n,
  TB: 1_000_000_000_000n,
};

/** Parse decimal input without floating-point rounding; round sub-bytes half up. */
export function calibrationBytes(raw: string, unit: CalibrationUnit): number | null {
  const text = raw.trim();
  if (text.length > 40 || !/^\d+(?:\.\d{1,12})?$/.test(text)) return null;
  const [whole, fraction = ''] = text.split('.');
  const scale = 10n ** BigInt(fraction.length);
  const scaled = BigInt(whole + fraction) * FACTORS[unit];
  const bytes = (scaled + scale / 2n) / scale;
  if (bytes > BigInt(Number.MAX_SAFE_INTEGER)) return null;
  return Number(bytes);
}

export function calibratedUsage(inbound: number, outbound: number, mode: TrafficMode): number {
  if (mode === 'in') return inbound;
  if (mode === 'out') return outbound;
  if (mode === 'sum') return inbound + outbound;
  return Math.max(inbound, outbound);
}

/** Decimal GB representation retains every byte when prefilling the form. */
export function calibrationGB(bytes: number): string {
  const value = BigInt(bytes);
  const fraction = (value % FACTORS.GB).toString().padStart(9, '0').replace(/0+$/, '');
  return `${value / FACTORS.GB}${fraction ? `.${fraction}` : ''}`;
}
