import type { CoreState, CoreRevision } from 'src/types/proxy';

import { useState, useEffect } from 'react';

import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Typography from '@mui/material/Typography';
import CircularProgress from '@mui/material/CircularProgress';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { applyCore, installCore, restartCore } from 'src/api/proxy';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

import { ActionDialog } from '../shared';
import { coreStatus, revisionApplied } from '../helpers';

export function CoreStatusCard({
  core,
  onRefresh,
  onLogs,
  onRevisions,
}: {
  core: CoreState;
  onRefresh: () => Promise<unknown>;
  onLogs: () => void;
  onRevisions: () => void;
}) {
  const [action, setAction] = useState<'install' | 'restart' | null>(null);
  const [sending, setSending] = useState(false);
  const [waiting, setWaiting] = useState<{ revision?: CoreRevision; version?: string } | null>(
    null
  );
  const status = coreStatus(core);
  useEffect(() => {
    if (!waiting) return undefined;
    const timer = setTimeout(() => {
      setWaiting(null);
      toast.warning('60 秒内尚未确认完成，请查看核心状态与最后错误');
    }, 60_000);
    return () => clearTimeout(timer);
  }, [waiting]);
  useEffect(() => {
    if (!waiting) return;
    const applied = waiting.revision
      ? revisionApplied(core, waiting.revision)
      : core.online && core.installed_version === waiting.version;
    if (applied) {
      setWaiting(null);
      toast.success(waiting.revision ? '目标修订已应用' : '核心版本已安装');
    }
  }, [core, waiting]);
  const apply = async () => {
    setSending(true);
    try {
      const result = await applyCore(core.server_id);
      setWaiting({ revision: result.revision });
      await onRefresh();
      toast.success('下发请求已发送');
    } catch (err) {
      toast.error(getErrorMessage(err));
    } finally {
      setSending(false);
    }
  };
  const blocked = !core.online || sending || !!waiting;
  const installLabel = core.installed_version ? `升级到 ${core.current_version}` : '安装核心';
  return (
    <Card sx={{ p: { xs: 2, sm: 3 } }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
        <Typography variant="h6" sx={{ flex: 1 }}>
          sing-box 核心
        </Typography>
        <Label color={status.color}>{status.label}</Label>
      </Box>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'auto minmax(0, 1fr)',
          gap: 1.5,
          typography: 'body2',
        }}
      >
        <Box color="text.secondary">Agent</Box>
        <Box>{core.online ? '在线' : '离线'}</Box>
        <Box color="text.secondary">已安装版本</Box>
        <Box sx={{ overflowWrap: 'anywhere' }}>{core.installed_version || '未安装'}</Box>
        <Box color="text.secondary">托管版本</Box>
        <Box sx={{ overflowWrap: 'anywhere' }}>{core.current_version || '尚未设置'}</Box>
        <Box color="text.secondary">运行状态</Box>
        <Box>
          {core.running ? '运行中' : '已停止'}
          {!core.online && '（离线前状态）'}
        </Box>
        <Box color="text.secondary">修订 已应用 / 目标</Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {core.applied_revision} / {core.desired_revision}
          {core.pending && <CircularProgress size={14} aria-label="等待应用目标修订" />}
        </Box>
        <Box color="text.secondary">本机防火墙</Box>
        <Box>{core.firewall ?? '未上报'}</Box>
      </Box>
      <Typography variant="subtitle2" sx={{ mt: 2, mb: 1 }}>
        节点监听端口{!core.online && '（离线前）'}
      </Typography>
      <Box sx={{ display: 'flex', gap: 0.75, flexWrap: 'wrap' }}>
        {core.listening.length ? (
          core.listening.map((p) => <Chip key={p} label={p} size="small" variant="outlined" />)
        ) : (
          <Typography variant="body2" color="text.secondary">
            暂无监听端口
          </Typography>
        )}
      </Box>
      {core.firewall === 'none' && (
        <Alert severity="warning" sx={{ mt: 2 }}>
          本机无防火墙管理。请按已启用入站的端口与传输协议检查云安全组。
        </Alert>
      )}
      {!core.online && (
        <Alert severity="info" sx={{ mt: 2 }}>
          Agent 离线，安装、重启、下发与日志暂不可用。历史记录仍可查看。
        </Alert>
      )}
      {!core.current_version && (
        <Alert severity="info" sx={{ mt: 2 }}>
          请先在
          <Button size="small" component={RouterLink} href={paths.dashboard.settings.corefiles}>
            核心托管
          </Button>
          设置当前版本。
        </Alert>
      )}
      {core.last_error && (
        <Alert severity="error" sx={{ mt: 2, overflowWrap: 'anywhere' }}>
          <Box component="details">
            <Box component="summary">
              最后错误：{core.last_error.slice(0, 80)}
              {core.last_error.length > 80 ? '…' : ''}
            </Box>
            <Box component="pre" sx={{ whiteSpace: 'pre-wrap', fontSize: 12 }}>
              {core.last_error}
            </Box>
          </Box>
        </Alert>
      )}
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, mt: 2.5 }}>
        {core.installed_version !== core.current_version && (
          <Button
            variant="contained"
            disabled={blocked || !core.current_version}
            onClick={() => setAction('install')}
          >
            {installLabel}
          </Button>
        )}
        <Button
          variant="outlined"
          disabled={blocked || !core.installed_version}
          onClick={() => setAction('restart')}
        >
          重启
        </Button>
        <Button
          variant="outlined"
          loading={sending || !!waiting}
          disabled={!core.online || !core.installed_version || !core.current_version}
          onClick={apply}
        >
          重新下发
        </Button>
        <Button disabled={!core.online || !core.installed_version} onClick={onLogs}>
          查看日志
        </Button>
        <Button onClick={onRevisions}>修订历史</Button>
      </Box>
      {action && (
        <ActionDialog
          title={action === 'install' ? installLabel : '重启核心'}
          disabled={!core.online}
          onClose={() => setAction(null)}
          onConfirm={async () => {
            if (action === 'install') {
              await installCore(core.server_id);
              setWaiting({ version: core.current_version });
            } else await restartCore(core.server_id);
            await onRefresh();
            toast.success('操作请求已发送，等待节点确认');
          }}
        >
          <Typography>
            {action === 'install'
              ? `将在节点安装 ${core.current_version} 并应用当前配置。`
              : '将重启节点的 sing-box 服务。'}
            已有连接可能中断数秒。
          </Typography>
        </ActionDialog>
      )}
    </Card>
  );
}
