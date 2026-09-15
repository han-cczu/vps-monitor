import type { ServerSnapshot } from 'src/types/realtime';

export type TrafficScope = 'period' | 'boot';

/** 两种口径不能互相兜底：未上报的系统计数必须显示为未知。 */
export function selectTrafficTotals(server: ServerSnapshot, scope: TrafficScope) {
  return scope === 'boot'
    ? { out: server.net.boot_out_total ?? null, in: server.net.boot_in_total ?? null }
    : { out: server.traffic?.out ?? null, in: server.traffic?.in ?? null };
}
