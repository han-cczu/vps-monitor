import type { PingTask, PingTaskPayload } from 'src/types/ping';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import Switch from '@mui/material/Switch';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import DialogTitle from '@mui/material/DialogTitle';
import Autocomplete from '@mui/material/Autocomplete';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';
import FormControlLabel from '@mui/material/FormControlLabel';

import { savePingTask } from 'src/api/ping';
import { useServers } from 'src/api/servers';

import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

export function PingTaskFormDialog({
  task,
  onClose,
  onSaved,
}: {
  task: PingTask | null;
  onClose: () => void;
  onSaved: () => Promise<unknown>;
}) {
  const { servers, serversLoading, serversError } = useServers();
  const [form, setForm] = useState<PingTaskPayload>({
    name: task?.name ?? '',
    target: task?.target ?? '',
    kind: task?.kind ?? 'icmp',
    interval_sec: task?.interval_sec ?? 60,
    server_ids: task?.server_ids ?? null,
    enabled: task?.enabled ?? true,
    sort_order: task?.sort_order ?? 0,
  });
  const [allNodes, setAllNodes] = useState(task?.server_ids == null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const close = () => {
    if (!busy) {
      onClose();
    }
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const result = await savePingTask(task?.id, {
        ...form,
        name: form.name.trim(),
        target: form.target.trim(),
        server_ids: allNodes ? null : (form.server_ids ?? []),
      });
      toast.success(`已保存，配置已推送至 ${result.pushed} 台在线节点`);
      await onSaved();
      onClose();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open fullWidth maxWidth="sm" onClose={close}>
      <Box component="form" onSubmit={submit}>
        <DialogTitle>{task ? '编辑 Ping 任务' : '新建 Ping 任务'}</DialogTitle>
        <DialogContent
          sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: '12px !important' }}
        >
          {error && <Alert severity="error">{error}</Alert>}
          <TextField
            label="名称"
            required
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            slotProps={{ htmlInput: { maxLength: 32 } }}
          />
          <TextField
            select
            label="探测类型"
            value={form.kind}
            onChange={(e) => setForm({ ...form, kind: e.target.value as 'icmp' | 'tcp' })}
          >
            <MenuItem value="icmp">ICMP</MenuItem>
            <MenuItem value="tcp">TCP</MenuItem>
          </TextField>
          <TextField
            label="目标"
            required
            value={form.target}
            onChange={(e) => setForm({ ...form, target: e.target.value })}
            helperText={
              form.kind === 'tcp'
                ? '主机:端口，例如 example.com:443；IPv6 使用 [::1]:443'
                : 'IP 或域名，不带协议和端口'
            }
          />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
            <TextField
              label="间隔（秒）"
              type="number"
              required
              value={form.interval_sec}
              onChange={(e) => setForm({ ...form, interval_sec: Number(e.target.value) })}
              slotProps={{ htmlInput: { min: 10, max: 3600, step: 1 } }}
              helperText="10–3600，建议 60 秒"
            />
            <TextField
              label="排序"
              type="number"
              required
              value={form.sort_order}
              onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) })}
              slotProps={{ htmlInput: { min: -1000000, max: 1000000, step: 1 } }}
              helperText="数值越小越靠前"
            />
          </Box>
          <FormControlLabel
            control={<Switch checked={allNodes} onChange={(_, checked) => setAllNodes(checked)} />}
            label="应用到全部节点（含以后新增的节点）"
          />
          {!allNodes && (
            <>
              {serversError && <Alert severity="error">节点列表加载失败，请稍后重试。</Alert>}
              <Autocomplete
                multiple
                options={servers}
                loading={serversLoading}
                getOptionLabel={(server) => server.name}
                isOptionEqualToValue={(a, b) => a.id === b.id}
                value={servers.filter((server) => form.server_ids?.includes(server.id))}
                onChange={(_, selected) =>
                  setForm({ ...form, server_ids: selected.map((server) => server.id) })
                }
                renderInput={(params) => (
                  <TextField
                    {...params}
                    label="作用节点"
                    helperText="未选择节点时，不向任何节点下发。"
                  />
                )}
              />
            </>
          )}
          <FormControlLabel
            control={
              <Switch
                checked={form.enabled}
                onChange={(_, enabled) => setForm({ ...form, enabled })}
              />
            }
            label="启用任务"
          />
        </DialogContent>
        <DialogActions>
          <Button color="inherit" onClick={close} disabled={busy}>
            取消
          </Button>
          <Button
            type="submit"
            variant="contained"
            loading={busy}
            disabled={!allNodes && (serversLoading || !!serversError)}
          >
            保存并下发
          </Button>
        </DialogActions>
      </Box>
    </Dialog>
  );
}
