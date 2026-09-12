import type { RouteObject } from 'react-router';

import { Outlet } from 'react-router';
import { lazy, Suspense } from 'react';

import { AuthSplitLayout } from 'src/layouts/auth-split';

import { SplashScreen } from 'src/components/loading-screen';

import { GuestGuard } from 'src/auth/guard';

// ----------------------------------------------------------------------

const SignInPage = lazy(() => import('src/pages/auth/sign-in'));

// 面板是单管理员、整站登录墙，没有注册与找回密码
export const authRoutes: RouteObject[] = [
  {
    path: 'auth',
    element: (
      <Suspense fallback={<SplashScreen />}>
        <Outlet />
      </Suspense>
    ),
    children: [
      {
        path: 'sign-in',
        element: (
          <GuestGuard>
            <AuthSplitLayout slotProps={{ section: { title: 'VPS Monitor' } }}>
              <SignInPage />
            </AuthSplitLayout>
          </GuestGuard>
        ),
      },
    ],
  },
];
