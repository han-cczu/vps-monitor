import type { GridColDef } from '@mui/x-data-grid';
import type { ServerItem } from 'src/types/server';

import { useState, useCallback } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Tooltip from '@mui/material/Tooltip';
import { DataGrid } from '@mui/x-data-grid';
import Typography from '@mui/material/Typography';

import { paths } from 'src/routes/paths';
import { useRouter } from 'src/routes/hooks';

import { daysUntil, formatPrice } from 'src/utils/format';

import { DashboardContent } from 'src/layouts/dashboard';
import { useServers, deleteServer, resetServerToken } from 'src/api/servers';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';
import { Iconify } from 'src/components/iconify';
import { FlagIcon } from 'src/components/flag-icon';
import { EmptyContent } from 'src/components/empty-content';
import { ConfirmDialog } from 'src/components/custom-dialog';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';
import { CustomGridActionsCellItem } from 'src/components/custom-data-grid';

import { getErrorMessage } from 'src/auth/utils';

import { ServerFormDialog } from '../server-form-dialog';
import { formatRegion, formatTrafficLimit } from '../utils';
import { ServerTokenDialog, type ServerTokenInfo } from '../server-token-dialog';

// ----------------------------------------------------------------------

export function ServersListView() {
  const router = useRouter();
  const { servers, serversLoading, serversError, refreshServers } = useServers();

  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<ServerItem | null>(null);
  const [tokenInfo, setTokenInfo] = useState<ServerTokenInfo | null>(null);
  const [deleting, setDeleting] = useState<ServerItem | null>(null);
  const [deletingBusy, setDeletingBusy] = useState(false);

  const openCreate = useCallback(() => {
    setEditing(null);
    setFormOpen(true);
  }, []);

  const openEdit = useCallback((server: ServerItem) => {
    setEditing(server);
    setFormOpen(true);
  }, []);

  const handleResetToken = useCallback(
    async (server: ServerItem) => {
      try {
        const result = await resetServerToken(server.id);
        setFormOpen(false);
        setTokenInfo({
          serverName: server.name,
          token: result.token,
          installCommand: result.install_command,
          reason: 'reset',
        });
        await refreshServers();
      } catch (error) {
        console.error(error);
        toast.error(getErrorMessage(error));
      }
    },
    [refreshServers]
  );

  const handleDelete = useCallback(async () => {
    if (!deleting) {
      return;
    }
    setDeletingBusy(true);
    try {
      await deleteServer(deleting.id);
      toast.success('节点已删除');
      setDeleting(null);
      await refreshServers();
    } catch (error) {
      console.error(error);
      toast.error(getErrorMessage(error));
    } finally {
      setDeletingBusy(false);
    }
  }, [deleting, refreshServers]);

  const columns: GridColDef<ServerItem>[] = [
    {
      field: 'name',
      headerName: '名称',
      flex: 1,
      minWidth: 180,
      renderCell: (params) => (
        <Box sx={{ gap: 1.5, height: 1, display: 'flex', alignItems: 'center' }}>
          <FlagIcon code={params.row.region} sx={{ width: 24, height: 24 }} />
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="body2" noWrap sx={{ lineHeight: 1.4 }}>
              {params.row.name}
            </Typography>
            {params.row.public_host && (
              <Typography
                variant="caption"
                noWrap
                component="div"
                sx={{ lineHeight: 1.4, color: 'text.secondary' }}
              >
                {params.row.public_host}
              </Typography>
            )}
          </Box>
        </Box>
      ),
    },
    {
      field: 'region',
      headerName: '地区',
      width: 104,
      renderCell: (params) => formatRegion(params.row.region) || '—',
    },
    {
      field: 'group_name',
      headerName: '分组',
      width: 104,
      renderCell: (params) => params.row.group_name || '—',
    },
    {
      field: 'tags',
      headerName: '标签',
      width: 150,
      sortable: false,
      filterable: false,
      renderCell: (params) => (
        <Box sx={{ gap: 0.5, height: 1, display: 'flex', flexWrap: 'wrap', alignItems: 'center' }}>
          {params.row.tags.length
            ? params.row.tags.map((tag) => (
                <Label key={tag} variant="soft">
                  {tag}
                </Label>
              ))
            : '—'}
        </Box>
      ),
    },
    {
      field: 'online',
      headerName: '状态',
      width: 96,
      renderCell: (params) =>
        params.row.online ? (
          <Label variant="soft" color="success">
            在线
          </Label>
        ) : (
          <Label variant="soft" color="default">
            未连接
          </Label>
        ),
    },
    {
      field: 'expire_at',
      headerName: '到期',
      width: 140,
      renderCell: (params) => <ExpireCell expireAt={params.row.expire_at} />,
    },
    {
      field: 'price',
      headerName: '价格',
      width: 116,
      renderCell: (params) =>
        formatPrice(params.row.price, params.row.currency, params.row.billing_cycle),
    },
    {
      field: 'traffic_limit',
      headerName: '流量',
      width: 96,
      renderCell: (params) => formatTrafficLimit(params.row.traffic_limit),
    },
    {
      type: 'actions',
      field: 'actions',
      headerName: ' ',
      width: 56,
      align: 'right',
      headerAlign: 'right',
      sortable: false,
      filterable: false,
      disableColumnMenu: true,
      getActions: (params) => [
        <CustomGridActionsCellItem
          key="detail"
          showInMenu
          icon={<Iconify icon="solar:eye-bold" />}
          label="详情与曲线"
          onClick={() => router.push(paths.dashboard.servers.details(params.row.id))}
        />,
        <CustomGridActionsCellItem
          key="edit"
          showInMenu
          icon={<Iconify icon="solar:pen-bold" />}
          label="编辑"
          onClick={() => openEdit(params.row)}
        />,
        <CustomGridActionsCellItem
          key="token"
          showInMenu
          icon={<Iconify icon="ic:round-vpn-key" />}
          label="重置 token"
          onClick={() => handleResetToken(params.row)}
        />,
        <CustomGridActionsCellItem
          key="delete"
          showInMenu
          icon={<Iconify icon="solar:trash-bin-trash-bold" sx={{ color: 'error.main' }} />}
          label={
            <Box component="span" sx={{ color: 'error.main' }}>
              删除
            </Box>
          }
          onClick={() => setDeleting(params.row)}
        />,
      ],
    },
  ];

  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs
        heading="节点"
        links={[{ name: '节点' }]}
        action={
          <Button
            variant="contained"
            startIcon={<Iconify icon="mingcute:add-line" />}
            onClick={openCreate}
          >
            新增节点
          </Button>
        }
        sx={{ mb: 3 }}
      />

      {serversError && (
        <Alert severity="error" sx={{ mb: 3 }}>
          {getErrorMessage(serversError)}
        </Alert>
      )}

      <Card sx={{ height: { xs: 640, md: 'calc(100vh - 280px)' }, minHeight: 480 }}>
        <DataGrid
          rows={servers}
          columns={columns}
          loading={serversLoading}
          rowHeight={64}
          getRowId={(row) => row.id}
          disableRowSelectionOnClick
          pageSizeOptions={[10, 25, 50]}
          initialState={{ pagination: { paginationModel: { pageSize: 10 } } }}
          slots={{
            noRowsOverlay: () => (
              <EmptyContent
                title="还没有节点"
                description="点右上角「新增节点」，创建后会给出一键安装命令"
              />
            ),
          }}
        />
      </Card>

      <ServerFormDialog
        open={formOpen}
        currentServer={editing}
        onClose={() => setFormOpen(false)}
        onCreated={(result) => {
          setTokenInfo({
            serverName: result.server.name,
            token: result.token,
            installCommand: result.install_command,
            reason: 'created',
          });
          refreshServers();
        }}
        onUpdated={() => refreshServers()}
        onResetToken={handleResetToken}
      />

      <ServerTokenDialog info={tokenInfo} onClose={() => setTokenInfo(null)} />

      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title="删除节点"
        content={
          <>
            确定删除「{deleting?.name}」吗？这台节点的采集数据、代理配置与订阅分配会一并删除，
            <b>不可恢复</b>。节点上的 agent 需要手动卸载。
          </>
        }
        action={
          <Button variant="contained" color="error" loading={deletingBusy} onClick={handleDelete}>
            删除
          </Button>
        }
      />
    </DashboardContent>
  );
}

// ----------------------------------------------------------------------

function ExpireCell({ expireAt }: { expireAt: string | null }) {
  const days = daysUntil(expireAt);

  if (!expireAt || days === null) {
    return <Box component="span">—</Box>;
  }

  const color = days < 0 ? 'error.main' : days <= 7 ? 'warning.main' : 'text.secondary';
  const suffix = days < 0 ? '已过期' : `${days} 天`;

  return (
    <Tooltip title={days < 0 ? '已过期' : `还有 ${days} 天到期`}>
      <Box sx={{ height: 1, display: 'flex', alignItems: 'center', gap: 0.75 }}>
        <Typography variant="body2">{expireAt}</Typography>
        <Typography variant="caption" sx={{ color }}>
          {suffix}
        </Typography>
      </Box>
    </Tooltip>
  );
}
