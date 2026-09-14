import { useState } from 'react';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';

import axios from 'src/lib/axios';
import { useServers } from 'src/api/servers';
import { useAdvanced, useInbounds } from 'src/api/proxy';

import { getErrorMessage } from 'src/auth/utils';

import { ActionDialog } from '../shared';

function RelayDialog({
  serverId,
  onClose,
  onSaved,
}: {
  serverId: number;
  onClose: () => void;
  onSaved: (json: Record<string, unknown>) => void;
}) {
  const { servers, serversError } = useServers();
  const [target, setTarget] = useState(0);
  const [inbound, setInbound] = useState(0);
  const inbounds = useInbounds(target);
  return (
    <ActionDialog
      title="中转到其它节点"
      onClose={onClose}
      disabled={!target || !inbound}
      confirmLabel="校验并保存中转"
      onConfirm={async () => {
        const result = await axios.post(`/api/servers/${serverId}/advanced/relay`, {
          target_server_id: target,
          target_inbound_id: inbound,
        });
        onSaved(result.data.advanced.extra_json);
        onClose();
      }}
    >
      <Alert severity="warning" sx={{ mb: 2 }}>
        此节点进入代理入站的流量将默认经目标节点出口；系统自身流量和 Agent
        连接不受影响。将创建或复用一个不限额的中转专用用户。
      </Alert>
      {(serversError || inbounds.error) && target > 0 && (
        <Alert severity="error">{getErrorMessage(serversError || inbounds.error)}</Alert>
      )}
      <TextField
        select
        fullWidth
        label="目标节点"
        value={target}
        onChange={(e) => {
          setTarget(Number(e.target.value));
          setInbound(0);
        }}
        sx={{ mb: 2 }}
      >
        <MenuItem value={0}>请选择</MenuItem>
        {servers
          .filter((s) => s.id !== serverId)
          .map((s) => (
            <MenuItem key={s.id} value={s.id} disabled={!s.public_host}>
              {s.name}
              {!s.public_host && '（缺少公开地址）'}
            </MenuItem>
          ))}
      </TextField>
      <TextField
        select
        fullWidth
        label="目标入站"
        value={inbound}
        onChange={(e) => setInbound(Number(e.target.value))}
      >
        <MenuItem value={0}>请选择</MenuItem>
        {inbounds.data?.inbounds
          .filter((i) => i.enabled && ['vless', 'shadowsocks'].includes(i.protocol))
          .map((i) => (
            <MenuItem key={i.id} value={i.id}>
              {i.protocol} :{i.listen_port} {i.remark}
            </MenuItem>
          ))}
      </TextField>
    </ActionDialog>
  );
}

