import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Link from '@mui/material/Link';
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import Button from '@mui/material/Button';
import Typography from '@mui/material/Typography';

import { useUpdates, checkUpdates } from 'src/api/updates';

import { getErrorMessage } from 'src/auth/utils';

import { latestLabel, latestValue, updateSummary } from './status';

// 检查更新卡片：站点设置与节点页共用。
//
// 它只展示检查结果，不提供安装入口——面板与探针的升级仍走既有的人工部署、
// 以及节点页原有的逐台/批量升级流程。
export function UpdateCheckCard() {
  const { data, error, mutate } = useUpdates();
  const [busy, setBusy] = useState(false);
  const [checkError, setCheckError] = useState('');
  const [reused, setReused] = useState(false);

  const check = async () => {
    const checkedAt = data?.checked_at;
    setBusy(true);
    setCheckError('');
    setReused(false);
    try {
      const next = await checkUpdates();
      // 检查器对成功结果缓存 10 分钟、失败结果 1 分钟：检查时间没变就是复用了缓存。
      setReused(checkedAt !== undefined && next.checked_at === checkedAt);
      await mutate(next, false);
    } catch (err) {
      setCheckError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const summary = data ? updateSummary(data) : null;
  const cells: [string, string][] = [
    ['当前面板', data?.panel_version || '—'],
    ['面板携带的探针', data?.agent_version || '未部署'],
    [
      latestLabel(data?.state ?? 'unchecked', Boolean(data?.latest)),
      latestValue(data?.state ?? 'unchecked', data?.latest?.version),
    ],
  ];

  return (
    <Card sx={{ p: { xs: 2, md: 3 } }}>
      <Stack spacing={2}>
        <Box
          sx={{
            gap: 2,
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <Typography variant="h6">版本与更新</Typography>
          <Button variant="outlined" loading={busy} onClick={check}>
            检查更新
          </Button>
        </Box>
        {(checkError || error) && (
          <Alert severity="error">{checkError || getErrorMessage(error)}</Alert>
        )}
        <Box
          sx={{ gap: 2, display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(3, 1fr)' } }}
        >
          {cells.map(([label, value]) => (
            <Box key={label} sx={{ minWidth: 0 }}>
              <Typography variant="body2" color="text.secondary">
                {label}
              </Typography>
              <Typography variant="subtitle1" sx={{ overflowWrap: 'anywhere' }}>
                {value}
              </Typography>
            </Box>
          ))}
        </Box>
        <Box aria-live="polite" aria-busy={busy}>
          {data?.state === 'error' && (
            <Alert severity="error">
              {data.message}
              {data.latest && ' 上面显示的版本来自上次成功检查。'}
            </Alert>
          )}
          {data?.state === 'ok' && !data.latest && (
            <Alert severity="info">
              更新源还没有稳定版本发布（草稿和预发布不计），暂时无法比较面板与探针版本。
            </Alert>
          )}
          {summary && (
            <Alert severity={summary.severity}>
              <Stack spacing={0.5}>
                <span>{summary.panel}</span>
                <span>{summary.agent}</span>
                <Link
                  href={summary.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  sx={{ alignSelf: 'flex-start' }}
                >
                  查看发布说明
                </Link>
              </Stack>
            </Alert>
          )}
        </Box>
        {data && (
          <Typography variant="caption" color="text.secondary">
            更新源：{data.repository}。
            {data.checked_at
              ? `上次检查：${new Date(data.checked_at * 1000).toLocaleString()}；${
                  data.state === 'error' ? '失败后 1 分钟可重试' : '10 分钟内复用检查结果'
                }。`
              : '点击检查更新，查询面板和探针的最新稳定版。'}
            {reused && ' 本次点击复用了缓存，没有重新查询更新源。'}
          </Typography>
        )}
      </Stack>
    </Card>
  );
}
