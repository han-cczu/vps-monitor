// 实时快照的全局 store。
//
// 只存服务端下发的那一帧：连接状态 + 每台节点的最新值。
// 速率折线的历史采样**不在这里**——每秒往 store 写 30 个点会让所有订阅者跑一遍 selector，
// 那份数据放在卡片自己的 useRef 里（见 sections/monitor/net-row.tsx）。

import type { Snapshot, WsStatus, ServerSnapshot } from 'src/types/realtime';

import { create } from 'zustand';

// ----------------------------------------------------------------------

type RealtimeState = {
  status: WsStatus;
  /** 最后一帧的服务端时刻（秒），0 表示还没收到过 */
  ts: number;
  servers: Record<number, ServerSnapshot>;
  /** 节点 id 的展示顺序，服务端已经按 sort_order、id 排好 */
  order: number[];

  applySnapshot: (snapshot: Snapshot) => void;
  setStatus: (status: WsStatus) => void;
  reset: () => void;
};

const initialState = {
  status: 'closed' as WsStatus,
  ts: 0,
  servers: {} as Record<number, ServerSnapshot>,
  order: [] as number[],
};

export const useRealtime = create<RealtimeState>((set) => ({
  ...initialState,

  applySnapshot: (snapshot) =>
    set((state) => {
      const servers: Record<number, ServerSnapshot> = {};
      const order: number[] = [];

      for (const incoming of snapshot.servers) {
        const previous = state.servers[incoming.id];
        // 没变化的节点保留原对象引用，卡片的 memo 才拦得住重渲染。
        servers[incoming.id] = previous && isSameServer(previous, incoming) ? previous : incoming;
        order.push(incoming.id);
      }

      return {
        ts: snapshot.ts,
        servers,
        order: isSameOrder(state.order, order) ? state.order : order,
      };
    }),

  setStatus: (status) => set({ status }),

  /** 登出时清空：下次登录的人不该看到上一个人的节点。 */
  reset: () => set({ ...initialState }),
}));

// ----------------------------------------------------------------------

/** 卡片只订阅自己那一台，别的节点变化不会让它重渲染。 */
export const useServer = (id: number) => useRealtime((state) => state.servers[id]);

export const useServerIds = () => useRealtime((state) => state.order);

export const useRealtimeStatus = () => useRealtime((state) => state.status);

// ----------------------------------------------------------------------

/**
 * 逐字段比较两帧里的同一台节点。
 *
 * **ServerSnapshot 每加一个字段，这里必须同步加上**——漏掉的字段变化时，
 * 这个函数会判定「没变」而复用旧对象，卡片被 memo 拦住，界面就静默地停在旧值上，
 * 而且不会有任何报错。traffic（步骤 18）/ core（13）现在恒为空，
 * 也照样列进来，就是为了那天填上数据时不用记得回来改这里。
 *
 * 嵌套对象逐字段展开，不用 JSON.stringify——十几台节点每秒一次，字符串化的开销大得多。
 */
function isSameServer(a: ServerSnapshot, b: ServerSnapshot): boolean {
  return (
    a.online === b.online &&
    a.last_seen === b.last_seen &&
    a.cpu === b.cpu &&
    a.procs === b.procs &&
    a.uptime === b.uptime &&
    a.mem.used === b.mem.used &&
    a.mem.total === b.mem.total &&
    a.swap.used === b.swap.used &&
    a.disk.used === b.disk.used &&
    a.disk.total === b.disk.total &&
    a.net.up === b.net.up &&
    a.net.down === b.net.down &&
    a.net.out_total === b.net.out_total &&
    a.net.in_total === b.net.in_total &&
    a.conn.tcp === b.conn.tcp &&
    a.conn.udp === b.conn.udp &&
    a.load[0] === b.load[0] &&
    a.load[1] === b.load[1] &&
    a.load[2] === b.load[2] &&
    a.cores === b.cores &&
    a.v4 === b.v4 &&
    a.v6 === b.v6 &&
    a.name === b.name &&
    a.region === b.region &&
    a.group === b.group &&
    a.sort === b.sort &&
    a.price === b.price &&
    a.currency === b.currency &&
    a.cycle === b.cycle &&
    a.bandwidth === b.bandwidth &&
    a.expire_at === b.expire_at &&
    a.traffic === b.traffic &&
    a.core === b.core &&
    a.ping.length === b.ping.length &&
    a.ping.every((item, i) => {
      const other = b.ping[i];
      return (
        item.task_id === other.task_id &&
        item.name === other.name &&
        item.latency === other.latency &&
        item.loss === other.loss &&
        item.last_ts === other.last_ts
      );
    }) &&
    isSameList(a.tags, b.tags)
  );
}

/** 标签等原始值列表逐项比较；ping 对象在上面按字段比较。 */
function isSameList(a: readonly unknown[], b: readonly unknown[]): boolean {
  return a.length === b.length && a.every((item, index) => item === b[index]);
}

function isSameOrder(a: number[], b: number[]): boolean {
  return a.length === b.length && a.every((id, index) => id === b[index]);
}
