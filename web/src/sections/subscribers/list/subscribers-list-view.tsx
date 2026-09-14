import type { GridColDef } from '@mui/x-data-grid';
import type { Subscriber } from 'src/types/subscriber';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import { DataGrid } from '@mui/x-data-grid';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { DashboardContent } from 'src/layouts/dashboard';
import { useSubscriber, useSubscribers, deleteSubscriber } from 'src/api/subscribers';

import { Label } from 'src/components/label';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { getErrorMessage } from 'src/auth/utils';

import { QuotaBar } from '../quota-bar';
import { ActionDialog } from '../../proxy/shared';
import { SubLinksDialog } from '../sub-links-dialog';
import { SubscriberForm } from '../form/subscriber-form';

export function subscriberStatus(s: Subscriber) {
  if (!s.enabled) return { label: '手动停用', color: 'default' } as const;
  if (s.auto_disabled === 'quota') return { label: '超额停用', color: 'error' } as const;
  if (s.auto_disabled === 'expired') return { label: '到期停用', color: 'warning' } as const;
  return { label: '正常', color: 'success' } as const;
}
export function SubscribersListView() {
  const result = useSubscribers();
  const [edit, setEdit] = useState<number | 'new' | null>(null);
  const [links, setLinks] = useState<number | null>(null);
  const [remove, setRemove] = useState<Subscriber | null>(null);
  const detail = useSubscriber(typeof edit === 'number' ? edit : undefined);
  const columns: GridColDef<Subscriber>[] = [
    {
      field: 'name',
      headerName: '名称',
      minWidth: 150,
      flex: 1,
      renderCell: ({ row }) => (
        <Button component={RouterLink} href={paths.dashboard.subscribers.details(row.id)}>
          {row.name}
        </Button>
      ),
    },
    {
      field: 'status',
      headerName: '状态',
      width: 110,
      valueGetter: (_, row) => subscriberStatus(row).label,
      renderCell: ({ row }) => (
        <Label color={subscriberStatus(row).color}>{subscriberStatus(row).label}</Label>
      ),
    },
    {
      field: 'traffic_used',
      headerName: '已用 / 上限',
      minWidth: 200,
      flex: 1,
      renderCell: ({ row }) => <QuotaBar used={row.traffic_used} limit={row.traffic_limit} />,
    },
    {
      field: 'expire_at',
      headerName: '到期日',
      width: 115,
      valueGetter: (value) => value || '无到期',
    },
    { field: 'servers_count', headerName: '分配节点', type: 'number', width: 90 },
    {
      field: 'actions',
      headerName: '操作',
      width: 200,
      sortable: false,
      filterable: false,
      renderCell: ({ row }) => (
        <Box sx={{ display: 'flex', alignItems: 'center', height: 1 }}>
          <Button onClick={() => setLinks(row.id)}>订阅</Button>
          <Button onClick={() => setEdit(row.id)}>编辑</Button>
          <Button color="error" onClick={() => setRemove(row)}>
            删除
          </Button>
        </Box>
      ),
    },
  ];
  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs
        heading="订阅用户"
        links={[{ name: '订阅用户' }]}
        action={
          <Button variant="contained" onClick={() => setEdit('new')}>
            新增用户
          </Button>
        }
        sx={{ mb: 3 }}
      />
      {(result.error || detail.error) && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {getErrorMessage(result.error || detail.error)}
        </Alert>
      )}
      <Card sx={{ height: 620 }}>
        <DataGrid
          rows={result.data?.subscribers ?? []}
          columns={columns}
          loading={result.isLoading || detail.isLoading}
          rowHeight={64}
          disableRowSelectionOnClick
          pageSizeOptions={[10, 25, 50]}
          initialState={{ pagination: { paginationModel: { pageSize: 10 } } }}
        />
      </Card>
      {edit !== null && (edit === 'new' || detail.data) && (
        <SubscriberForm
          key={edit}
          subscriber={edit === 'new' ? undefined : detail.data?.subscriber}
          onClose={() => setEdit(null)}
          onSaved={() => setEdit(null)}
        />
      )}
      {links !== null && <SubLinksDialog id={links} onClose={() => setLinks(null)} />}
      {remove && (
        <ActionDialog
          title={`删除 ${remove.name}`}
          danger
          onClose={() => setRemove(null)}
          onConfirm={async () => {
            await deleteSubscriber(remove.id);
            setRemove(null);
          }}
        >
          将删除此用户、分配和流量记录，订阅链接和凭据失效。
        </ActionDialog>
      )}
    </DashboardContent>
  );
}
