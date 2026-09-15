import type { GridColDef } from '@mui/x-data-grid';
import type { Inbound, CoreState } from 'src/types/proxy';
import type { ProxyObservations } from 'src/types/proxy-observation';

import useSWR from 'swr';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Tooltip from '@mui/material/Tooltip';
import { DataGrid } from '@mui/x-data-grid';
import Typography from '@mui/material/Typography';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { fetcher } from 'src/lib/axios';
import { useServers } from 'src/api/servers';
import { useCoreFiles } from 'src/api/corefiles';
import { useProxyAssignments } from 'src/api/proxy';
import { DashboardContent } from 'src/layouts/dashboard';

import { Label } from 'src/components/label';
import { FlagIcon } from 'src/components/flag-icon';
import { EmptyContent } from 'src/components/empty-content';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { getErrorMessage } from 'src/auth/utils';

import { coreStatus, countAssignments } from '../helpers';

type NodeProxy = {
  id: number;
  core?: CoreState;
  inbounds?: Inbound[];
  observed?: ProxyObservations;
  error?: string;
};

const externalInstances = (row: NodeProxy) =>
  row.observed?.instances.filter((i) => i.ownership === 'external' && !i.absent) ?? [];

async function fetchOverview([, ids]: [string, number[]]): Promise<NodeProxy[]> {
  return Promise.all(
    ids.map(async (id) => {
      try {
        const [core, inbounds, observed] = await Promise.all([
          fetcher<{ core: CoreState }>(`/api/servers/${id}/core`),
          fetcher<{ inbounds: Inbound[] }>(`/api/servers/${id}/inbounds`),
          fetcher<ProxyObservations>(`/api/servers/${id}/proxy-observations`),
        ]);
        return { id, core: core.core, inbounds: inbounds.inbounds, observed };
      } catch (err) {
        return { id, error: getErrorMessage(err) };
      }
    })
  );
}

export function ProxyListView() {
  const { servers, serversError, serversLoading, refreshServers } = useServers();
  const files = useCoreFiles();
  const assignments = useProxyAssignments();
  const overview = useSWR(
    servers.length ? ['proxy-overview', servers.map((s) => s.id)] : null,
    fetchOverview,
    { refreshInterval: 5000 }
  );
  const current = files.versions.find((v) => v.current)?.version;
  const rows = servers.map((s) => ({ ...s, ...overview.data?.find((x) => x.id === s.id) }));
  type Row = (typeof rows)[number];
  const columns: GridColDef<Row>[] = [
    {
      field: 'name',
      headerName: '节点',
      minWidth: 180,
      flex: 1,
      renderCell: ({ row }) => (
        <Box sx={{ height: 1, display: 'flex', alignItems: 'center', gap: 1 }}>
          <FlagIcon code={row.region} sx={{ width: 24 }} />
          <Typography variant="body2" noWrap>
            {row.name}
          </Typography>
        </Box>
      ),
    },
    {
      field: 'online',
      headerName: 'Agent',
      width: 90,
      valueGetter: (_, row) => row.core?.online,
      renderCell: ({ row }) =>
        row.core ? (
          <Label color={row.core.online ? 'success' : 'default'}>
            {row.core.online ? '在线' : '离线'}
          </Label>
        ) : (
          '—'
        ),
    },
    {
      field: 'status',
      headerName: '核心状态',
      width: 160,
      valueGetter: (_, row) => (row.core ? coreStatus(row.core).label : ''),
      renderCell: ({ row }) =>
        row.observed?.management === 'external' ? (
          <Label color="info">外部管理 · 只读</Label>
        ) : row.observed?.management === 'unknown' ? (
          <Label color="warning">待核验 / 升级探针</Label>
        ) : row.core ? (
          <Label color={coreStatus(row.core).color}>{coreStatus(row.core).label}</Label>
        ) : (
          '—'
        ),
    },
    {
      field: 'version',
      headerName: '已安装版本',
      width: 180,
      valueGetter: (_, row) => row.core?.installed_version ?? '',
      renderCell: ({ row }) => (
        <Box sx={{ height: 1, display: 'flex', alignItems: 'center', gap: 1 }}>
          {externalInstances(row).length
            ? externalInstances(row)
                .map((i) => `${i.core === 'xray' ? 'Xray' : 'sing-box'} ${i.version || ''}`)
                .join('、')
            : row.core?.installed_version || '—'}
          {row.core?.installed_version && current && row.core.installed_version !== current && (
            <Label color="warning">可升级</Label>
          )}
        </Box>
      ),
    },
    {
      field: 'inbounds',
      headerName: '入站数',
      width: 84,
      type: 'number',
      valueGetter: (_, row) =>
        externalInstances(row).reduce((n, i) => n + i.inbounds.length, 0) +
        (row.inbounds?.length ?? 0),
    },
    {
      field: 'users',
      headerName: '分配用户',
      description: '节点内去重人数，含停用用户',
      width: 100,
      type: 'number',
      valueGetter: (_, row) =>
        assignments.data ? countAssignments(assignments.data.subscribers, row.id) : null,
    },
    {
      field: 'error',
      headerName: '最后错误',
      minWidth: 160,
      flex: 1,
      valueGetter: (_, row) => row.error || row.core?.last_error || '',
      renderCell: ({ row }) => (
        <Tooltip title={row.error || row.core?.last_error || ''}>
          <Typography variant="body2" noWrap sx={{ lineHeight: '64px', color: 'error.main' }}>
            {row.error || row.core?.last_error || '—'}
          </Typography>
        </Tooltip>
      ),
    },
    {
      field: 'actions',
      headerName: '操作',
      width: 90,
      sortable: false,
      filterable: false,
      renderCell: ({ row }) => (
        <Button component={RouterLink} href={paths.dashboard.proxy.detail(row.id)}>
          管理
        </Button>
      ),
    },
  ];
  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs
        heading="代理"
        links={[{ name: '代理' }]}
        action={
          <Button
            onClick={() => {
              void refreshServers();
              void overview.mutate();
              void files.refresh();
              void assignments.mutate();
            }}
          >
            刷新
          </Button>
        }
        sx={{ mb: 3 }}
      />
      <Alert
        severity="info"
        sx={{ mb: 3 }}
        action={
          <Button component={RouterLink} href={paths.dashboard.settings.corefiles}>
            管理版本
          </Button>
        }
      >
        当前托管版本：{files.isLoading ? '加载中' : current || '尚未设置'}
        。切换托管版本后，需逐台执行升级。
      </Alert>
      {(serversError || files.error || assignments.error || overview.error) && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {getErrorMessage(serversError || files.error || assignments.error || overview.error)}
        </Alert>
      )}
      <Card sx={{ height: { xs: 580, md: 'calc(100vh - 330px)' }, minHeight: 400 }}>
        <DataGrid
          rows={rows}
          columns={columns}
          loading={serversLoading || overview.isLoading}
          rowHeight={64}
          disableRowSelectionOnClick
          pageSizeOptions={[10, 25, 50]}
          initialState={{ pagination: { paginationModel: { pageSize: 10 } } }}
          slots={{
            noRowsOverlay: () => (
              <EmptyContent title="还没有节点" description="先在节点页面新增节点并安装 Agent" />
            ),
          }}
        />
      </Card>
      <Typography variant="caption" color="text.secondary" sx={{ mt: 1 }}>
        分配用户包含停用用户；离线节点显示最后一次上报的核心状态。
      </Typography>
    </DashboardContent>
  );
}
