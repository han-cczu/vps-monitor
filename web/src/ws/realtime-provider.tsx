import { useEffect } from 'react';

import axios, { endpoints } from 'src/lib/axios';
import { useRealtime } from 'src/store/realtime';

import { useAuthContext } from 'src/auth/hooks';
import { JWT_STORAGE_KEY } from 'src/auth/context/jwt/constant';

import { startRealtime } from './client';

// ----------------------------------------------------------------------

type Props = {
  children: React.ReactNode;
};

/**
 * 在登录墙之内维持实时通道。
 *
 * 挂在 routes/sections/dashboard.tsx 的 AuthGuard 里面：只有登录之后才连，
 * 离开 dashboard 或被 AuthGuard 踢回登录页时自动卸载。
 * 另外盯着 authenticated——登出是在原地把它翻成 false，不一定立刻发生路由跳转。
 */
export function RealtimeProvider({ children }: Props) {
  const { authenticated } = useAuthContext();

  useEffect(() => {
    if (!authenticated) {
      return undefined;
    }

    const stop = startRealtime({
      // token 直接读 sessionStorage 而不是从 context 拿：重连发生在 effect 之外的定时器里，
      // 那时候闭包里的 token 可能已经是旧的（改密后重新登录会换一个）。
      getToken: () => sessionStorage.getItem(JWT_STORAGE_KEY),

      // 服务端以 4001 拒了鉴权，多半是 token 过期。监控页是纯 WebSocket 页面，
      // 自己不发 REST 请求，也就永远碰不到 axios 那个「401 → 清 token 并跳登录页」的拦截器；
      // 这里主动打一发 /api/auth/me 把那条既有链路激活，而不是在这儿再写一套跳转逻辑。
      onUnauthorized: () => {
        axios.get(endpoints.auth.me).catch(() => {});
      },
    });

    return () => {
      stop();
      // 清空快照：下一个登录的人不该先看到上一个人的节点闪一下。
      useRealtime.getState().reset();
    };
  }, [authenticated]);

  return children;
}
