// ----------------------------------------------------------------------

const ROOTS = {
  AUTH: '/auth',
  DASHBOARD: '/dashboard',
};

// ----------------------------------------------------------------------

export const paths = {
  // AUTH（只有一种方式：面板自己的 JWT 登录）
  auth: {
    signIn: `${ROOTS.AUTH}/sign-in`,
  },
  // DASHBOARD
  dashboard: {
    root: ROOTS.DASHBOARD,
    /** 监控总览 */
    overview: ROOTS.DASHBOARD,
    /** 节点 */
    servers: {
      root: `${ROOTS.DASHBOARD}/servers`,
      new: `${ROOTS.DASHBOARD}/servers/new`,
      details: (id: string | number) => `${ROOTS.DASHBOARD}/servers/${id}`,
      edit: (id: string | number) => `${ROOTS.DASHBOARD}/servers/${id}/edit`,
      proxy: (id: string | number) => `${ROOTS.DASHBOARD}/servers/${id}/proxy`,
    },
    /** 代理 */
    proxy: {
      root: `${ROOTS.DASHBOARD}/proxy`,
    },
    /** 订阅用户 */
    subscribers: {
      root: `${ROOTS.DASHBOARD}/subscribers`,
      new: `${ROOTS.DASHBOARD}/subscribers/new`,
      details: (id: string | number) => `${ROOTS.DASHBOARD}/subscribers/${id}`,
    },
    /** 告警 */
    alerts: {
      root: `${ROOTS.DASHBOARD}/alerts`,
    },
    /** 设置 */
    settings: {
      root: `${ROOTS.DASHBOARD}/settings`,
      account: `${ROOTS.DASHBOARD}/settings/account`,
    },
  },
};
