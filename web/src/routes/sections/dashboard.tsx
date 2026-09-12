import type { RouteObject } from 'react-router';

import { lazy, Suspense } from 'react';
import { Outlet, Navigate } from 'react-router';

import { paths } from 'src/routes/paths';

import { DashboardLayout } from 'src/layouts/dashboard';

import { LoadingScreen } from 'src/components/loading-screen';

import { AuthGuard } from 'src/auth/guard';

import { usePathname } from '../hooks';

// ----------------------------------------------------------------------

const OverviewPage = lazy(() => import('src/pages/dashboard/overview'));
const ServersPage = lazy(() => import('src/pages/dashboard/servers'));
const ProxyPage = lazy(() => import('src/pages/dashboard/proxy'));
const SubscribersPage = lazy(() => import('src/pages/dashboard/subscribers'));
const AlertsPage = lazy(() => import('src/pages/dashboard/alerts'));
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
    element: <AuthGuard>{dashboardLayout()}</AuthGuard>,
    children: [
      { element: <OverviewPage />, index: true },
      { path: 'servers', element: <ServersPage /> },
      { path: 'proxy', element: <ProxyPage /> },
      { path: 'subscribers', element: <SubscribersPage /> },
      { path: 'alerts', element: <AlertsPage /> },
      {
        path: 'settings',
        children: [
          // 设置目前只有"账号"一页，进来直接跳过去；步骤 20 加 TOTP / 审计 / 备份后再做总览
          { index: true, element: <Navigate to={paths.dashboard.settings.account} replace /> },
          { path: 'account', element: <SettingsAccountPage /> },
        ],
      },
    ],
  },
];