export function AdvancedJSON({ serverId }: { serverId: number }) {
  const advanced = useAdvanced(serverId);
  const [open, setOpen] = useState(false);
  const [text, setText] = useState('{}');
  const [base, setBase] = useState('{}');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [busy, setBusy] = useState(false);
  const [helper, setHelper] = useState<'relay' | 'remove' | null>(null);
  const dirty = text !== base;
  const accept = async (value: Record<string, unknown>) => {
    const next = JSON.stringify(value, null, 2);
    setText(next);
    setBase(next);
    setSuccess('已保存，等待节点应用');
    await advanced.mutate({ advanced: { extra_json: value } }, false);
  };
  const perform = async (save: boolean) => {
    setBusy(true);
    setError('');
    setSuccess('');
    try {
      const extra = JSON.parse(text);
      const result = save
        ? await axios.put(`/api/servers/${serverId}/advanced`, { extra_json: extra })
        : await axios.post(`/api/servers/${serverId}/advanced/check`, { extra_json: extra });
      if (save) {
        await accept(result.data.advanced.extra_json);
      } else {
        setSuccess('预检通过；保存时会重新检查');
      }
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Alert
        severity={advanced.error ? 'error' : 'info'}
        action={
          <Button
            disabled={!advanced.data}
            onClick={() => {
              const next = JSON.stringify(advanced.data?.advanced.extra_json ?? {}, null, 2);
              setText(next);
              setBase(next);
              setError('');
              setSuccess('');
              setOpen(true);
            }}
          >
            编辑
          </Button>
        }
      >
        高级 JSON：
        {advanced.error
          ? getErrorMessage(advanced.error)
          : Object.keys(advanced.data?.advanced.extra_json ?? {}).length
            ? '已配置'
            : '未配置'}
      </Alert>
      <Dialog open={open} fullWidth maxWidth="lg" onClose={busy ? undefined : () => setOpen(false)}>
        <DialogTitle>高级 JSON</DialogTitle>
        <DialogContent>
          <Alert severity="info" sx={{ mb: 2 }}>
            配置会与受管入站合并，保存前使用当前托管版本的 sing-box 检查；不能覆盖
            inbounds、experimental、log。错误信息中的凭据会脱敏。
          </Alert>
          {error && (
            <Alert severity="error" sx={{ mb: 2, whiteSpace: 'pre-wrap', fontFamily: 'monospace' }}>
              {error}
            </Alert>
          )}
          {success && (
            <Alert severity="success" sx={{ mb: 2 }}>
              {success}
            </Alert>
          )}
          <TextField
            fullWidth
            multiline
            minRows={16}
            maxRows={26}
            label="高级 JSON 配置"
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setSuccess('');
            }}
            slotProps={{ htmlInput: { style: { fontFamily: 'monospace', fontSize: 13 } } }}
          />
          <Box sx={{ display: 'flex', gap: 1, mt: 2, flexWrap: 'wrap' }}>
            <Button disabled={busy} onClick={() => setText('{}')}>
              恢复为 {'{}'}
            </Button>
            <Button disabled={dirty || busy} onClick={() => setHelper('relay')}>
              中转到其它节点
            </Button>
            <Button disabled={dirty || busy} color="error" onClick={() => setHelper('remove')}>
              移除中转
            </Button>
          </Box>
          {dirty && (
            <Typography variant="caption">先保存当前编辑内容，之后可使用中转助手。</Typography>
          )}
          <details>
            <summary>配置示例</summary>
            <Typography component="pre" sx={{ fontFamily: 'monospace', overflowX: 'auto' }}>
              {JSON.stringify({ dns: { servers: [{ type: 'local', tag: 'local' }] } }, null, 2)}
            </Typography>
            <Typography component="pre" sx={{ fontFamily: 'monospace', overflowX: 'auto' }}>
              {JSON.stringify(
                {
                  route: {
                    rules: [
                      { domain_suffix: ['example.com'], action: 'route', outbound: 'direct' },
                    ],
                  },
                },
                null,
                2
              )}
            </Typography>
          </details>
        </DialogContent>
        <DialogActions>
          <Button disabled={busy} onClick={() => setOpen(false)}>
            关闭
          </Button>
          <Button loading={busy} onClick={() => perform(false)}>
            校验
          </Button>
          <Button variant="contained" loading={busy} onClick={() => perform(true)}>
            保存
          </Button>
        </DialogActions>
      </Dialog>
      {helper === 'relay' && (
        <RelayDialog
          serverId={serverId}
          onClose={() => setHelper(null)}
          onSaved={(json) => {
            void accept(json);
          }}
        />
      )}
      {helper === 'remove' && (
        <ActionDialog
          title="移除中转"
          danger
          onClose={() => setHelper(null)}
          onConfirm={async () => {
            const result = await axios.delete(`/api/servers/${serverId}/advanced/relay`);
            await accept(result.data.advanced.extra_json);
            setHelper(null);
          }}
        >
          移除当前助手默认出口及对应出站，恢复直接出口；中转用户历史用量保留，解除其入站分配。
        </ActionDialog>
      )}
    </>
  );
}
