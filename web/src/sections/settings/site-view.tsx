import type { SiteSettings } from 'src/api/settings';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import Button from '@mui/material/Button';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';

import { useSiteSettings, saveSiteSettings } from 'src/api/settings';

import { toast } from 'src/components/snackbar';
import { UpdateCheckCard } from 'src/components/update-check/update-check-card';

import { getErrorMessage } from 'src/auth/utils';

const retention = [
  ['retention.metrics_minute_days', '分钟指标保留天数'],
  ['retention.metrics_hour_days', '小时指标保留天数'],
  ['retention.ping_days', 'Ping 记录保留天数'],
  ['retention.audit_days', '审计记录保留天数'],
] as const;
export function SiteView() {
  const { data, error, isLoading, mutate } = useSiteSettings();
  const [draft, setDraft] = useState<Partial<SiteSettings>>({});
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const values = data ? { ...data, ...draft } : null;
  const change = <K extends keyof SiteSettings>(key: K, value: SiteSettings[K]) =>
    setDraft((old) => ({ ...old, [key]: value }));
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setMessage('');
    try {
      const result = await saveSiteSettings(draft);
      await mutate(result, false);
      setDraft({});
      toast.success('站点设置已保存');
    } catch (err) {
      setMessage(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Stack spacing={3}>
      <UpdateCheckCard />
      <Card sx={{ p: { xs: 2, md: 3 } }}>
        <Stack spacing={3} component="form" onSubmit={save}>
          <Typography variant="h6">站点设置</Typography>
          {(error || message) && (
            <Alert severity="error">{message || getErrorMessage(error)}</Alert>
          )}
          {isLoading && <Typography>正在读取设置…</Typography>}
          {values && (
            <>
              <Box
                sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, gap: 3 }}
              >
                <TextField
                  required
                  label="站点标题"
                  value={values['site.title']}
                  onChange={(e) => change('site.title', e.target.value)}
                  slotProps={{ htmlInput: { maxLength: 80 } }}
                />
                <TextField
                  required
                  label="业务时区"
                  value={values['site.tz']}
                  onChange={(e) => change('site.tz', e.target.value)}
                  helperText="例如 Asia/Shanghai；下次账期滚动及到期判断生效。"
                />
                <TextField
                  select
                  label="内存 / 磁盘显示单位"
                  value={values['site.bytes_base']}
                  onChange={(e) => change('site.bytes_base', Number(e.target.value) as 1000 | 1024)}
                  helperText="流量套餐、用量和网速统一按 1000 进制显示（GB / TB）。"
                >
                  <MenuItem value={1000}>1000（KB / MB / GB）</MenuItem>
                  <MenuItem value={1024}>1024（KiB / MiB / GiB）</MenuItem>
                </TextField>
                <TextField
                  select
                  label="订阅用户流量统计口径"
                  value={values['enforce.count_mode']}
                  onChange={(e) =>
                    change('enforce.count_mode', e.target.value as 'sum' | 'download')
                  }
                  helperText="下次采样起生效；已累计的历史不重新计算。"
                >
                  <MenuItem value="sum">上传 + 下载</MenuItem>
                  <MenuItem value="download">仅下载</MenuItem>
                </TextField>
                {retention.map(([key, label]) => (
                  <TextField
                    key={key}
                    required
                    type="number"
                    label={label}
                    value={values[key]}
                    onChange={(e) => change(key, Number(e.target.value))}
                    slotProps={{ htmlInput: { min: 1, max: 3650, step: 1 } }}
                  />
                ))}
                <TextField
                  required
                  type="number"
                  label="告警冷却（分钟）"
                  value={values['alert.cooldown_minutes']}
                  onChange={(e) => change('alert.cooldown_minutes', Number(e.target.value))}
                  slotProps={{ htmlInput: { min: 0, max: 10080, step: 1 } }}
                />
              </Box>
              <Alert severity="info">
                缩短保留期后，下一次清理会删除超期历史；请按需要先备份。
              </Alert>
              <Button
                type="submit"
                variant="contained"
                loading={busy}
                disabled={!Object.keys(draft).length}
                sx={{ alignSelf: { xs: 'stretch', sm: 'flex-start' } }}
              >
                保存设置
              </Button>
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
}
