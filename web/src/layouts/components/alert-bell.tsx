import type { AlertEventsData } from 'src/types/alert';

import useSWR from 'swr';

import Badge from '@mui/material/Badge';
import Tooltip from '@mui/material/Tooltip';
import IconButton from '@mui/material/IconButton';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { fetcher } from 'src/lib/axios';

import { Iconify } from 'src/components/iconify';

export function AlertBell() {
  const { data, error } = useSWR<AlertEventsData>('/api/alert-events?open=1', fetcher, {
    refreshInterval: 30000,
  });
  const count = error ? undefined : data?.open_count;
  const label = count === undefined ? '告警数量暂不可用' : `${count} 条进行中的告警`;
  return (
    <Tooltip title={label}>
      <IconButton
        component={RouterLink}
        href={`${paths.dashboard.alerts.root}?open=1`}
        aria-label={label}
      >
        <Badge color="error" badgeContent={count === undefined ? '!' : count} max={99}>
          <Iconify icon="solar:bell-bing-bold-duotone" width={24} />
        </Badge>
      </IconButton>
    </Tooltip>
  );
}
