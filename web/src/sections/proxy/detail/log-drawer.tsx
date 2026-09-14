import { useRef, useState, useEffect } from 'react';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Drawer from '@mui/material/Drawer';
import Button from '@mui/material/Button';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import LinearProgress from '@mui/material/LinearProgress';

import { fetchLogs } from 'src/api/proxy';

import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

import { copyText } from '../shared';
import { plainLog } from '../helpers';

export function LogDrawer({
  serverId,
  online,
  onClose,
}: {
  serverId: number;
  online: boolean;
  onClose: () => void;
}) {
  const [lines, setLines] = useState(200);
  const [refresh, setRefresh] = useState(0);
  const [text, setText] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const pre = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (!online) return undefined;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    setText('');
    fetchLogs(serverId, lines, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setText(plainLog(value));
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          const message = getErrorMessage(err);
          setError(message);
          toast.error(message);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [serverId, lines, refresh, online]);
  useEffect(() => {
    if (pre.current) pre.current.scrollTop = pre.current.scrollHeight;
  }, [text]);
  return (
    <Drawer
      open
      anchor="right"
      onClose={onClose}
      slotProps={{
        paper: { sx: { width: { xs: '100%', sm: 640 }, maxWidth: '100%', p: { xs: 2, sm: 3 } } },
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', mb: 2 }}>
        <Typography variant="h6" sx={{ flex: 1 }}>
          核心日志
        </Typography>
        <Button color="inherit" onClick={onClose}>
          关闭
        </Button>
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
        <TextField
          select
          size="small"
          label="行数"
          value={lines}
          disabled={loading || !online}
          onChange={(e) => setLines(Number(e.target.value))}
          sx={{ width: 100 }}
        >
          {[100, 200, 500].map((n) => (
            <MenuItem key={n} value={n}>
              {n}
            </MenuItem>
          ))}
        </TextField>
        <Button
          loading={loading && online}
          disabled={!online}
          onClick={() => setRefresh((n) => n + 1)}
        >
          刷新
        </Button>
        <Button disabled={!text || loading} onClick={() => copyText(text)}>
          复制日志
        </Button>
      </Box>
      {!online && <Alert severity="info">Agent 离线，无法读取新日志。</Alert>}
      {error && <Alert severity="error">{error}</Alert>}
      {loading && online && <LinearProgress aria-label="正在读取日志" />}
      <Box
        component="pre"
        ref={pre}
        tabIndex={0}
        aria-label="日志内容"
        sx={{
          flex: 1,
          minHeight: 0,
          overflow: 'auto',
          bgcolor: 'background.neutral',
          borderRadius: 1,
          p: 2,
          fontSize: 12,
          whiteSpace: 'pre',
          lineHeight: 1.7,
        }}
      >
        {text || (!loading && !error ? '暂无日志' : '')}
      </Box>
    </Drawer>
  );
}
