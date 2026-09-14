import { useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import Button from '@mui/material/Button';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';

import { useTOTP, setupTOTP, toggleTOTP } from 'src/api/settings';

import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

export function SecurityView() {
  const { data, error, isLoading, mutate } = useTOTP();
  const [password, setPassword] = useState('');
  const [setup, setSetup] = useState<{ secret: string; url: string } | null>(null);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const begin = async () => {
    setBusy(true);
    setMessage('');
    try {
      setSetup(await setupTOTP(password));
      setPassword('');
    } catch (err) {
      setMessage(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  const toggle = async () => {
    setBusy(true);
    setMessage('');
    try {
      await toggleTOTP(!data?.enabled, code);
      setSetup(null);
      setCode('');
      await mutate();
      toast.success(data?.enabled ? '二步验证已禁用' : '二步验证已启用');
    } catch (err) {
      setMessage(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card sx={{ p: { xs: 2, md: 3 }, mt: 3 }}>
      <Stack spacing={2} sx={{ maxWidth: 600 }}>
        <Typography variant="h6">二步验证（TOTP）</Typography>
        {error && <Alert severity="error">{getErrorMessage(error)}</Alert>}
        {message && <Alert severity="error">{message}</Alert>}
        {data && (
          <Alert severity={data.enabled ? 'success' : 'info'}>
            {data.enabled
              ? '已启用，登录时需要验证器中的六位验证码。'
              : '未启用。绑定验证器后，密码通过仍需验证码才能登录。'}
          </Alert>
        )}
        {data && !data.enabled && !setup && (
          <>
            <TextField
              label="当前密码"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Button
              variant="contained"
              disabled={!password || isLoading}
              loading={busy}
              onClick={begin}
            >
              绑定验证器
            </Button>
          </>
        )}
        {setup && (
          <>
            <Typography>在验证器中扫描二维码，或手动输入密钥。请勿向他人展示。</Typography>
            <Box sx={{ p: 2, bgcolor: 'white', width: 'fit-content', maxWidth: 1 }}>
              <QRCodeSVG
                value={setup.url}
                size={200}
                style={{ maxWidth: '100%', height: 'auto' }}
              />
            </Box>
            <Typography component="code" sx={{ overflowWrap: 'anywhere' }}>
              {setup.secret}
            </Typography>
          </>
        )}
        {(setup || data?.enabled) && (
          <>
            <TextField
              label="六位验证码"
              autoComplete="one-time-code"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
              slotProps={{ htmlInput: { inputMode: 'numeric', maxLength: 6, pattern: '[0-9]{6}' } }}
              helperText="已用验证码90秒内不能再次使用；请等验证码变化。"
            />
            <Button
              color={data?.enabled ? 'error' : 'primary'}
              variant="contained"
              disabled={code.length !== 6}
              loading={busy}
              onClick={toggle}
            >
              {data?.enabled ? '验证并禁用二步验证' : '验证并启用二步验证'}
            </Button>
            {setup && (
              <Button
                onClick={() => {
                  setSetup(null);
                  setCode('');
                }}
              >
                取消绑定
              </Button>
            )}
          </>
        )}
        <Typography variant="body2" color="text.secondary">
          丢失验证器时，由服务器管理员执行 reset-totp 找回。更换面板 JWT 密钥前请先禁用二步验证。
        </Typography>
      </Stack>
    </Card>
  );
}
