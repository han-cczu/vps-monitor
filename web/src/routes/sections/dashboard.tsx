import type { RouteObject } from 'react-router';

import { lazy, Suspense } from 'react';
import { Outlet, Navigate } from 'react-router';

import { paths } from 'src/routes/paths';

import { DashboardLayout } from 'src/layouts/dashboard';
import { RealtimeProvider } from 'src/ws/realtime-provider';

import { LoadingScreen } from 'src/components/loading-screen';

import { AuthGuard } from 'src/auth/guard';

import { usePathname } from '../hooks';

// ----------------------------------------------------------------------

const OverviewPage = lazy(() => import('src/pages/dashboard/overview'));
const ServersPage = lazy(() => import('src/pages/dashboard/servers'));
const ServerDetailPage = lazy(() => import('src/pages/dashboard/server-detail'));
const ProxyPage = lazy(() => import('src/pages/dashboard/proxy'));
const SubscribersPage = lazy(() => import('src/pages/dashboard/subscribers'));
const AlertsPage = lazy(() => import('src/pages/dashboard/alerts'));
const PingTasksPage = lazy(() => import('src/pages/dashboard/settings/ping-tasks'));
const SettingsAccountPage = lazy(() => import('src/pages/dashboard/settings/account'));

// ----------------------------------------------------------------------

function SuspenseOutlet() {
  const pathname = usePathname();
  return (
    <Suspense key={pathname} fallback={<LoadingScreen />}>
      <Outlet />
    </Suspense>
  );
}

const dashboardLayout = () => (
  <DashboardLayout>
    <SuspenseOutlet />
  </DashboardLayout>
);

export const dashboardRoutes: RouteObject[] = [
  {
    path: 'dashboard',
    // RealtimeProvider 放在 AuthGuard 之内：登录之后才连 WebSocket，登出即断开
    element: (
      <AuthGuard>
        <RealtimeProvider>{dashboardLayout()}</RealtimeProvider>
      </AuthGuard>
    ),
    children: [
      { element: <OverviewPage />, index: true },
      {
        path: 'servers',
        children: [
          { index: true, element: <ServersPage /> },
          { path: ':id', element: <ServerDetailPage /> },
        ],
      },
      { path: 'proxy', element: <ProxyPage /> },
      { path: 'subscribers', element: <SubscribersPage /> },
      { path: 'alerts', element: <AlertsPage /> },
      {
        path: 'settings',
        children: [
          // 设置默认进入账号页，其他设置从页内标签进入。
          { index: true, element: <Navigate to={paths.dashboard.settings.account} replace /> },
          { path: 'account', element: <SettingsAccountPage /> },
          { path: 'ping-tasks', element: <PingTasksPage /> },
        ],
      },
    ],
  },
];
