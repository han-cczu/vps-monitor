import useSWR from 'swr';
import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';

import axios, { fetcher } from 'src/lib/axios';

import { getErrorMessage } from 'src/auth/utils';

import { AccountLayout } from '../../account/account-layout';

type Settings = { 'sub.clash_template': string; 'enforce.count_mode': 'sum' | 'download' };
function TemplateForm({
  settings,
  onSaved,
}: {
  settings: Settings;
  onSaved: () => Promise<unknown>;
}) {
  const [template, setTemplate] = useState(settings['sub.clash_template'] ?? '');
  const [mode, setMode] = useState(settings['enforce.count_mode'] ?? 'sum');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const save = async () => {
    setBusy(true);
    setError('');
    setSaved(false);
    try {
      await axios.put('/api/settings', {
        'sub.clash_template': template,
        'enforce.count_mode': mode,
      });
      await onSaved();
      setSaved(true);
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card sx={{ p: 3 }}>
      <Alert severity="info" sx={{ mb: 3 }}>
        {
          '模板必须包含一次 {{PROXIES}}（完整 proxies 段）和 {{PROXY_NAMES}}（带引号的名称列表）。留空保存使用默认模板。'
        }
      </Alert>
      {error && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}
      {saved && (
        <Alert severity="success" sx={{ mb: 2 }}>
          已保存，订阅缓存已失效。
        </Alert>
      )}
      <TextField
        label="Clash 订阅模板"
        fullWidth
        multiline
        minRows={14}
        value={template}
        onChange={(e) => {
          setTemplate(e.target.value);
          setSaved(false);
        }}
        slotProps={{ htmlInput: { style: { fontFamily: 'monospace' } } }}
      />
      <TextField
        fullWidth
        select
        label="计费流量模式"
        value={mode}
        onChange={(e) => setMode(e.target.value as Settings['enforce.count_mode'])}
        sx={{ mt: 3 }}
        helperText="仅影响后续新增流量，不回溯历史累计；按节点/每日分账仍显示原始上下行。"
      >
        <MenuItem value="sum">上行 + 下行</MenuItem>
        <MenuItem value="download">仅下行</MenuItem>
      </TextField>
      <Box sx={{ display: 'flex', gap: 2, mt: 3 }}>
        <Button
          disabled={busy}
          onClick={() => {
            setTemplate('');
            setSaved(false);
          }}
        >
          恢复默认
        </Button>
        <Button variant="contained" loading={busy} onClick={save}>
          保存
        </Button>
      </Box>
    </Card>
  );
}
export function SubscriptionSettingsView() {
  const result = useSWR<Settings>('/api/settings', fetcher);
  return (
    <AccountLayout>
      {result.error && <Alert severity="error">{getErrorMessage(result.error)}</Alert>}
      {result.data && <TemplateForm settings={result.data} onSaved={() => result.mutate()} />}
    </AccountLayout>
  );
}
