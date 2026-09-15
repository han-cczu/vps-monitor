import type { ServerItem, TrafficPeriod } from 'src/types/server';

import { useState } from 'react';
import useSWR, { useSWRConfig } from 'swr';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Table from '@mui/material/Table';
import Button from '@mui/material/Button';
import TableRow from '@mui/material/TableRow';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import Typography from '@mui/material/Typography';
import TableContainer from '@mui/material/TableContainer';
import CircularProgress from '@mui/material/CircularProgress';

import { formatBytes, formatPrice, formatPanelDate } from 'src/utils/format';

import axios, { fetcher } from 'src/lib/axios';
import { useServer } from 'src/store/realtime';

import { TrafficRow } from 'src/sections/monitor/card-rows';

import { getErrorMessage } from 'src/auth/utils';

import { ServerFormDialog } from '../server-form-dialog';
import { TrafficCalibrationDialog } from './traffic-calibration-dialog';

export function BillingPanel({ server }: { server: ServerItem }) {
  const [calibrating, setCalibrating] = useState(false);
  const [editing, setEditing] = useState<ServerItem | null>(null);
  const [editLoading, setEditLoading] = useState(false);
  const [editError, setEditError] = useState('');
  const { mutate } = useSWRConfig();
  const edit = async () => {
    setEditLoading(true);
    setEditError('');
    try {
      const response = await axios.get<{ server: ServerItem }>(`/api/servers/${server.id}`);
      setEditing(response.data.server);
    } catch (err) {
      setEditError(getErrorMessage(err));
    } finally {
      setEditLoading(false);
    }
  };
  const live = useServer(server.id);
  const { data, error, isLoading } = useSWR<TrafficPeriod[]>(
    `/api/servers/${server.id}/traffic?months=12`,
    fetcher,
    { refreshInterval: 60000 }
  );
  const date = formatPanelDate;
  const current = live?.traffic;
  return (
    <Card sx={{ p: 3 }}>
      <Box
        sx={{
          mb: 2,
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 2,
        }}
      >
        <Typography variant="h6">套餐与流量</Typography>
        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
          <Button variant="outlined" loading={editLoading} onClick={edit}>
            编辑套餐与周期
          </Button>
          <Button variant="outlined" onClick={() => setCalibrating(true)}>
            校准本期流量
          </Button>
        </Box>
      </Box>
      {editError && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {editError}
        </Alert>
      )}
      {editing && (
        <ServerFormDialog
          open
          currentServer={editing}
          initialTab="plan"
          onClose={() => setEditing(null)}
          onCreated={() => {}}
          onUpdated={() => {
            void Promise.allSettled([
              mutate('/api/servers'),
              mutate(`/api/servers/${server.id}`),
              mutate(`/api/servers/${server.id}/traffic?months=12`),
            ]);
          }}
        />
      )}
      {calibrating && (
        <TrafficCalibrationDialog
          serverId={server.id}
          serverName={server.name}
          onClose={() => setCalibrating(false)}
        />
      )}
      <Box
        sx={{
          display: 'grid',
          gap: 2,
          mb: 3,
          gridTemplateColumns: { xs: '1fr 1fr', md: 'repeat(4, 1fr)' },
        }}
      >
        <Item
          label="价格 / 周期"
          value={formatPrice(server.price, server.currency, server.billing_cycle)}
        />
        <Item label="套餐到期日" value={live?.expire_at ?? server.expire_at ?? '—'} />
        <Item
          label="自动顺延"
          value={server.auto_renew && server.billing_cycle !== 'once' ? '已开启' : '关闭'}
        />
        <Item
          label="流量重置周期"
          value={
            server.traffic_reset_mode === 'days'
              ? '每 30 天'
              : `每月 ${server.traffic_reset_day} 日（月末不足则最后一天）`
          }
        />
      </Box>
      {current && (
        <Box sx={{ mb: 3 }}>
          <Typography variant="body2" sx={{ mb: 1 }}>
            本期流量开始：{date(current.period_start)} · 下次重置：
            {date(current.period_end_expected)} ·{' '}
            {{ in: '入站', out: '出站', sum: '双向合计', max: '取较大者' }[current.mode]}
          </Typography>
          <TrafficRow traffic={current} />
        </Box>
      )}
      <Typography variant="subtitle2" sx={{ mb: 1 }}>
        近 12 期
      </Typography>
      {error && <Alert severity="error">{getErrorMessage(error)}</Alert>}
      {isLoading ? (
        <CircularProgress size={24} aria-label="加载账期" />
      ) : (
        <TableContainer>
          <Table size="small" aria-label="流量账期历史">
            <TableHead>
              <TableRow>
                {['流量周期开始', '流量周期结束', '入站', '出站', '计费用量'].map((label) => (
                  <TableCell key={label}>{label}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {(data ?? []).map((period) => (
                <TableRow key={period.period_start}>
                  <TableCell>{date(period.period_start)}</TableCell>
                  <TableCell>
                    {period.period_end ? date(period.period_end) : '当前流量周期'}
                  </TableCell>
                  <TableCell>{formatBytes(period.in)}</TableCell>
                  <TableCell>{formatBytes(period.out)}</TableCell>
                  <TableCell>{formatBytes(period.used)}</TableCell>
                </TableRow>
              ))}
              {!data?.length && !error && (
                <TableRow>
                  <TableCell colSpan={5}>暂无账期记录</TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </TableContainer>
      )}
    </Card>
  );
}

function Item({ label, value }: { label: string; value: string }) {
  return (
    <Box>
      <Typography variant="caption" color="text.secondary">
        {label}
      </Typography>
      <Typography variant="body2">{value}</Typography>
    </Box>
  );
}
