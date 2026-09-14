import type { DashboardContentProps } from 'src/layouts/dashboard';

import { removeLastSlash } from 'minimal-shared/utils';

import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';

import { paths } from 'src/routes/paths';
import { usePathname } from 'src/routes/hooks';
import { RouterLink } from 'src/routes/components';

import { DashboardContent } from 'src/layouts/dashboard';

import { Iconify } from 'src/components/iconify';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

// ----------------------------------------------------------------------

// 设置共享导航；TOTP 在步骤 20 加进来。
const NAV_ITEMS = [
  {
    label: '订阅模板',
    href: paths.dashboard.settings.subscription,
    icon: <Iconify width={24} icon="solar:list-bold" />,
  },
  {
    label: '代理核心',
    href: paths.dashboard.settings.corefiles,
    icon: <Iconify width={24} icon="solar:list-bold" />,
  },
  {
    label: 'Ping 任务',
    href: paths.dashboard.settings.pingTasks,
    icon: <Iconify width={24} icon="solar:list-bold" />,
  },
  {
    label: '安全',
    icon: <Iconify width={24} icon="ic:round-vpn-key" />,
    href: paths.dashboard.settings.account,
  },
];

// ----------------------------------------------------------------------

export function AccountLayout({ children, ...other }: DashboardContentProps) {
  const pathname = usePathname();

  return (
    <DashboardContent {...other}>
      <CustomBreadcrumbs
        heading="设置"
        links={[
          { name: '设置', href: paths.dashboard.settings.root },
          {
            name:
              NAV_ITEMS.find((item) => item.href === removeLastSlash(pathname))?.label ?? '账号',
          },
        ]}
        sx={{ mb: 3 }}
      />

      <Tabs variant="scrollable" value={removeLastSlash(pathname)} sx={{ mb: { xs: 3, md: 5 } }}>
        {NAV_ITEMS.map((tab) => (
          <Tab
            component={RouterLink}
            key={tab.href}
            label={tab.label}
            icon={tab.icon}
            value={tab.href}
            href={tab.href}
          />
        ))}
      </Tabs>

      {children}
    </DashboardContent>
  );
}
