import type { CoreVersion } from 'src/types/corefiles';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Card from '@mui/material/Card';
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

import { useCoreFiles, setCurrentCore, deleteCoreVersion } from 'src/api/corefiles';

import { toast } from 'src/components/snackbar';
import { EmptyContent } from 'src/components/empty-content';

import { getErrorMessage } from 'src/auth/utils';

import { CoreUploadDialog } from './core-upload-dialog';
import { AccountLayout } from '../../account/account-layout';

export function CoreFilesView() {
  const { versions, pinnedVersion, error, isLoading, refresh } = useCoreFiles();
  const [mode, setMode] = useState<'upload' | 'fetch' | null>(null);
  const [action, setAction] = useState<{ kind: 'current' | 'delete'; item: CoreVersion } | null>(
    null
  );
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState('');
  const confirm = async () => {
    if (!action) return;
    setBusy(true);
    setActionError('');
    try {
      if (action.kind === 'current') await setCurrentCore(action.item.version);
      else await deleteCoreVersion(action.item.version);
      toast.success(action.kind === 'current' ? '当前版本已更新' : '版本已删除');
      await refresh();
      setAction(null);
    } catch (err) {
      setActionError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <AccountLayout>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 2, alignItems: 'center', mb: 3 }}>
        <Box sx={{ flex: 1, minWidth: 180 }}>
          <Typography variant="h6">代理核心</Typography>
          <Typography variant="body2" color="text.secondary">
            {pinnedVersion ? `当前适配版本：${pinnedVersion}` : '管理 sing-box 构建版本'}
          </Typography>
        </Box>
        <Button variant="outlined" onClick={() => setMode('fetch')}>
          从 GitHub 获取
        </Button>
        <Button variant="contained" onClick={() => setMode('upload')}>
          上传核心
        </Button>
      </Box>
      <Alert severity="info" sx={{ mb: 3 }}>
        上传 amd64 和 arm64 两个架构后可设为当前版本。版本切换不会自动升级已安装的节点。
      </Alert>
      {error && (
        <Alert
          severity="error"
          sx={{ mb: 2 }}
          action={
            <Button color="inherit" onClick={() => refresh()}>
              重试
            </Button>
          }
        >
          核心版本加载失败。
        </Alert>
      )}
      {isLoading ? (
        <LinearProgress aria-label="加载核心版本" />
      ) : !versions.length ? (
        <Card sx={{ p: 3 }}>
          <EmptyContent
            title="还没有核心版本"
            description="上传本项目工作流生成的 sing-box 文件，或从 GitHub Release 获取。"
          />
        </Card>
      ) : (
        <TableContainer component={Card} sx={{ overflowX: 'auto' }}>
          <Table sx={{ minWidth: 680 }} aria-label="核心版本">
            <TableHead>
              <TableRow>
                {['版本', '架构', '上传时间', '状态', '操作'].map((name) => (
                  <TableCell key={name}>{name}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {versions.map((item) => (
                <TableRow key={item.version}>
                  <TableCell sx={{ fontWeight: 600 }}>{item.version}</TableCell>
                  <TableCell>
                    <Box sx={{ display: 'flex', gap: 0.5 }}>
                      {(['amd64', 'arm64'] as const).map((arch) => (
                        <Chip
                          key={arch}
                          size="small"
                          label={arch}
                          variant={item.arches.includes(arch) ? 'filled' : 'outlined'}
                          color={item.arches.includes(arch) ? 'success' : 'default'}
                        />
                      ))}
                    </Box>
                  </TableCell>
                  <TableCell>{new Date(item.uploaded_at * 1000).toLocaleString()}</TableCell>
                  <TableCell>
                    {item.current ? (
                      <Chip size="small" color="primary" label="当前版本" />
                    ) : item.arches.length === 2 ? (
                      '可启用'
                    ) : (
                      '待补架构'
                    )}
                  </TableCell>
                  <TableCell>
                    <Box sx={{ display: 'flex', gap: 1, whiteSpace: 'nowrap' }}>
                      <Button
                        size="small"
                        disabled={item.current || item.arches.length !== 2}
                        onClick={() => {
                          setActionError('');
                          setAction({ kind: 'current', item });
                        }}
                      >
                        设为当前
                      </Button>
                      <Button
                        size="small"
                        color="error"
                        disabled={item.current}
                        onClick={() => {
                          setActionError('');
                          setAction({ kind: 'delete', item });
                        }}
                      >
                        删除
                      </Button>
                    </Box>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      )}
      {mode && (
        <CoreUploadDialog
          mode={mode}
          pinnedVersion={pinnedVersion}
          onClose={() => setMode(null)}
          onSaved={refresh}
        />
      )}
      {action && (
        <Dialog
          open
          fullWidth
          maxWidth="xs"
          onClose={() => {
            if (!busy) setAction(null);
          }}
        >
          <DialogTitle>{action?.kind === 'current' ? '设为当前版本' : '删除核心版本'}</DialogTitle>
          <DialogContent>
            {actionError && (
              <Alert severity="error" sx={{ mb: 2 }}>
                {actionError}
              </Alert>
            )}
            <Typography>
              {action?.kind === 'current'
                ? `将 ${action.item.version} 设为当前版本。已安装节点不会自动升级；后续在代理页逐台执行升级。`
                : `删除 ${action?.item.version} 的全部核心文件，删除后需要重新上传。`}
            </Typography>
          </DialogContent>
          <DialogActions>
            <Button color="inherit" disabled={busy} onClick={() => setAction(null)}>
              取消
            </Button>
            <Button
              variant="contained"
              color={action?.kind === 'delete' ? 'error' : 'primary'}
              loading={busy}
              onClick={confirm}
            >
              确认
            </Button>
          </DialogActions>
        </Dialog>
      )}
    </AccountLayout>
  );
}
