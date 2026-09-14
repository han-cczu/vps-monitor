import type { ChartOptions } from './types';

import { cloneDeep, mergeWith } from 'es-toolkit';

// ApexCharts 的轴、tooltip 等字段支持对象或数组。数组必须整体替换，不能与默认对象按键合并。
export function mergeChartOptions(base: ChartOptions, overrides: ChartOptions): ChartOptions {
  return mergeWith(cloneDeep(base), overrides, (_target, source) =>
    Array.isArray(source) ? cloneDeep(source) : undefined
  );
}
