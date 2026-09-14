import type { CoreRevision, RevisionDetail } from 'src/types/proxy';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Table from '@mui/material/Table';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import TableRow from '@mui/material/TableRow';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import Typography from '@mui/material/Typography';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';
import TableContainer from '@mui/material/TableContainer';
import LinearProgress from '@mui/material/LinearProgress';

import { rollback, useRevisions, fetchRevision } from 'src/api/proxy';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

import { ActionDialog } from '../shared';

export function RevisionsDialog({
  serverId,
  online,
  onClose,
  onSaved,
}: {
  serverId: number;
  online: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { data, error, isLoading, mutate } = useRevisions(serverId);
  const [view, setView] = useState<RevisionDetail | null>(null);
  const [target, setTarget] = useState<CoreRevision | null>(null);
  const [loading, setLoading] = useState<number | null>(null);
  return (
    <Dialog open fullWidth maxWidth="md" onClose={onClose}>
      <DialogTitle>修订历史 · 最近 20 条</DialogTitle>
      <DialogContent>
        {error && (
          <Alert severity="error" action={<Button onClick={() => mutate()}>重试</Button>}>
            {getErrorMessage(error)}
          </Alert>
        )}
        {isLoading && <LinearProgress />}
        {!online && (
          <Alert severity="info" sx={{ mb: 2 }}>
            Agent 离线，可查看历史，暂不可回滚。
          </Alert>
        )}
        <TableContainer>
          <Table size="small" sx={{ minWidth: 640 }} aria-label="修订历史">
            <TableHead>
              <TableRow>
                {['修订', '时间', '来源', '版本 / SHA-256', '状态', '操作'].map((s) => (
                  <TableCell key={s}>{s}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {data?.revisions.map((rev) => (
                <TableRow key={rev.revision}>
                  <TableCell>#{rev.revision}</TableCell>
                  <TableCell>{new Date(rev.created_at * 1000).toLocaleString()}</TableCell>
                  <TableCell>{rev.created_by}</TableCell>
                  <TableCell>
                    {rev.version}
                    <Typography component="div" variant="caption" sx={{ fontFamily: 'monospace' }}>
                      {rev.sha256.slice(0, 8)}
                    </Typography>
                  </TableCell>
                  <TableCell>{rev.applied && <Label color="success">当前已应用</Label>}</TableCell>
                  <TableCell>
                    <Box sx={{ display: 'flex', whiteSpace: 'nowrap' }}>
                      <Button
                        size="small"
                        loading={loading === rev.revision}
                        disabled={loading !== null}
                        onClick={async () => {
                          setLoading(rev.revision);
                          try {
                            setView(await fetchRevision(serverId, rev.revision));
                          } catch (err) {
                            toast.error(getErrorMessage(err));
                          } finally {
                            setLoading(null);
                          }
                        }}
                      >
                        查看
                      </Button>
                      <Button size="small" disabled={!online} onClick={() => setTarget(rev)}>
                        回滚
                      </Button>
                    </Box>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
        {!isLoading && !error && !data?.revisions.length && (
          <Typography sx={{ p: 3 }} color="text.secondary">
            尚无修订；保存入站并完成预检后将自动生成。
          </Typography>
        )}
      </DialogContent>
      <DialogActions>
        <Button color="inherit" onClick={onClose}>
          关闭
        </Button>
      </DialogActions>
      {view && (
        <Dialog open fullWidth maxWidth="md" onClose={() => setView(null)}>
          <DialogTitle>修订 #{view.revision} · 只读配置</DialogTitle>
          <DialogContent dividers>
            <Box
              component="pre"
              tabIndex={0}
              sx={{ overflow: 'auto', fontSize: 12, maxHeight: '60vh' }}
            >
              {JSON.stringify(view.config_json, null, 2)}
            </Box>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setView(null)}>关闭配置</Button>
          </DialogActions>
        </Dialog>
      )}
      {target && (
        <ActionDialog
          title={`回滚到修订 #${target.revision}`}
          disabled={!online}
          danger
          onClose={() => setTarget(null)}
          onConfirm={async () => {
            const result = await rollback(serverId, target.revision);
            await Promise.allSettled([mutate(), onSaved()]);
            toast.success(`已创建修订 #${result.revision}，等待节点应用`);
          }}
        >
          <Typography>
            将以此配置和版本创建一个新修订并下发，已有连接可能中断。入站表单数据不会改写；后续编辑或“重新下发”将按表单数据重新生成配置。
          </Typography>
        </ActionDialog>
      )}
    </Dialog>
  );
}
