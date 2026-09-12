import { paths } from 'src/routes/paths';

import axios from 'src/lib/axios';

import { JWT_STORAGE_KEY } from './constant';

// ----------------------------------------------------------------------

export function jwtDecode(token: string) {
  try {
    if (!token) return null;

    const parts = token.split('.');
    if (parts.length < 2) {
      throw new Error('Invalid token!');
    }

    const base64Url = parts[1];
    const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
    const decoded = JSON.parse(atob(base64));

    return decoded;
  } catch (error) {
    console.error('Error decoding token:', error);
    throw error;
  }
}

// ----------------------------------------------------------------------

export function isValidToken(accessToken: string) {
  if (!accessToken) {
    return false;
  }

  try {
    const decoded = jwtDecode(accessToken);

    if (!decoded || !('exp' in decoded)) {
      return false;
    }

    const currentTime = Date.now() / 1000;

    return decoded.exp > currentTime;
  } catch (error) {
    console.error('Error during token validation:', error);
    return false;
  }
}

// ----------------------------------------------------------------------

/** setTimeout 的延时上限（约 24.8 天），再大会被当成 0 立刻触发 */
const MAX_TIMEOUT_MS = 2_147_483_647;

/**
 * 到 exp 那一刻把会话清掉并回登录页（带 reason=expired，登录页据此提示"登录已过期"）。
 * 只在 sessionStorage 里仍是同一个 token 时才动手：用户已经退出、或重新登录换了新 token，就什么都不做。
 */
export function tokenExpired(exp: number, accessToken: string) {
  const timeLeft = exp * 1000 - Date.now();
  const delay = Math.min(Math.max(timeLeft, 0), MAX_TIMEOUT_MS);

  setTimeout(() => {
    try {
      if (sessionStorage.getItem(JWT_STORAGE_KEY) !== accessToken) {
        return;
      }
      sessionStorage.removeItem(JWT_STORAGE_KEY);
      window.location.href = `${paths.auth.signIn}?reason=expired`;
    } catch (error) {
      console.error('Error during token expiration:', error);
      throw error;
    }
  }, delay);
}

// ----------------------------------------------------------------------

export async function setSession(accessToken: string | null) {
  try {
    if (accessToken) {
      sessionStorage.setItem(JWT_STORAGE_KEY, accessToken);

      axios.defaults.headers.common.Authorization = `Bearer ${accessToken}`;

      const decodedToken = jwtDecode(accessToken); // 服务端签发，有效期 12 小时

      if (decodedToken && 'exp' in decodedToken) {
        tokenExpired(decodedToken.exp, accessToken);
      } else {
        throw new Error('Invalid access token!');
      }
    } else {
      sessionStorage.removeItem(JWT_STORAGE_KEY);
      delete axios.defaults.headers.common.Authorization;
    }
  } catch (error) {
    console.error('Error during set session:', error);
    throw error;
  }
}
