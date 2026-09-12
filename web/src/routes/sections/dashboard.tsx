import type { RouteObject } from 'react-router';

import { Outlet } from 'react-router';
import { lazy, Suspense } from 'react';

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
const SettingsPage = lazy(() => import('src/pages/dashboard/settings'));

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
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
];
