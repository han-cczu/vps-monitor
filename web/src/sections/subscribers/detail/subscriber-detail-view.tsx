import { useState } from 'react';

import Box from '@mui/material/Box';
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

import { DashboardContent } from 'src/layouts/dashboard';
import { useSubscriber, subscriberAction, useSubscriberTraffic } from 'src/api/subscribers';

import { Label } from 'src/components/label';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { getErrorMessage } from 'src/auth/utils';

import { ActionDialog } from '../../proxy/shared';
import { QuotaBar, bytesText } from '../quota-bar';
import { SubLinksDialog } from '../sub-links-dialog';
import { SubscriberForm } from '../form/subscriber-form';
import { subscriberStatus } from '../list/subscribers-list-view';

export function SubscriberDetailView({ id }: { id: number }) {
  const detail = useSubscriber(id);
  const traffic = useSubscriberTraffic(id);
  const [dialog, setDialog] = useState<
    'links' | 'edit' | 'reset-usage' | 'reset-token' | 'regenerate-credentials' | null
  >(null);
  const subscriber = detail.data?.subscriber;
  const max = Math.max(1, ...(traffic.data?.daily.map((d) => d.up + d.down) ?? []));
  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs
        heading={subscriber?.name ?? '订阅用户'}
        links={[{ name: '订阅用户', href: paths.dashboard.subscribers.root }, { name: '详情' }]}
        sx={{ mb: 3 }}
      />
      {(detail.error || traffic.error) && (
        <Alert severity="error">{getErrorMessage(detail.error || traffic.error)}</Alert>
      )}
      {subscriber && (
        <>
          <Card sx={{ p: 3, mb: 3 }}>
            <Label color={subscriberStatus(subscriber).color}>
              {subscriberStatus(subscriber).label}
            </Label>
            <Typography sx={{ my: 2, whiteSpace: 'pre-wrap' }}>
              {subscriber.note || '无备注'}
            </Typography>
            {subscriber.kind === 'relay' && <Alert severity="info">中转专用用户由源节点的中转助手管理，不能单独修改或旋转凭据。</Alert>}
            <QuotaBar used={subscriber.traffic_used} limit={subscriber.traffic_limit} />
            <Typography variant="body2">
              到期：{subscriber.expire_at || '无到期'} · 账期起始：
              {new Date(subscriber.period_start * 1000).toLocaleDateString()} · 下次重置：
              {subscriber.next_reset_date ??
                (subscriber.reset_day ? `每月 ${subscriber.reset_day} 日` : '不自动重置')}
            </Typography>
            <Box sx={{ display: 'flex', gap: 1, mt: 2, flexWrap: 'wrap' }}>
              <Button disabled={subscriber.kind === 'relay'} onClick={() => setDialog('links')}>订阅链接</Button>
              <Button disabled={subscriber.kind === 'relay'} onClick={() => setDialog('edit')}>编辑</Button>
              <Button onClick={() => setDialog('reset-usage')}>清零用量</Button>
              <Button color="error" disabled={subscriber.kind === 'relay'} onClick={() => setDialog('reset-token')}>
                重置 token
              </Button>
              <Button color="error" disabled={subscriber.kind === 'relay'} onClick={() => setDialog('regenerate-credentials')}>
                重生凭据
              </Button>
            </Box>
          </Card>
          <Card sx={{ p: 3, mb: 3, overflowX: 'auto' }}>
            <Typography variant="h6" sx={{ mb: 2 }}>
              当前账期 · 按节点分账
            </Typography>
            <Table>
              <TableHead>
                <TableRow>
                  <TableCell>节点</TableCell>
                  <TableCell>上行</TableCell>
                  <TableCell>下行</TableCell>
                  <TableCell>合计</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {traffic.data?.by_server.map((row) => (
                  <TableRow key={row.server_id}>
                    <TableCell>
                      {row.name} #{row.server_id}
                    </TableCell>
                    <TableCell>{bytesText(row.up)}</TableCell>
                    <TableCell>{bytesText(row.down)}</TableCell>
                    <TableCell>{bytesText(row.up + row.down)}</TableCell>
                  </TableRow>
                ))}
                {traffic.data?.by_server.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={4}>当前账期暂无流量</TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </Card>
          <Card sx={{ p: 3 }}>
            <Typography variant="h6">最近 30 天 · 上下行合计</Typography>
            <Box
              sx={{ display: 'flex', gap: 0.5, height: 210, pt: 3, alignItems: 'end' }}
              role="img"
              aria-label="最近30天每日流量柱状图"
            >
              {traffic.data?.daily.map((d) => (
                <Box
                  key={d.date}
                  title={`${d.date} 上行 ${bytesText(d.up)} 下行 ${bytesText(d.down)}`}
                  sx={{
                    flex: 1,
                    minWidth: 0,
                    height: `${Math.max(1, ((d.up + d.down) / max) * 100)}%`,
                    bgcolor: 'primary.main',
                    borderRadius: '3px 3px 0 0',
                  }}
                />
              ))}
            </Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: 1 }}>
              <Typography variant="caption">{traffic.data?.daily[0]?.date}</Typography>
              <Typography variant="caption">{traffic.data?.daily.at(-1)?.date}</Typography>
            </Box>
            <details>
              <summary>查看每日明细</summary>
              <Table size="small">
                <TableBody>
                  {traffic.data?.daily.map((d) => (
                    <TableRow key={d.date}>
                      <TableCell>{d.date}</TableCell>
                      <TableCell>上行 {bytesText(d.up)}</TableCell>
                      <TableCell>下行 {bytesText(d.down)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </details>
          </Card>
          {dialog === 'links' && <SubLinksDialog id={id} onClose={() => setDialog(null)} />}
          {dialog === 'edit' && (
            <SubscriberForm
              subscriber={subscriber}
              onClose={() => setDialog(null)}
              onSaved={() => setDialog(null)}
            />
          )}
          {dialog && !['links', 'edit'].includes(dialog) && (
            <ActionDialog
              title={
                dialog === 'reset-usage'
                  ? '清零用量'
                  : dialog === 'reset-token'
                    ? '重置订阅 token'
                    : '重新生成凭据'
              }
              danger
              onClose={() => setDialog(null)}
              onConfirm={async () => {
                await subscriberAction(
                  id,
                  dialog as 'reset-usage' | 'reset-token' | 'regenerate-credentials'
                );
                setDialog(null);
              }}
            >
              {dialog === 'reset-usage'
                ? '清零后重新评估；仍到期或手动停用不会恢复。每日历史保留。'
                : dialog === 'reset-token'
                  ? '旧订阅链接立即失效。'
                  : '旧代理凭据失效，客户端需要刷新订阅。'}
            </ActionDialog>
          )}
        </>
      )}
    </DashboardContent>
  );
}
