import * as z from 'zod';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { useBoolean } from 'minimal-shared/hooks';
import { zodResolver } from '@hookform/resolvers/zod';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import TextField from '@mui/material/TextField';
import IconButton from '@mui/material/IconButton';
import InputAdornment from '@mui/material/InputAdornment';

import { useRouter, useSearchParams } from 'src/routes/hooks';

import { Iconify } from 'src/components/iconify';
import { Form, Field } from 'src/components/hook-form';

import { useAuthContext } from '../../hooks';
import { getErrorMessage } from '../../utils';
import { FormHead } from '../../components/form-head';
import { signInWithMFA, signInWithPassword } from '../../context/jwt';

// ----------------------------------------------------------------------

export type SignInSchemaType = z.infer<typeof SignInSchema>;

// 面板按用户名登录（初始管理员是 admin），不是邮箱；长度规则由服务端说了算
export const SignInSchema = z.object({
  username: z.string().trim().min(1, { error: '请输入用户名' }),
  password: z.string().min(1, { error: '请输入密码' }),
});

// ----------------------------------------------------------------------

export function JwtSignInView() {
  const router = useRouter();

  const searchParams = useSearchParams();

  const showPassword = useBoolean();

  const { checkUserSession } = useAuthContext();

  const [ticket, setTicket] = useState<string | null>(null);
  const [code, setCode] = useState('');
  const [mfaBusy, setMfaBusy] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  // 会话到期被踢回来时带 ?reason=expired
  const sessionExpired = searchParams.get('reason') === 'expired';

  const defaultValues: SignInSchemaType = {
    username: '',
    password: '',
  };

  const methods = useForm({
    resolver: zodResolver(SignInSchema),
    defaultValues,
  });

  const {
    handleSubmit,
    formState: { isSubmitting },
  } = methods;

  const onSubmit = handleSubmit(async (data) => {
    try {
      setErrorMessage(null);
      const result = await signInWithPassword({ username: data.username, password: data.password });
      if (result.mfaRequired) {
        setTicket(result.ticket);
        setCode('');
        methods.setValue('password', '');
        return;
      }
      await checkUserSession?.();

      router.refresh();
    } catch (error) {
      console.error(error);
      setErrorMessage(getErrorMessage(error));
    }
  });

  const renderForm = () => (
    <Box sx={{ gap: 3, display: 'flex', flexDirection: 'column' }}>
      <Field.Text
        name="username"
        label="用户名"
        autoComplete="username"
        slotProps={{ inputLabel: { shrink: true } }}
      />

      <Field.Text
        name="password"
        label="密码"
        type={showPassword.value ? 'text' : 'password'}
        autoComplete="current-password"
        slotProps={{
          inputLabel: { shrink: true },
          input: {
            endAdornment: (
              <InputAdornment position="end">
                <IconButton onClick={showPassword.onToggle} edge="end">
                  <Iconify icon={showPassword.value ? 'solar:eye-bold' : 'solar:eye-closed-bold'} />
                </IconButton>
              </InputAdornment>
            ),
          },
        }}
      />

      <Button
        fullWidth
        color="inherit"
        size="large"
        type="submit"
        variant="contained"
        loading={isSubmitting}
        loadingIndicator="登录中…"
      >
        登录
      </Button>
    </Box>
  );

  return (
    <>
      <FormHead
        title="登录"
        description="使用管理员账号登录面板"
        sx={{ textAlign: { xs: 'center', md: 'left' } }}
      />

      {sessionExpired && !errorMessage && (
        <Alert severity="info" sx={{ mb: 3 }}>
          登录已过期，请重新登录。
        </Alert>
      )}

      {!!errorMessage && (
        <Alert severity="error" sx={{ mb: 3 }}>
          {errorMessage}
        </Alert>
      )}

      {ticket ? (
        <Box
          component="form"
          onSubmit={async (e) => {
            e.preventDefault();
            setMfaBusy(true);
            setErrorMessage(null);
            try {
              await signInWithMFA(ticket, code);
              await checkUserSession?.();
              router.refresh();
            } catch (error) {
              setTicket(null);
              setCode('');
              setErrorMessage(getErrorMessage(error));
            } finally {
              setMfaBusy(false);
            }
          }}
          sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}
        >
          <Alert severity="info">
            请输入验证器中的验证码。验证步骤五分钟有效，每次提交后需重新登录。
          </Alert>
          <TextField
            autoFocus
            label="六位验证码"
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
            autoComplete="one-time-code"
            slotProps={{ htmlInput: { inputMode: 'numeric', maxLength: 6, pattern: '[0-9]{6}' } }}
          />
          <Button type="submit" variant="contained" loading={mfaBusy} disabled={code.length !== 6}>
            验证并登录
          </Button>
          <Button
            onClick={() => {
              setTicket(null);
              setCode('');
            }}
          >
            返回密码登录
          </Button>
        </Box>
      ) : (
        <Form methods={methods} onSubmit={onSubmit}>
          {renderForm()}
        </Form>
      )}
    </>
  );
}
