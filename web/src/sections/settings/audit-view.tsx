import type { GridColDef } from '@mui/x-data-grid';
import type { AuditEntry } from 'src/api/settings';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import { DataGrid } from '@mui/x-data-grid';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import DialogTitle from '@mui/material/DialogTitle';
import useMediaQuery from '@mui/material/useMediaQuery';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';

import { useAudit } from 'src/api/settings';

import { getErrorMessage } from 'src/auth/utils';

const pretty = (value: string) => {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value || '—';
  }
};
export function AuditView() {
  const small = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const [pagination, setPagination] = useState({ page: 0, pageSize: 25 });
  const [draft, setDraft] = useState({ actor: '', action: '', target_type: '', from: '', to: '' });
  const [filters, setFilters] = useState(draft);
  const [selected, setSelected] = useState<AuditEntry | null>(null);
  const query = new URLSearchParams({
    page: String(pagination.page + 1),
    size: String(pagination.pageSize),
  });
  Object.entries(filters).forEach(([key, value]) => {
    if (value)
      query.set(key, key === 'from' || key === 'to' ? new Date(value).toISOString() : value);
  });
  const { data, error, isLoading } = useAudit(query);
  const columns: GridColDef<AuditEntry>[] = [
    {
      field: 'ts',
      headerName: '时间',
      width: 180,
      valueFormatter: (v: number) => new Date(v * 1000).toLocaleString(),
    },
    { field: 'actor', headerName: '操作者', width: 115 },
    { field: 'action', headerName: '动作', minWidth: 175, flex: 1 },
    { field: 'target_type', headerName: '目标类型', width: 110 },
    { field: 'target_id', headerName: '目标', width: 110 },
    { field: 'ip', headerName: '来源 IP', width: 150 },
    {
      field: 'detail',
      headerName: '详情',
      width: 80,
      sortable: false,
      filterable: false,
      renderCell: ({ row }) => (
        <Button size="small" onClick={() => setSelected(row)}>
          查看
        </Button>
      ),
    },
  ];
  return (
    <Stack spacing={2}>
      <Card sx={{ p: 2 }}>
        <Box
          component="form"
          onSubmit={(e) => {
            e.preventDefault();
            setFilters(draft);
            setPagination((p) => ({ ...p, page: 0 }));
          }}
          sx={{
            display: 'grid',
            gap: 2,
            gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr', lg: 'repeat(3, 1fr)' },
          }}
        >
          {(['actor', 'action', 'target_type'] as const).map((key, index) => (
            <TextField
              key={key}
              label={['操作者（精确匹配）', '动作（精确匹配）', '目标类型'][index]}
              value={draft[key]}
              onChange={(e) => setDraft((d) => ({ ...d, [key]: e.target.value }))}
            />
          ))}
          {(['from', 'to'] as const).map((key) => (
            <TextField
              key={key}
              type="datetime-local"
              label={key === 'from' ? '开始时间（本机时区）' : '结束时间（本机时区）'}
              value={draft[key]}
              onChange={(e) => setDraft((d) => ({ ...d, [key]: e.target.value }))}
              slotProps={{ inputLabel: { shrink: true } }}
            />
          ))}
          <Button variant="contained" type="submit">
            筛选
          </Button>
        </Box>
      </Card>
      {error && <Alert severity="error">{getErrorMessage(error)}</Alert>}
      <Card sx={{ height: 580, minWidth: 0 }}>
        <DataGrid
          rows={data?.items ?? []}
          columns={columns}
          rowCount={data?.total ?? 0}
          loading={isLoading}
          paginationMode="server"
          paginationModel={pagination}
          onPaginationModelChange={setPagination}
          pageSizeOptions={[25, 50, 100]}
          columnVisibilityModel={
            small
              ? { ts: false, actor: false, target_type: false, target_id: false, ip: false }
              : {}
          }
          disableRowSelectionOnClick
          disableColumnFilter
          disableColumnSorting
        />
      </Card>
      <Dialog
        open={!!selected}
        onClose={() => setSelected(null)}
        fullScreen={small}
        fullWidth
        maxWidth="lg"
      >
        <DialogTitle>审计详情 · {selected?.action}</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ mb: 2 }}>
            {selected &&
              `${new Date(selected.ts * 1000).toLocaleString()} · ${selected.actor} · ${selected.ip} · ${selected.target_type}/${selected.target_id}`}
          </Typography>
          <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, gap: 2 }}>
            {(['before', 'after'] as const).map((key) => (
              <Box key={key} sx={{ minWidth: 0 }}>
                <Typography variant="subtitle2">
                  {key === 'before' ? '修改前' : '修改后'}
                </Typography>
                <Box
                  component="pre"
                  sx={{
                    p: 2,
                    bgcolor: 'background.neutral',
                    whiteSpace: 'pre-wrap',
                    overflowWrap: 'anywhere',
                    fontSize: 12,
                  }}
                >
                  {pretty(selected?.[key] ?? '')}
                </Box>
              </Box>
            ))}
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setSelected(null)}>关闭</Button>
        </DialogActions>
      </Dialog>
    </Stack>
  );
}
