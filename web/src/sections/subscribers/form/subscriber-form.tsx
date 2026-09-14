import type { Subscriber } from 'src/types/subscriber';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import Switch from '@mui/material/Switch';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';
import FormControlLabel from '@mui/material/FormControlLabel';

import {
  saveSubscriber,
  assignSubscriber,
  subscriberAction,
  refreshSubscribers,
} from 'src/api/subscribers';

import { getErrorMessage } from 'src/auth/utils';

import { AssignmentPicker } from '../assignment-picker';
import { CopyField, ActionDialog } from '../../proxy/shared';

export function SubscriberForm({
  subscriber,
  onClose,
  onSaved,
}: {
  subscriber?: Subscriber;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [current, setCurrent] = useState(subscriber);
  const [tab, setTab] = useState('base');
  const [name, setName] = useState(subscriber?.name ?? '');
  const [note, setNote] = useState(subscriber?.note ?? '');
  const [enabled, setEnabled] = useState(subscriber?.enabled ?? true);
  const [limit, setLimit] = useState(String((subscriber?.traffic_limit ?? 0) / 1024 ** 3));
  const [unit, setUnit] = useState(3);
  const [resetDay, setResetDay] = useState(subscriber?.reset_day ?? 0);
  const [expire, setExpire] = useState(subscriber?.expire_at ?? '');
  const [ids, setIds] = useState(subscriber?.assigned_inbounds.map((i) => i.inbound_id) ?? []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [confirm, setConfirm] = useState(false);
  const save = async () => {
    setError('');
    const trafficLimit = Math.round(Number(limit) * 1024 ** unit);
    if (
      !name.trim() ||
      name.trim().length > 64 ||
      note.length > 2000 ||
      !Number.isSafeInteger(trafficLimit) ||
      trafficLimit < 0
    ) {
      setError('请填写 1–64 字名称、最多 2000 字备注和有效非负额度。');
      return;
    }
    setBusy(true);
    try {
      const saved = await saveSubscriber(current?.id, {
        name: name.trim(),
        note,
        enabled,
        traffic_limit: trafficLimit,
        reset_day: resetDay,
        expire_at: expire || null,
      });
      setCurrent(saved);
      await assignSubscriber(saved.id, ids);
      await refreshSubscribers(saved.id);
      onSaved();
    } catch (err) {
      setError(`保存未完成：${getErrorMessage(err)}。可修正后重试，已创建的用户会继续更新。`);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Dialog open fullWidth maxWidth="md" onClose={busy ? undefined : onClose}>
        <DialogTitle>{current ? '编辑订阅用户' : '新增订阅用户'}</DialogTitle>
        <DialogContent>
          {error && (
            <Alert severity="error" sx={{ mb: 2 }}>
              {error}
            </Alert>
          )}
          <Tabs value={tab} onChange={(_, value) => setTab(value)} sx={{ mb: 3 }}>
            <Tab value="base" label="基础" />
            <Tab value="quota" label="额度" />
            <Tab value="assign" label="分配" />
            <Tab value="credentials" label="凭据" disabled={!current} />
          </Tabs>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, minHeight: 260 }}>
            {tab === 'base' && (
              <>
                <TextField
                  required
                  label="名称"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  slotProps={{ htmlInput: { maxLength: 64 } }}
                />
                <TextField
                  label="备注"
                  multiline
                  minRows={3}
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  slotProps={{ htmlInput: { maxLength: 2000 } }}
                />
                <FormControlLabel
                  control={<Switch checked={enabled} onChange={(_, value) => setEnabled(value)} />}
                  label="启用"
                />
              </>
            )}
            {tab === 'quota' && (
              <>
                <Box sx={{ display: 'flex', gap: 2 }}>
                  <TextField
                    label="流量上限（0 为不限额）"
                    type="number"
                    value={limit}
                    onChange={(e) => setLimit(e.target.value)}
                    slotProps={{ htmlInput: { min: 0, step: 'any' } }}
                    fullWidth
                  />
                  <TextField
                    select
                    label="单位"
                    value={unit}
                    onChange={(e) => {
                      const next = Number(e.target.value);
                      setLimit(String(Number(limit) * 1024 ** (unit - next)));
                      setUnit(next);
                    }}
                    sx={{ minWidth: 100 }}
                  >
                    {['B', 'KiB', 'MiB', 'GiB', 'TiB'].map((u, i) => (
                      <MenuItem key={u} value={i}>
                        {u}
                      </MenuItem>
                    ))}
                  </TextField>
                </Box>
                <TextField
                  select
                  label="每月重置日"
                  value={resetDay}
                  onChange={(e) => setResetDay(Number(e.target.value))}
                >
                  {Array.from({ length: 32 }, (_, i) => (
                    <MenuItem key={i} value={i}>
                      {i === 0 ? '不自动重置' : `${i} 日`}
                    </MenuItem>
                  ))}
                </TextField>
                <TextField
                  label="到期日（留空为无到期）"
                  type="date"
                  value={expire}
                  onChange={(e) => setExpire(e.target.value)}
                  slotProps={{ inputLabel: { shrink: true } }}
                />
                <Alert severity="info">
                  不足指定重置日的月份使用月末；到期日按面板时区当日结束计算。
                </Alert>
              </>
            )}
            {tab === 'assign' && <AssignmentPicker value={ids} onChange={setIds} />}
            {tab === 'credentials' && current && (
              <>
                <CopyField secret label="UUID" value={current.uuid ?? ''} />
                <CopyField secret label="密码" value={current.password ?? ''} />
                <CopyField secret label="SS 用户密钥" value={current.ss_user_key ?? ''} />
                <Button color="error" onClick={() => setConfirm(true)}>
                  重新生成凭据
                </Button>
              </>
            )}
          </Box>
          {ids.length === 0 && tab !== 'assign' && (
            <Alert severity="warning" sx={{ mt: 2 }}>
              当前未分配入站，保存后订阅没有代理节点。
            </Alert>
          )}
        </DialogContent>
        <DialogActions>
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button variant="contained" loading={busy} onClick={save}>
            保存
          </Button>
        </DialogActions>
      </Dialog>
      {confirm && current && (
        <ActionDialog
          title="重新生成凭据"
          danger
          onClose={() => setConfirm(false)}
          onConfirm={async () => {
            setCurrent(await subscriberAction(current.id, 'regenerate-credentials'));
            setConfirm(false);
          }}
        >
          旧代理凭据会失效，客户端需刷新订阅。订阅 URL 保持不变。
        </ActionDialog>
      )}
    </>
  );
}
