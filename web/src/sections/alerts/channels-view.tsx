import type { NotifyChannel } from 'src/types/alert';

import useSWR from 'swr';
import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import Switch from '@mui/material/Switch';
import MenuItem from '@mui/material/MenuItem';
import Checkbox from '@mui/material/Checkbox';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';
import FormControlLabel from '@mui/material/FormControlLabel';

import axios, { fetcher } from 'src/lib/axios';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';
import { ConfirmDialog } from 'src/components/custom-dialog';
import { LoadingScreen } from 'src/components/loading-screen';

import { getErrorMessage } from 'src/auth/utils';

export function ChannelsView() {
  const { data, error, isLoading, mutate } = useSWR<{ channels: NotifyChannel[] }>(
    '/api/notify-channels',
    fetcher
  );
  const [editing, setEditing] = useState<NotifyChannel | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<NotifyChannel | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const test = async (channel: NotifyChannel) => {
    setBusy(channel.id);
    try {
      await axios.post(`/api/notify-channels/${channel.id}/test`);
      toast.success('测试通知已送达渠道');
    } catch (e) {
      toast.error(getErrorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const remove = async () => {
    if (!deleting) return;
    setBusy(deleting.id);
    try {
      await axios.delete(`/api/notify-channels/${deleting.id}`);
      setDeleting(null);
      await mutate();
      toast.success('通知渠道已删除');
    } catch (e) {
      toast.error(getErrorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  return (
    <>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 2 }}>
        <Button variant="contained" onClick={() => setEditing(null)}>
          新增通知渠道
        </Button>
      </Box>
      {error && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {getErrorMessage(error)}
        </Alert>
      )}
      {isLoading ? (
        <LoadingScreen />
      ) : (
        <Box
          sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr', md: 'repeat(2, 1fr)' } }}
        >
          {data?.channels.map((c) => (
            <Card key={c.id} sx={{ p: 3 }}>
              <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', mb: 1 }}>
                <Typography variant="h6" sx={{ flex: 1, overflowWrap: 'anywhere' }}>
                  {c.name}
                </Typography>
                <Label color={c.enabled ? 'success' : 'default'}>
                  {c.enabled ? '启用' : '停用'}
                </Label>
              </Box>
              <Typography
                variant="body2"
                color="text.secondary"
                sx={{ overflowWrap: 'anywhere', mb: 2 }}
              >
                {c.kind === 'telegram' ? `Telegram · 聊天 ID ${c.config.chat_id}` : c.config.url}
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
                <Button
                  variant="outlined"
                  onClick={() => test(c)}
                  loading={busy === c.id}
                  disabled={busy !== null}
                >
                  发送测试
                </Button>
                <Button onClick={() => setEditing(c)}>编辑</Button>
                <Button color="error" onClick={() => setDeleting(c)}>
                  删除
                </Button>
              </Box>
            </Card>
          ))}
          {!data?.channels.length && !error && (
            <Alert severity="info">
              尚无通知渠道。告警仍会记录，添加并启用渠道后新告警才会发送。
            </Alert>
          )}
        </Box>
      )}
      {editing !== undefined && (
        <ChannelForm
          current={editing}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
            mutate();
          }}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title="删除通知渠道"
        content={`删除「${deleting?.name ?? ''}」及其待发送通知？告警历史会保留。`}
        action={
          <Button color="error" variant="contained" loading={busy !== null} onClick={remove}>
            删除
          </Button>
        }
      />
    </>
  );
}

function ChannelForm({
  current,
  onClose,
  onSaved,
}: {
  current: NotifyChannel | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(current?.name ?? '');
  const [kind, setKind] = useState<'telegram' | 'webhook'>(current?.kind ?? 'webhook');
  const [enabled, setEnabled] = useState(current?.enabled ?? true);
  const [url, setURL] = useState(current?.config.url ?? '');
  const [chatID, setChatID] = useState(current?.config.chat_id ?? '');
  const [token, setToken] = useState('');
  const [secret, setSecret] = useState('');
  const [clearSecret, setClearSecret] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const save = async () => {
    setBusy(true);
    setError('');
    try {
      const config = kind === 'telegram' ? { bot_token: token, chat_id: chatID } : { url, secret };
      const payload = { name, kind, config, enabled, clear_secret: clearSecret };
      if (current) await axios.put(`/api/notify-channels/${current.id}`, payload);
      else await axios.post('/api/notify-channels', payload);
      toast.success('通知渠道已保存');
      onSaved();
    } catch (e) {
      setError(getErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open fullWidth maxWidth="sm" onClose={busy ? undefined : onClose}>
      <DialogTitle>{current ? '编辑通知渠道' : '新增通知渠道'}</DialogTitle>
      <DialogContent>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
          {error && <Alert severity="error">{error}</Alert>}
          <TextField
            label="名称"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            disabled={busy}
          />
          <TextField
            select
            label="渠道类型"
            value={kind}
            onChange={(e) => setKind(e.target.value as typeof kind)}
            disabled={!!current || busy}
          >
            <MenuItem value="webhook">Webhook</MenuItem>
            <MenuItem value="telegram">Telegram</MenuItem>
          </TextField>
          {kind === 'telegram' ? (
            <>
              <TextField
                label="机器人令牌"
                type="password"
                autoComplete="new-password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                helperText={
                  current?.config.has_bot_token
                    ? '已保存；留空保留原 token'
                    : '从 BotFather 获取的机器人 token'
                }
                disabled={busy}
              />
              <TextField
                label="聊天 ID"
                value={chatID}
                onChange={(e) => setChatID(e.target.value)}
                disabled={busy}
              />
            </>
          ) : (
            <>
              <TextField
                label="Webhook 地址"
                value={url}
                onChange={(e) => setURL(e.target.value)}
                placeholder="https://example.com/webhook"
                disabled={busy}
              />
              <TextField
                label="HMAC 签名密钥（可选）"
                type="password"
                autoComplete="new-password"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                helperText={
                  current?.config.has_secret
                    ? '已保存；留空保留原 secret'
                    : '设置后发送 X-Signature 签名头'
                }
                disabled={busy || clearSecret}
              />
              {current?.config.has_secret && (
                <FormControlLabel
                  label="移除现有签名 secret"
                  control={
                    <Checkbox
                      checked={clearSecret}
                      onChange={(_, checked) => setClearSecret(checked)}
                    />
                  }
                />
              )}
            </>
          )}
          <FormControlLabel
            label="启用渠道"
            control={
              <Switch
                checked={enabled}
                onChange={(_, checked) => setEnabled(checked)}
                disabled={busy}
              />
            }
          />
        </Box>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={busy}>
          取消
        </Button>
        <Button variant="contained" onClick={save} loading={busy}>
          保存
        </Button>
      </DialogActions>
    </Dialog>
  );
}
