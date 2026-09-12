import * as z from 'zod';
import { useForm } from 'react-hook-form';
import { useBoolean } from 'minimal-shared/hooks';
import { zodResolver } from '@hookform/resolvers/zod';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Button from '@mui/material/Button';
import IconButton from '@mui/material/IconButton';
import InputAdornment from '@mui/material/InputAdornment';

import axios, { endpoints } from 'src/lib/axios';

import { toast } from 'src/components/snackbar';
import { Iconify } from 'src/components/iconify';
import { Form, Field } from 'src/components/hook-form';

import { getErrorMessage } from 'src/auth/utils';

// ----------------------------------------------------------------------

/** 与服务端 POST /api/auth/password 的规则一致：新密码至少 10 位 */
const MIN_PASSWORD_LENGTH = 10;

export type ChangePasswordSchemaType = z.infer<typeof ChangePasswordSchema>;

export const ChangePasswordSchema = z
  .object({
    oldPassword: z.string().min(1, { error: '请输入当前密码' }),
    newPassword: z
      .string()
      .min(1, { error: '请输入新密码' })
      .min(MIN_PASSWORD_LENGTH, { error: `新密码至少 ${MIN_PASSWORD_LENGTH} 位` }),
    confirmNewPassword: z.string().min(1, { error: '请再输入一次新密码' }),
  })
  .refine((val) => val.oldPassword !== val.newPassword, {
    error: '新密码不能与当前密码相同',
    path: ['newPassword'],
  })
  .refine((val) => val.newPassword === val.confirmNewPassword, {
    error: '两次输入的新密码不一致',
    path: ['confirmNewPassword'],
  });

// ----------------------------------------------------------------------

export function AccountChangePassword() {
  const showPassword = useBoolean();

  const defaultValues: ChangePasswordSchemaType = {
    oldPassword: '',
    newPassword: '',
    confirmNewPassword: '',
  };

  const methods = useForm({
    mode: 'all',
    resolver: zodResolver(ChangePasswordSchema),
    defaultValues,
  });

  const {
    reset,
    handleSubmit,
    formState: { isSubmitting },
  } = methods;

  const onSubmit = handleSubmit(async (data) => {
    try {
      await axios.post(endpoints.auth.password, {
        oldPassword: data.oldPassword,
        newPassword: data.newPassword,
      });
      reset();
      toast.success('密码已更新');
    } catch (error) {
      console.error(error);
      toast.error(getErrorMessage(error));
    }
  });

  const renderToggle = () => (
    <InputAdornment position="end">
      <IconButton onClick={showPassword.onToggle} edge="end">
        <Iconify icon={showPassword.value ? 'solar:eye-bold' : 'solar:eye-closed-bold'} />
      </IconButton>
    </InputAdornment>
  );

  return (
    <Form methods={methods} onSubmit={onSubmit}>
      <Card
        sx={{
          p: 3,
          gap: 3,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        <Field.Text
          name="oldPassword"
          type={showPassword.value ? 'text' : 'password'}
          label="当前密码"
          autoComplete="current-password"
          slotProps={{ input: { endAdornment: renderToggle() } }}
        />

        <Field.Text
          name="newPassword"
          label="新密码"
          type={showPassword.value ? 'text' : 'password'}
          autoComplete="new-password"
          slotProps={{ input: { endAdornment: renderToggle() } }}
          helperText={
            <Box component="span" sx={{ gap: 0.5, display: 'flex', alignItems: 'center' }}>
              <Iconify icon="solar:info-circle-bold" width={16} /> 至少 {MIN_PASSWORD_LENGTH} 位
            </Box>
          }
        />

        <Field.Text
          name="confirmNewPassword"
          type={showPassword.value ? 'text' : 'password'}
          label="确认新密码"
          autoComplete="new-password"
          slotProps={{ input: { endAdornment: renderToggle() } }}
        />

        <Button type="submit" variant="contained" loading={isSubmitting} sx={{ ml: 'auto' }}>
          保存
        </Button>
      </Card>
    </Form>
  );
}
