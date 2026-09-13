// 浏览器侧的实时通道。
//
// 协议见 docs/protocol.md §2：连上后 5 秒内必须发一帧 {"type":"auth","token":JWT}，
// 之后每秒收一帧 snapshot。浏览器的 WebSocket API 带不了请求头，所以 token 走首帧。

import type { Snapshot } from 'src/types/realtime';

import { useRealtime } from 'src/store/realtime';

// ----------------------------------------------------------------------

/** 重连退避：1 秒起翻倍，30 秒封顶。 */
const MIN_BACKOFF = 1_000;
const MAX_BACKOFF = 30_000;

/** 连接活过这么久才把退避归零，避免「连上就被踢」时每秒重连（和 agent 侧同一个思路）。 */
const STABLE_CONNECTION = 10_000;

/** 看门狗的检查周期，以及「多久没收到帧就认为链路已死」。 */
const WATCHDOG_INTERVAL = 3_000;
const STALE_AFTER = 6_000;

type Options = {
  getToken: () => string | null;
  /** 服务端以 4001 拒绝鉴权时调用（token 过期或无效）。 */
  onUnauthorized?: () => void;
};

/**
 * 启动实时通道，返回一个停止函数。
 *
 * 停止函数是幂等的，React 18+ 的 StrictMode 会把 effect 跑两遍，
 * 这里必须保证「启动 → 停止 → 再启动」不会留下野连接或野定时器。
 */
export function startRealtime({ getToken, onUnauthorized }: Options): () => void {
  const { setStatus, applySnapshot } = useRealtime.getState();

  let socket: WebSocket | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let watchdog: ReturnType<typeof setInterval> | null = null;
  let backoff = MIN_BACKOFF;
  /** 本次连接 open 的时刻；0 表示这条连接从来没 open 过。 */
  let openedAt = 0;
  /** 最后一次收到帧的本地时刻。用本地时钟而不是帧里的 ts，免得节点与浏览器时钟不一致。 */
  let lastFrameAt = 0;
  let stopped = false;

  const clearRetry = () => {
    if (retryTimer !== null) {
      clearTimeout(retryTimer);
      retryTimer = null;
    }
  };

  const clearWatchdog = () => {
    if (watchdog !== null) {
      clearInterval(watchdog);
      watchdog = null;
    }
  };

  const scheduleRetry = () => {
    if (stopped || retryTimer !== null) {
      return;
    }
    // 加 ±20% 抖动：服务端重启恢复的那一刻，所有标签页不要卡在同一毫秒涌进来
    const jitter = backoff * 0.2 * (Math.random() * 2 - 1);
    retryTimer = setTimeout(
      () => {
        retryTimer = null;
        connect();
      },
      Math.max(MIN_BACKOFF / 2, backoff + jitter)
    );
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
  };

  /**
   * 看门狗：socket 还是 OPEN、但帧不再到达的「半死连接」。
   *
   * NAT 超时、笔记本休眠唤醒、VPN 断开、中间的负载均衡悄悄掐链路，
   * 这些情况浏览器收不到 close 事件，onclose 永远不触发，页面就会拿着冻结的旧值
   * 一直显示「在线」——对监控面板来说这是最坏的失败模式。
   * 服务端每秒一帧，连续 6 秒没帧就当它死了，主动关掉走重连。
   */
  const startWatchdog = () => {
    clearWatchdog();
    watchdog = setInterval(() => {
      if (stopped || !socket || socket.readyState !== WebSocket.OPEN) {
        return;
      }
      if (lastFrameAt === 0 || Date.now() - lastFrameAt < STALE_AFTER) {
        return;
      }
      setStatus('closed');
      socket.close();
    }, WATCHDOG_INTERVAL);
  };

  function connect() {
    if (stopped || socket) {
      return;
    }

    const token = getToken();
    if (!token) {
      // 还没登录（或刚登出）就不连，等下一次重试。
      scheduleRetry();
      return;
    }

    setStatus('connecting');

    const ws = new WebSocket(buildUrl());
    socket = ws;
    // 每条连接都从「没 open 过」重新开始计，否则下面的退避判断会一直命中上一条连接的时刻
    openedAt = 0;
    lastFrameAt = 0;

    ws.onopen = () => {
      openedAt = Date.now();
      ws.send(JSON.stringify({ type: 'auth', token }));
      setStatus('open');
      startWatchdog();
    };

    ws.onmessage = (event) => {
      if (typeof event.data !== 'string') {
        return;
      }
      try {
        const frame = JSON.parse(event.data) as Snapshot;
        if (frame.type === 'snapshot') {
          lastFrameAt = Date.now();
          applySnapshot(frame);
        }
      } catch {
        // 解不开的帧直接丢，不断连接——服务端只会发 snapshot，出现这种情况多半是代理插了东西。
      }
    };

    ws.onclose = (event) => {
      socket = null;
      clearWatchdog();
      setStatus('closed');

      if (stopped) {
        return;
      }

      // 只有「这条连接自己活够久」才把退避归零。
      // 注意必须判 openedAt !== 0：从没 open 过时它是 0，Date.now() - 0 永远大于任何阈值，
      // 那样握手一直失败也会每次归零，指数退避就形同虚设了。
      const lived = openedAt === 0 ? 0 : Date.now() - openedAt;
      if (lived >= STABLE_CONNECTION) {
        backoff = MIN_BACKOFF;
      }

      // 4001 是鉴权失败：token 过期或无效，退避重连没有意义。
      // 监控页是纯 WebSocket 页面，不会自己发出任何 REST 请求，
      // 也就碰不到 axios 那个「401 → 踢回登录页」的拦截器，得由这里主动触发。
      if (event.code === CLOSE_UNAUTHORIZED) {
        backoff = MAX_BACKOFF;
        onUnauthorized?.();
      }

      scheduleRetry();
    };

    ws.onerror = () => {
      // onerror 之后一定还会来一次 onclose，重连逻辑只写在 onclose 里。
      ws.close();
    };
  }

  /** 切回前台时立刻重连：后台标签页的定时器会被浏览器压到分钟级，看门狗也跟着失灵。 */
  const onVisibilityChange = () => {
    if (document.visibilityState !== 'visible' || stopped) {
      return;
    }

    // socket 还在但帧已经过期，说明它是条半死的连接，关掉重来
    if (socket) {
      if (lastFrameAt !== 0 && Date.now() - lastFrameAt >= STALE_AFTER) {
        setStatus('closed');
        socket.close();
      }
      return;
    }

    clearRetry();
    backoff = MIN_BACKOFF;
    connect();
  };

  document.addEventListener('visibilitychange', onVisibilityChange);
  connect();

  return () => {
    stopped = true;
    clearRetry();
    clearWatchdog();
    document.removeEventListener('visibilitychange', onVisibilityChange);

    if (socket) {
      // 先摘掉回调再关，免得 onclose 又排一次重连。
      socket.onopen = null;
      socket.onmessage = null;
      socket.onclose = null;
      socket.onerror = null;
      socket.close();
      socket = null;
    }
    setStatus('closed');
  };
}

// ----------------------------------------------------------------------

/** 服务端鉴权失败时用的关闭码，见 docs/protocol.md §2.1。 */
const CLOSE_UNAUTHORIZED = 4001;

/** 与页面同源；开发时 Vite 把 /api/ws 代理到 :9000（vite.config.ts）。 */
function buildUrl(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${window.location.host}/api/ws`;
}
