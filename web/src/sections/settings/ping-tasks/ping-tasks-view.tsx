import type { GridColDef } from '@mui/x-data-grid';
import type { PingTask } from 'src/types/ping';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import { DataGrid } from '@mui/x-data-grid';
import Typography from '@mui/material/Typography';

import { usePingTasks, deletePingTask } from 'src/api/ping';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';
import { ConfirmDialog } from 'src/components/custom-dialog';

import { getErrorMessage } from 'src/auth/utils';

import { PingTaskFormDialog } from './ping-task-form-dialog';

export function PingTasksView() {
  const { tasks, error, isLoading, refresh } = usePingTasks();
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<PingTask | null>(null);
  const [deleting, setDeleting] = useState<PingTask | null>(null);
  const [busy, setBusy] = useState(false);
  const remove = async () => {
    if (!deleting) {
      return;
    }
    setBusy(true);
    try {
      await deletePingTask(deleting.id);
      toast.success('任务及历史记录已删除');
      setDeleting(null);
      await refresh();
    } catch (err) {
      toast.error(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  const columns: GridColDef<PingTask>[] = [
    { field: 'name', headerName: '名称', minWidth: 130, flex: 1 },
    {
      field: 'kind',
      headerName: '类型',
      width: 90,
      valueFormatter: (value: string) => value.toUpperCase(),
    },
    { field: 'target', headerName: '探测目标', minWidth: 180, flex: 1 },
    { field: 'interval_sec', headerName: '间隔（秒）', width: 110 },
    {
      field: 'server_ids',
      headerName: '作用范围',
      width: 130,
      sortable: false,
      valueFormatter: (value: number[] | null) =>
        value === null ? '全部节点' : `${value.length} 台节点`,
    },
    {
      field: 'enabled',
      headerName: '状态',
      width: 90,
      renderCell: ({ row }) => (
        <Label color={row.enabled ? 'success' : 'default'}>{row.enabled ? '启用' : '禁用'}</Label>
      ),
    },
    { field: 'sort_order', headerName: '排序', width: 80 },
    {
      field: 'actions',
      headerName: '操作',
      width: 150,
      sortable: false,
      filterable: false,
      renderCell: ({ row }) => (
        <Box sx={{ display: 'flex', alignItems: 'center', height: 1 }}>
          <Button
            size="small"
            onClick={() => {
              setEditing(row);
              setFormOpen(true);
            }}
          >
            编辑
          </Button>
          <Button size="small" color="error" onClick={() => setDeleting(row)}>
            删除
          </Button>
        </Box>
      ),
    },
  ];
  return (
    <>
      <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 2, mb: 3 }}>
        <Box sx={{ flex: 1, minWidth: 220 }}>
          <Typography variant="h6">Ping 任务</Typography>
          <Typography variant="body2" color="text.secondary">
            配置 ICMP / TCP 探测，查看节点延迟与丢包。
          </Typography>
        </Box>
        <Button
          variant="contained"
          onClick={() => {
            setEditing(null);
            setFormOpen(true);
          }}
        >
          新建任务
        </Button>
      </Box>
      {error && (
        <Alert severity="error" sx={{ mb: 2 }}>
          任务加载失败，请稍后重试。
        </Alert>
      )}
      <Card sx={{ minWidth: 0, height: 520 }}>
        <DataGrid
          rows={tasks}
          columns={columns}
          loading={isLoading}
          disableRowSelectionOnClick
          pageSizeOptions={[10, 25, 50]}
          initialState={{ pagination: { paginationModel: { pageSize: 10 } } }}
          localeText={{ noRowsLabel: '暂无 Ping 任务' }}
        />
      </Card>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 2 }}>
        在线节点会自动接收变更；离线节点在重新连接后接收最新配置。
      </Typography>
      {formOpen && (
        <PingTaskFormDialog
          key={editing?.id ?? 'new'}
          task={editing}
          onClose={() => setFormOpen(false)}
          onSaved={refresh}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onClose={() => {
          if (!busy) {
            setDeleting(null);
          }
        }}
        title="删除 Ping 任务"
        content={`删除「${deleting?.name ?? ''}」及其全部历史记录？此操作无法撤销。`}
        action={
          <Button color="error" variant="contained" loading={busy} onClick={remove}>
            删除
          </Button>
        }
      />
    </>
  );
}
