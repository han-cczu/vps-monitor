import type { ServerItem, TrafficPeriod } from 'src/types/server';

import useSWR from 'swr';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Table from '@mui/material/Table';
import TableRow from '@mui/material/TableRow';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import Typography from '@mui/material/Typography';
import TableContainer from '@mui/material/TableContainer';
import CircularProgress from '@mui/material/CircularProgress';

import { formatBytes, formatPrice, formatPanelDate } from 'src/utils/format';

import { fetcher } from 'src/lib/axios';
import { useServer } from 'src/store/realtime';

import { TrafficRow } from 'src/sections/monitor/card-rows';

import { getErrorMessage } from 'src/auth/utils';

export function BillingPanel({ server }: { server: ServerItem }) {
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
      <Typography variant="h6" sx={{ mb: 2 }}>
        账单与流量
      </Typography>
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
        <Item label="到期日" value={live?.expire_at ?? server.expire_at ?? '—'} />
        <Item
          label="自动顺延"
          value={server.auto_renew && server.billing_cycle !== 'once' ? '已开启' : '关闭'}
        />
        <Item
          label="流量重置"
          value={`每月 ${server.traffic_reset_day} 日（月末不足则最后一天）`}
        />
      </Box>
      {current && (
        <Box sx={{ mb: 3 }}>
          <Typography variant="body2" sx={{ mb: 1 }}>
            本期 {date(current.period_start)} — {date(current.period_end_expected)} ·{' '}
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
                {['账期开始', '账期结束', '入站', '出站', '计费用量'].map((label) => (
                  <TableCell key={label}>{label}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {(data ?? []).map((period) => (
                <TableRow key={period.period_start}>
                  <TableCell>{date(period.period_start)}</TableCell>
                  <TableCell>{period.period_end ? date(period.period_end) : '当前账期'}</TableCell>
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
