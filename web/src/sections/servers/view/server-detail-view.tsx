import type { HistoryRange } from 'src/types/history';

import { useState } from 'react';
import { useParams } from 'react-router';

import Box from '@mui/material/Box';
import Tab from '@mui/material/Tab';
import Card from '@mui/material/Card';
import Tabs from '@mui/material/Tabs';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Typography from '@mui/material/Typography';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { useHistory } from 'src/api/history';
import { useServers } from 'src/api/servers';
import { DashboardContent } from 'src/layouts/dashboard';

import { LoadingScreen } from 'src/components/loading-screen';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { ServerCard } from 'src/sections/monitor/server-card';

import { HISTORY_RANGES } from 'src/types/history';

import { HistoryCharts } from '../detail/history-charts';
import { HostInfoPanel } from '../detail/host-info-panel';

// ----------------------------------------------------------------------

/** 节点详情：实时卡片 + 主机信息 + 历史曲线。 */
export function ServerDetailView() {
  const { id } = useParams();
  const serverID = Number(id);

  const [range, setRange] = useState<HistoryRange>('24h');

  // 节点的配置与主机信息走 REST；实时数值由卡片自己从 WebSocket store 订阅。
  const { servers, serversLoading } = useServers();
  const server = servers.find((item) => item.id === serverID);

  const { history, historyLoading } = useHistory(serverID, range);

  if (serversLoading) {
    return <LoadingScreen />;
  }

  if (!server) {
    return (
      <DashboardContent maxWidth="xl">
        <Alert
          severity="error"
          action={
            <Button component={RouterLink} href={paths.dashboard.servers.root} size="small">
              回到节点列表
            </Button>
          }
        >
          {`节点 ${id} 不存在，可能已经被删除。`}
        </Alert>
      </DashboardContent>
    );
  }

  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs
        heading={server.name}
        links={[
          { name: '监控', href: paths.dashboard.root },
          { name: '节点', href: paths.dashboard.servers.root },
          { name: server.name },
        ]}
        sx={{ mb: { xs: 3, md: 4 } }}
      />

      <Box sx={{ gap: 3, display: 'flex', flexDirection: 'column' }}>
        <Box
          sx={{
            gap: 3,
            display: 'grid',
            gridTemplateColumns: { xs: '1fr', lg: 'minmax(320px, 380px) 1fr' },
          }}
        >
          {/* 左边直接复用总览页的卡片：同一份实时数据，没必要再写一遍 */}
          <ServerCard id={serverID} />
          <HostInfoPanel host={server.host} />
        </Box>

        <Card sx={{ px: 2.5, pt: 1 }}>
          <Box sx={{ gap: 2, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <Tabs value={range} onChange={(_, value: HistoryRange) => setRange(value)}>
              {HISTORY_RANGES.map((item) => (
                <Tab key={item.value} value={item.value} label={item.label} />
              ))}
            </Tabs>

            <Typography variant="caption" sx={{ color: 'text.secondary' }}>
              {history?.step === 3600 ? '每小时一个点' : '每分钟一个点'}
            </Typography>
          </Box>
        </Card>

        <HistoryCharts history={history} loading={historyLoading} />
      </Box>
    </DashboardContent>
  );
}
