import type { TrafficCalibration } from 'src/types/server';
import type { CalibrationUnit } from 'src/utils/traffic-calibration';

import { useSWRConfig } from 'swr';
import { useState, useEffect } from 'react';

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
import CircularProgress from '@mui/material/CircularProgress';

import { formatBytes, formatPanelDate } from 'src/utils/format';
import {
  calibrationGB,
  calibratedUsage,
  calibrationBytes,
  CALIBRATION_UNITS,
} from 'src/utils/traffic-calibration';

import axios from 'src/lib/axios';

import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

const MODE_LABELS = { in: '入站', out: '出站', sum: '双向合计（入站 + 出站）', max: '取较大者' };

export function TrafficCalibrationDialog({
  serverId,
  serverName,
  onClose,
}: {
  serverId: number;
  serverName: string;
  onClose: () => void;
}) {
  const { mutate } = useSWRConfig();
  const [snapshot, setSnapshot] = useState<TrafficCalibration | null>(null);
  const [inbound, setInbound] = useState('');
  const [outbound, setOutbound] = useState('');
  const [inUnit, setInUnit] = useState<CalibrationUnit>('GB');
  const [outUnit, setOutUnit] = useState<CalibrationUnit>('GB');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const url = `/api/servers/${serverId}/traffic/calibration`;

  useEffect(() => {
    let active = true;
    axios
      .get<TrafficCalibration>(url)
      .then(({ data }) => {
        if (!active) return;
        setSnapshot(data);
        setInbound(calibrationGB(data.in));
        setOutbound(calibrationGB(data.out));
        setInUnit('GB');
        setOutUnit('GB');
      })
      .catch((err) => {
        if (active) setError(getErrorMessage(err));
      });
    return () => {
      active = false;
    };
  }, [url, loadAttempt]);

  const inBytes = calibrationBytes(inbound, inUnit);
  const outBytes = calibrationBytes(outbound, outUnit);
  const valid = inBytes !== null && outBytes !== null && Number.isSafeInteger(inBytes + outBytes);
  const used = snapshot && valid ? calibratedUsage(inBytes, outBytes, snapshot.mode) : null;

  const save = async () => {
    if (!snapshot || !valid || saving) return;
    setSaving(true);
    setError('');
    try {
      await axios.post<TrafficCalibration>(url, {
        period_start: snapshot.period_start,
        calibration_revision: snapshot.calibration_revision,
        mode: snapshot.mode,
        reset_day: snapshot.reset_day,
        in: inBytes,
        out: outBytes,
      });
      toast.success('本期入站、出站流量已校准');
      // Cache refresh failure must not invite resubmission of a committed change.
      void Promise.allSettled([
        mutate(`/api/servers/${serverId}/traffic?months=12`),
        mutate(`/api/servers/${serverId}`),
        mutate('/api/servers'),
      ]);
      onClose();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const reload = () => {
    setError('');
    setSnapshot(null);
    setLoadAttempt((value) => value + 1);
  };

  return (
    <Dialog open onClose={saving ? undefined : onClose} fullWidth maxWidth="sm">
      <DialogTitle>校准本期流量 · {serverName}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 2 }}>
        <Alert severity="info">
          分别填写服务商显示的本账期已用入站和出站总量，包含安装探针前的用量。
          保存会替换本期累计值，以保存时最近一次探针采样为起点继续累计；下个账期自动归零。
        </Alert>
        <Typography variant="body2" color="text.secondary">
          请核对服务商的账期、统计时间和单位。GB/TB 按 1000 进制，GiB/TiB 按 1024 进制；不足 1
          字节四舍五入。开机累计不受校准影响。
        </Typography>
        {error && <Alert severity="error">{error}</Alert>}
        {!snapshot && !error && <CircularProgress size={24} aria-label="加载本期流量" />}
        {snapshot && (
          <>
            <Typography variant="body2">
              本期 {formatPanelDate(snapshot.period_start)} —{' '}
              {formatPanelDate(snapshot.period_end_expected)}
              {' · '}
              {MODE_LABELS[snapshot.mode]}
            </Typography>
            <Typography variant="body2" color="text.secondary">
              打开时：入站 {formatBytes(snapshot.in)}，出站 {formatBytes(snapshot.out)}。
              {snapshot.calibrated_at > 0 &&
                ` 上次校准：${formatPanelDate(snapshot.calibrated_at)}。`}
            </Typography>
            {!snapshot.ready && (
              <Alert severity="warning">尚无近期探针采样，请等待节点恢复上报后重新加载。</Alert>
            )}
            <Box sx={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 110px', gap: 2 }}>
              <TextField
                label="本期已用入站"
                value={inbound}
                disabled={saving}
                onChange={(event) => setInbound(event.target.value)}
                slotProps={{ htmlInput: { inputMode: 'decimal', maxLength: 40 } }}
                error={inBytes === null}
                helperText={
                  inBytes === null ? '请输入非负数字，最多 12 位小数' : '服务商的入站累计总量'
                }
              />
              <TextField
                select
                label="入站单位"
                value={inUnit}
                disabled={saving}
                onChange={(event) => setInUnit(event.target.value as CalibrationUnit)}
              >
                {CALIBRATION_UNITS.map((unit) => (
                  <MenuItem key={unit} value={unit}>
                    {unit}
                  </MenuItem>
                ))}
              </TextField>
              <TextField
                label="本期已用出站"
                value={outbound}
                disabled={saving}
                onChange={(event) => setOutbound(event.target.value)}
                slotProps={{ htmlInput: { inputMode: 'decimal', maxLength: 40 } }}
                error={outBytes === null}
                helperText={
                  outBytes === null ? '请输入非负数字，最多 12 位小数' : '服务商的出站累计总量'
                }
              />
              <TextField
                select
                label="出站单位"
                value={outUnit}
                disabled={saving}
                onChange={(event) => setOutUnit(event.target.value as CalibrationUnit)}
              >
                {CALIBRATION_UNITS.map((unit) => (
                  <MenuItem key={unit} value={unit}>
                    {unit}
                  </MenuItem>
                ))}
              </TextField>
            </Box>
            {used !== null && (
              <Alert
                severity={snapshot.limit > 0 && used >= snapshot.limit ? 'warning' : 'success'}
              >
                校准后计费用量：{formatBytes(used)}（{MODE_LABELS[snapshot.mode]}）
                {snapshot.limit > 0 &&
                  `，本期剩余 ${formatBytes(Math.max(0, snapshot.limit - used))}`}
              </Alert>
            )}
            {inBytes !== null && outBytes !== null && !valid && (
              <Alert severity="error">入站与出站合计过大，请检查数值和单位。</Alert>
            )}
          </>
        )}
      </DialogContent>
      <DialogActions>
        {(error || (snapshot && !snapshot.ready)) && (
          <Button onClick={reload} disabled={saving}>
            重新加载
          </Button>
        )}
        <Button onClick={onClose} disabled={saving}>
          取消
        </Button>
        <Button variant="contained" onClick={save} disabled={!snapshot?.ready || !valid || saving}>
          {saving ? '正在保存…' : '保存校准'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
