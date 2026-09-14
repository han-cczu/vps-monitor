import type { GridColDef } from '@mui/x-data-grid';
import type { AlertEvent, AlertEventsData } from 'src/types/alert';

import useSWR from 'swr';
import { useState } from 'react';
import { useSearchParams } from 'react-router';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Link from '@mui/material/Link';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Switch from '@mui/material/Switch';
import Tooltip from '@mui/material/Tooltip';
import { DataGrid } from '@mui/x-data-grid';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import FormControlLabel from '@mui/material/FormControlLabel';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { useServers } from 'src/api/servers';
import axios, { fetcher } from 'src/lib/axios';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

import { RULE_INFO } from './shared';

export function EventsView() {
  const [search] = useSearchParams();
  const [openOnly, setOpenOnly] = useState(search.get('open') === '1');
  const [kind, setKind] = useState('');
  const [target, setTarget] = useState('');
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState<number | null>(null);
  const query = new URLSearchParams({
    page: String(page + 1),
    open: openOnly ? '1' : '0',
    kind,
    target,
  });
  const { data, error, isLoading, mutate } = useSWR<AlertEventsData>(
    `/api/alert-events?${query}`,
    fetcher,
    { refreshInterval: 30000, keepPreviousData: true }
  );
  const { servers } = useServers();
  const { data: subscribers } = useSWR<{ subscribers: { id: number; name: string }[] }>(
    '/api/subscribers',
    fetcher,
    { refreshInterval: 60000 }
  );
  const targetName = (e: AlertEvent) =>
    (e.target_type === 'server'
      ? servers.find((s) => s.id === e.target_id)?.name
      : subscribers?.subscribers.find((s) => s.id === e.target_id)?.name) ??
    `${e.target_type === 'server' ? '节点' : '用户'} #${e.target_id}`;
  const resolve = async (id: number) => {
    setBusy(id);
    try {
      await axios.post(`/api/alert-events/${id}/resolve`);
      await mutate();
      toast.success('告警已关闭；条件持续时可能再次触发');
    } catch (e) {
      toast.error(getErrorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const columns: GridColDef<AlertEvent>[] = [
    {
      field: 'fired_at',
      headerName: '时间',
      width: 180,
      valueFormatter: (value: number) => new Date(value * 1000).toLocaleString('zh-CN'),
    },
    {
      field: 'level',
      headerName: '级别',
      width: 90,
      renderCell: ({ row }) => (
        <Label
          color={row.level === 'critical' ? 'error' : row.level === 'warning' ? 'warning' : 'info'}
        >
          {{ critical: '严重', warning: '警告', info: '信息' }[row.level]}
        </Label>
      ),
    },
    {
      field: 'rule_kind',
      headerName: '规则',
      width: 160,
      valueFormatter: (value: string) => RULE_INFO[value]?.name ?? value,
    },
    {
      field: 'target_id',
      headerName: '目标',
      width: 160,
      renderCell: ({ row }) => (
        <Link
          component={RouterLink}
          href={
            row.target_type === 'server'
              ? paths.dashboard.servers.details(row.target_id)
              : paths.dashboard.subscribers.details(row.target_id)
          }
        >
          {targetName(row)}
        </Link>
      ),
    },
    {
      field: 'title',
      headerName: '标题 / 详情',
      minWidth: 240,
      flex: 1,
      renderCell: ({ row }) => (
        <Tooltip title={<Box sx={{ whiteSpace: 'pre-wrap' }}>{row.message}</Box>}>
          <Box sx={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{row.title}</Box>
        </Tooltip>
      ),
    },
    {
      field: 'resolved_at',
      headerName: '状态',
      width: 110,
      renderCell: ({ row }) => (
        <Label color={row.resolved_at === null ? 'error' : 'default'}>
          {row.resolved_at === null
            ? '进行中'
            : row.resolved_at === row.fired_at
              ? '已记录'
              : '已关闭'}
        </Label>
      ),
    },
    {
      field: 'notified_at',
      headerName: '通知',
      width: 160,
      renderCell: ({ row }) => (
        <Tooltip
          title={
            row.notified_at
              ? new Date(row.notified_at * 1000).toLocaleString('zh-CN')
              : '可能处于冷却、无渠道、待重试或发送失败；不会把未确认的发送视为成功。'
          }
        >
          <Box>{row.notified_at ? '已送达' : '未确认送达'}</Box>
        </Tooltip>
      ),
    },
    {
      field: 'actions',
      headerName: '操作',
      width: 80,
      sortable: false,
      renderCell: ({ row }) =>
        row.resolved_at === null ? (
          <Button
            size="small"
            onClick={() => resolve(row.id)}
            loading={busy === row.id}
            disabled={busy !== null}
          >
            关闭
          </Button>
        ) : null,
    },
  ];
  return (
    <>
      <Box sx={{ display: 'flex', gap: 2, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
        <FormControlLabel
          label="仅进行中"
          control={
            <Switch
              checked={openOnly}
              onChange={(_, checked) => {
                setOpenOnly(checked);
                setPage(0);
              }}
            />
          }
        />
        <TextField
          select
          size="small"
          label="规则"
          value={kind}
          onChange={(e) => {
            setKind(e.target.value);
            setPage(0);
          }}
          sx={{ minWidth: 200 }}
        >
          <MenuItem value="">全部规则</MenuItem>
          {Object.entries(RULE_INFO).map(([key, info]) => (
            <MenuItem value={key} key={key}>
              {info.name}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          select
          size="small"
          label="目标"
          value={target}
          onChange={(e) => {
            setTarget(e.target.value);
            setPage(0);
          }}
          sx={{ minWidth: 200 }}
        >
          <MenuItem value="">全部目标</MenuItem>
          {servers.map((s) => (
            <MenuItem value={`server:${s.id}`} key={`server:${s.id}`}>
              节点 · {s.name}
            </MenuItem>
          ))}
          {subscribers?.subscribers.map((s) => (
            <MenuItem value={`subscriber:${s.id}`} key={`subscriber:${s.id}`}>
              用户 · {s.name}
            </MenuItem>
          ))}
        </TextField>
      </Box>
      {error && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {getErrorMessage(error)}
        </Alert>
      )}
      <Card sx={{ height: 620 }}>
        <DataGrid
          rows={data?.events ?? []}
          columns={columns}
          loading={isLoading}
          paginationMode="server"
          rowCount={data?.total ?? 0}
          paginationModel={{ page, pageSize: 50 }}
          onPaginationModelChange={(model) => setPage(model.page)}
          pageSizeOptions={[50]}
          disableRowSelectionOnClick
          disableColumnFilter
          disableColumnSorting
        />
      </Card>
    </>
  );
}
