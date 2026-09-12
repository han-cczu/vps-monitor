import type { AxiosRequestConfig } from 'axios';

import axios from 'axios';

import { paths } from 'src/routes/paths';

import { CONFIG } from 'src/global-config';

import { JWT_STORAGE_KEY } from 'src/auth/context/jwt/constant';

// ----------------------------------------------------------------------

const axiosInstance = axios.create({
  // 留空 = 同源；开发时由 Vite 代理转发到 Go
  baseURL: CONFIG.serverUrl,
  headers: {
    'Content-Type': 'application/json',
  },
});

/**
 * 请求拦截器：每次请求都从 sessionStorage 取一次 token。
 * setSession() 只在登录那一刻设置 axios 默认头，刷新页面后要等 checkUserSession() 重设，
 * 这里兜底，避免刷新后第一批请求漏带 Authorization。
 */
axiosInstance.interceptors.request.use((config) => {
  const token = sessionStorage.getItem(JWT_STORAGE_KEY);

  if (token && !config.headers.Authorization) {
    config.headers.Authorization = `Bearer ${token}`;
  }

  return config;
});

/**
 * 响应拦截器：
 * - 服务端错误统一是 { message: string }，把它提成 Error 抛出去，页面直接展示
 * - 401 一律清 session 并回登录页；登录接口自己的 401 不跳（本来就在登录页）
 */
axiosInstance.interceptors.response.use(
  (response) => response,
  (error) => {
    const status: number | undefined = error?.response?.status;

    if (status === 401) {
      sessionStorage.removeItem(JWT_STORAGE_KEY);
      delete axiosInstance.defaults.headers.common.Authorization;

      if (!window.location.pathname.startsWith(paths.auth.signIn)) {
        window.location.href = `${paths.auth.signIn}?reason=expired`;
      }
    }

    const serverMessage: unknown = error?.response?.data?.message;
    let message: string;
    if (typeof serverMessage === 'string' && serverMessage) {
      message = serverMessage;
    } else if (status) {
      message = `请求失败（HTTP ${status}）`;
    } else {
      message = '网络错误，请稍后重试';
    }

    console.error('Axios error:', message);
    return Promise.reject(new Error(message));
  }
);

export default axiosInstance;

// ----------------------------------------------------------------------

export const fetcher = async <T = unknown>(
  args: string | [string, AxiosRequestConfig]
): Promise<T> => {
  try {
    const [url, config] = Array.isArray(args) ? args : [args, {}];

    const res = await axiosInstance.get<T>(url, config);

    return res.data;
  } catch (error) {
    console.error('Fetcher failed:', error);
    throw error;
  }
};

// ----------------------------------------------------------------------

export const endpoints = {
  health: '/api/health',
  auth: {
    me: '/api/auth/me',
    signIn: '/api/auth/sign-in',
    password: '/api/auth/password',
  },
  servers: {
    root: '/api/servers',
    byId: (id: number) => `/api/servers/${id}`,
    token: (id: number) => `/api/servers/${id}/token`,
  },
} as const;
