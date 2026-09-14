import type { AlertRule } from 'src/types/alert';

import useSWR from 'swr';
import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Table from '@mui/material/Table';
import Button from '@mui/material/Button';
import Switch from '@mui/material/Switch';
import TableRow from '@mui/material/TableRow';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableBody from '@mui/material/TableBody';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import TableContainer from '@mui/material/TableContainer';

import axios, { fetcher } from 'src/lib/axios';

import { toast } from 'src/components/snackbar';
import { LoadingScreen } from 'src/components/loading-screen';

import { getErrorMessage } from 'src/auth/utils';

import { RULE_INFO, parseThresholds } from './shared';

export function RulesView() {
  const { data, error, isLoading, mutate } = useSWR<{ rules: AlertRule[] }>(
    '/api/alert-rules',
    fetcher,
    { revalidateOnFocus: false }
  );
  const [draft, setDraft] = useState<AlertRule[] | null>(null);
  const [arrays, setArrays] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const rules = draft ?? data?.rules ?? [];
  const update = (kind: string, patch: Partial<AlertRule>) =>
    setDraft(rules.map((r) => (r.kind === kind ? { ...r, ...patch } : r)));
  const save = async () => {
    try {
      const payload = rules.map((r) => {
        const array = arrays[r.kind];
        if (array === undefined) return r;
        const field = r.kind === 'server.expire' ? 'days' : 'percents';
        const allowed =
          r.kind === 'server.expire'
            ? [7, 3, 1]
            : r.kind === 'server.traffic'
              ? [80, 90, 100]
              : [80, 100];
        return { ...r, params: { ...r.params, [field]: parseThresholds(array, allowed) } };
      });
      setBusy(true);
      await axios.put('/api/alert-rules', payload);
      await mutate();
      setDraft(null);
      setArrays({});
      toast.success('告警规则已保存');
    } catch (e) {
      toast.error(getErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  if (isLoading) return <LoadingScreen />;
  if (error) return <Alert severity="error">{getErrorMessage(error)}</Alert>;
  return (
    <Card sx={{ p: 2 }}>
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          gap: 2,
          mb: 2,
          alignItems: 'center',
        }}
      >
        <Typography variant="body2" color="text.secondary">
          资源持续时间 1–15 分钟。修改后按新的规则评估。
        </Typography>
        <Button
          variant="contained"
          onClick={save}
          loading={busy}
          disabled={!draft && !Object.keys(arrays).length}
        >
          保存规则
        </Button>
      </Box>
      <TableContainer>
        <Table aria-label="告警规则" sx={{ minWidth: 760 }}>
          <TableHead>
            <TableRow>
              <TableCell>启用</TableCell>
              <TableCell>规则</TableCell>
              <TableCell>参数</TableCell>
              <TableCell>说明</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rules.map((r) => (
              <TableRow key={r.kind}>
                <TableCell>
                  <Switch
                    checked={r.enabled}
                    disabled={busy}
                    onChange={(_, enabled) => update(r.kind, { enabled })}
                    slotProps={{
                      input: { 'aria-label': `启用${RULE_INFO[r.kind]?.name ?? r.kind}` },
                    }}
                  />
                </TableCell>
                <TableCell sx={{ minWidth: 130 }}>
                  <Typography variant="subtitle2">{RULE_INFO[r.kind]?.name ?? r.kind}</Typography>
                </TableCell>
                <TableCell>
                  <Box sx={{ display: 'flex', gap: 1, minWidth: 270 }}>
                    {r.params.minutes !== undefined && (
                      <TextField
                        size="small"
                        type="number"
                        label="分钟"
                        value={r.params.minutes}
                        disabled={busy}
                        sx={{ width: 105 }}
                        onChange={(e) =>
                          update(r.kind, {
                            params: { ...r.params, minutes: Number(e.target.value) },
                          })
                        }
                      />
                    )}
                    {r.params.percent !== undefined && (
                      <TextField
                        size="small"
                        type="number"
                        label="百分比"
                        value={r.params.percent}
                        disabled={busy}
                        sx={{ width: 105 }}
                        onChange={(e) =>
                          update(r.kind, {
                            params: { ...r.params, percent: Number(e.target.value) },
                          })
                        }
                      />
                    )}
                    {(r.params.percents || r.params.days) && (
                      <TextField
                        size="small"
                        label={r.params.days ? '提前天数（逗号分隔）' : '百分比（逗号分隔）'}
                        value={arrays[r.kind] ?? (r.params.days ?? r.params.percents)?.join(', ')}
                        disabled={busy}
                        onChange={(e) => setArrays({ ...arrays, [r.kind]: e.target.value })}
                      />
                    )}
                    {!Object.keys(r.params).length && (
                      <Typography color="text.secondary">无参数</Typography>
                    )}
                  </Box>
                </TableCell>
                <TableCell>
                  <Typography variant="body2" color="text.secondary">
                    {RULE_INFO[r.kind]?.description}
                  </Typography>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableContainer>
    </Card>
  );
}
