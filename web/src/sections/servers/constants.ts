import type { TrafficMode, BillingCycle } from 'src/types/server';

// ----------------------------------------------------------------------

/** 地区下拉的常用项。表单是可自由输入的，填别的国家码也行。 */
export const REGION_OPTIONS = [
  { code: 'HK', label: '香港' },
  { code: 'TW', label: '台湾' },
  { code: 'MO', label: '澳门' },
  { code: 'CN', label: '中国大陆' },
  { code: 'JP', label: '日本' },
  { code: 'SG', label: '新加坡' },
  { code: 'KR', label: '韩国' },
  { code: 'US', label: '美国' },
  { code: 'CA', label: '加拿大' },
  { code: 'GB', label: '英国' },
  { code: 'DE', label: '德国' },
  { code: 'FR', label: '法国' },
  { code: 'NL', label: '荷兰' },
  { code: 'RU', label: '俄罗斯' },
  { code: 'TR', label: '土耳其' },
  { code: 'AU', label: '澳大利亚' },
  { code: 'IN', label: '印度' },
  { code: 'VN', label: '越南' },
  { code: 'TH', label: '泰国' },
  { code: 'MY', label: '马来西亚' },
  { code: 'ID', label: '印尼' },
  { code: 'PH', label: '菲律宾' },
] as const;

export const REGION_LABELS: Record<string, string> = Object.fromEntries(
  REGION_OPTIONS.map((item) => [item.code, item.label])
);

// ----------------------------------------------------------------------

export const CURRENCY_OPTIONS = [
  { code: 'CNY', label: '人民币 CNY', symbol: '¥' },
  { code: 'USD', label: '美元 USD', symbol: '$' },
  { code: 'HKD', label: '港币 HKD', symbol: 'HK$' },
  { code: 'EUR', label: '欧元 EUR', symbol: '€' },
  { code: 'JPY', label: '日元 JPY', symbol: '¥' },
  { code: 'GBP', label: '英镑 GBP', symbol: '£' },
  { code: 'SGD', label: '新加坡元 SGD', symbol: 'S$' },
  { code: 'TWD', label: '新台币 TWD', symbol: 'NT$' },
  { code: 'KRW', label: '韩元 KRW', symbol: '₩' },
  { code: 'RUB', label: '卢布 RUB', symbol: '₽' },
  { code: 'AUD', label: '澳元 AUD', symbol: 'A$' },
  { code: 'CAD', label: '加元 CAD', symbol: 'C$' },
] as const;

export const CURRENCY_SYMBOLS: Record<string, string> = Object.fromEntries(
  CURRENCY_OPTIONS.map((item) => [item.code, item.symbol])
);

// ----------------------------------------------------------------------

export const BILLING_CYCLE_OPTIONS: { value: BillingCycle; label: string }[] = [
  { value: 'month', label: '月付' },
  { value: 'quarter', label: '季付' },
  { value: 'year', label: '年付' },
  { value: 'once', label: '一次性' },
];

export const BILLING_CYCLE_LABELS: Record<BillingCycle, string> = {
  month: '月付',
  quarter: '季付',
  year: '年付',
  once: '一次性',
};

// ----------------------------------------------------------------------

export const TRAFFIC_MODE_OPTIONS: { value: TrafficMode; label: string; help: string }[] = [
  { value: 'max', label: '取较大者', help: '出站与入站中较大的一个，多数商家按这个算' },
  { value: 'out', label: '仅出站', help: '只统计从节点发出的流量' },
  { value: 'in', label: '仅入站', help: '只统计进入节点的流量' },
  { value: 'sum', label: '出站 + 入站', help: '两个方向相加' },
];

export const TRAFFIC_MODE_LABELS: Record<TrafficMode, string> = {
  max: '取较大者',
  out: '仅出站',
  in: '仅入站',
  sum: '出站 + 入站',
};
