import type { TrafficScope } from 'src/utils/traffic-display';

import { create } from 'zustand';

const STORAGE_KEY = 'vps-monitor-traffic-scope';

function initialScope(): TrafficScope {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'boot' ? 'boot' : 'period';
  } catch {
    return 'period';
  }
}

/** 总览和详情共用显示偏好；禁用浏览器存储时仍可在当前页面切换。 */
export const useTrafficDisplay = create<{
  scope: TrafficScope;
  setScope: (scope: TrafficScope) => void;
}>((set) => ({
  scope: initialScope(),
  setScope: (scope) => {
    set({ scope });
    try {
      localStorage.setItem(STORAGE_KEY, scope);
    } catch {
      // 显示偏好不应因存储不可用而阻断操作。
    }
  },
}));
