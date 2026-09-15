import type { Inbound, CoreState, ProxyAssignment } from 'src/types/proxy';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Switch from '@mui/material/Switch';
import Divider from '@mui/material/Divider';
import Typography from '@mui/material/Typography';

import { formatTrafficBytes } from 'src/utils/format';

import { updateInbound, deleteInbound, regenerateKeys } from 'src/api/proxy';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';
import { EmptyContent } from 'src/components/empty-content';

import { getErrorMessage } from 'src/auth/utils';

import { ActionDialog } from '../shared';
import { InboundForm } from './inbound-form';
import { PROTOCOLS, countAssignments } from '../helpers';

export function InboundList({
  serverId,
  inbounds,
  core,
  subscribers,
  onSaved,
}: {
  serverId: number;
  inbounds: Inbound[];
  core?: CoreState;
  subscribers?: ProxyAssignment[];
  onSaved: () => Promise<void>;
}) {
  const [editing, setEditing] = useState<Inbound | 'new' | null>(null);
  const [action, setAction] = useState<{ kind: 'delete' | 'keys'; inbound: Inbound } | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const toggle = async (inbound: Inbound) => {
    setBusy(inbound.id);
    try {
      await updateInbound(inbound.id, { enabled: !inbound.enabled });
      await onSaved();
      toast.success('已保存，等待节点应用');
    } catch (err) {
      toast.error(getErrorMessage(err));
    } finally {
      setBusy(null);
    }
  };
  return (
    <Card sx={{ p: { xs: 2, sm: 3 } }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 2 }}>
        <Typography variant="h6">
          入站配置{' '}
          <Typography component="span" color="text.secondary">
            {inbounds.length}
          </Typography>
        </Typography>
        <Button variant="contained" disabled={busy !== null} onClick={() => setEditing('new')}>
          新增入站
        </Button>
      </Box>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        保存后自动下发；节点离线或核心未安装时，配置会保留并等待应用。
      </Typography>
      {!inbounds.length && (
        <EmptyContent title="还没有入站" description="新增 VLESS、SS、HY2 或 TUIC 入站" />
      )}
      {inbounds.map((inbound, index) => {
        const p = PROTOCOLS[inbound.protocol];
        const counter = core?.inbounds.find((x) => x.name === inbound.tag);
        return (
          <Box key={inbound.id}>
            {!!index && <Divider sx={{ my: 2 }} />}
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
              <Label color={p.color}>{p.label}</Label>
              <Typography variant="subtitle2">
                {inbound.listen_port}/{p.transports.join('+')}
              </Typography>
              <Box sx={{ flexGrow: 1 }} />
              <Switch
                checked={inbound.enabled}
                disabled={busy !== null}
                onChange={() => toggle(inbound)}
                slotProps={{ input: { 'aria-label': `启用 ${inbound.tag}` } }}
              />
            </Box>
            <Typography variant="body2" sx={{ mt: 0.5, overflowWrap: 'anywhere' }}>
              {inbound.remark || inbound.tag}
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', my: 1 }}>
              分配用户 {subscribers ? countAssignments(subscribers, serverId, inbound.id) : '—'}
              （含停用） · 上传 {core ? formatTrafficBytes(counter?.up ?? 0) : '—'} / 下载{' '}
              {core ? formatTrafficBytes(counter?.down ?? 0) : '—'}
            </Typography>
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
              <Button size="small" disabled={busy !== null} onClick={() => setEditing(inbound)}>
                编辑
              </Button>
              {inbound.protocol !== 'tuic' && (
                <Button
                  size="small"
                  disabled={busy !== null}
                  onClick={() => setAction({ kind: 'keys', inbound })}
                >
                  重生密钥
                </Button>
              )}
              <Button
                size="small"
                color="error"
                disabled={busy !== null}
                onClick={() => setAction({ kind: 'delete', inbound })}
              >
                删除
              </Button>
            </Box>
          </Box>
        );
      })}
      {!!inbounds.length && (
        <Alert severity="info" sx={{ mt: 2 }}>
          流量为面板本次运行累计，重启面板后清零。TUIC 使用订阅用户凭据和节点证书，无独立入站密钥。
        </Alert>
      )}
      {editing && (
        <InboundForm
          serverId={serverId}
          inbounds={inbounds}
          current={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={onSaved}
        />
      )}
      {action && (
        <ActionDialog
          title={action.kind === 'delete' ? '删除入站' : '重生入站密钥'}
          danger
          onClose={() => setAction(null)}
          onConfirm={async () => {
            if (action.kind === 'delete') await deleteInbound(action.inbound.id);
            else await regenerateKeys(action.inbound.id);
            await onSaved();
            toast.success('已保存，等待节点应用');
          }}
        >
          <Typography>
            {action.kind === 'delete'
              ? `删除 ${action.inbound.tag} 及其用户分配，相关客户端将无法连接。`
              : `重新生成 ${action.inbound.tag} 的密钥后，旧凭据失效，客户端需刷新订阅。`}
          </Typography>
        </ActionDialog>
      )}
    </Card>
  );
}
