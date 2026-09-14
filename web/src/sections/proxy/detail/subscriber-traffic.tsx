import useSWR from 'swr';

import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Table from '@mui/material/Table';
import Button from '@mui/material/Button';
import TableRow from '@mui/material/TableRow';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableBody from '@mui/material/TableBody';
import Typography from '@mui/material/Typography';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { fetcher } from 'src/lib/axios';

import { Label } from 'src/components/label';

import { getErrorMessage } from 'src/auth/utils';

import { bytesText } from '../../subscribers/quota-bar';

type Row = { id: number; name: string; kind: string; up: number; down: number };
export function NodeSubscriberTraffic({ serverId }: { serverId: number }) {
  const result = useSWR<{ subscribers: Row[] }>(
    `/api/servers/${serverId}/subscriber-traffic`,
    fetcher,
    { refreshInterval: 10000 }
  );
  return (
    <Card sx={{ p: 3, overflowX: 'auto' }}>
      <Typography variant="h6" sx={{ mb: 2 }}>
        当前账期 · 用户分账
      </Typography>
      {result.error && <Alert severity="error">{getErrorMessage(result.error)}</Alert>}
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>用户</TableCell>
            <TableCell>上行</TableCell>
            <TableCell>下行</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {result.data?.subscribers.map((row) => (
            <TableRow key={row.id}>
              <TableCell>
                <Button component={RouterLink} href={paths.dashboard.subscribers.details(row.id)}>
                  {row.name}
                </Button>
                {row.kind === 'relay' && <Label color="info">中转</Label>}
              </TableCell>
              <TableCell>{bytesText(row.up)}</TableCell>
              <TableCell>{bytesText(row.down)}</TableCell>
            </TableRow>
          ))}
          {result.data?.subscribers.length === 0 && (
            <TableRow>
              <TableCell colSpan={3}>暂无用户分配或流量</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <Typography variant="caption">
        每位用户按自己的当前账期显示原始上下行；中转用户单独标记。
      </Typography>
    </Card>
  );
}
