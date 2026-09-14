import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';

import { paths } from 'src/routes/paths';
import { usePathname } from 'src/routes/hooks';
import { RouterLink } from 'src/routes/components';

import { DashboardContent } from 'src/layouts/dashboard';

import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { RulesView } from './rules-view';
import { EventsView } from './events-view';
import { ChannelsView } from './channels-view';

export function AlertsView() {
  const pathname = usePathname();
  const current = pathname.endsWith('/rules')
    ? 'rules'
    : pathname.endsWith('/channels')
      ? 'channels'
      : 'events';
  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs heading="告警" links={[{ name: '告警' }]} sx={{ mb: 2 }} />
      <Tabs value={current} sx={{ mb: 3 }} aria-label="告警管理">
        <Tab
          value="events"
          label="事件"
          component={RouterLink}
          href={paths.dashboard.alerts.root}
        />
        <Tab
          value="rules"
          label="规则"
          component={RouterLink}
          href={`${paths.dashboard.alerts.root}/rules`}
        />
        <Tab
          value="channels"
          label="通知渠道"
          component={RouterLink}
          href={`${paths.dashboard.alerts.root}/channels`}
        />
      </Tabs>
      {current === 'rules' ? (
        <RulesView />
      ) : current === 'channels' ? (
        <ChannelsView />
      ) : (
        <EventsView />
      )}
    </DashboardContent>
  );
}
