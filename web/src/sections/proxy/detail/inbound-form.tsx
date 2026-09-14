import type * as z from 'zod';
import type { Inbound, ProxyProtocol } from 'src/types/proxy';

import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import MenuItem from '@mui/material/MenuItem';
import Typography from '@mui/material/Typography';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';

import { createInbound, updateInbound } from 'src/api/proxy';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';
import { Form, Field } from 'src/components/hook-form';

import { getErrorMessage } from 'src/auth/utils';

import { CopyField } from '../shared';
import { PROTOCOLS, inboundSchema, inboundPayload, inboundDefaults } from '../helpers';

export function InboundForm({
  serverId,
  inbounds,
  current,
  onClose,
  onSaved,
}: {
  serverId: number;
  inbounds: Inbound[];
  current?: Inbound;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [error, setError] = useState('');
  const methods = useForm<z.input<typeof inboundSchema>, unknown, z.output<typeof inboundSchema>>({
    resolver: zodResolver(inboundSchema),
    defaultValues: inboundDefaults(current?.protocol ?? 'vless', inbounds, current),
  });
  const {
    watch,
    reset,
    handleSubmit,
    formState: { isSubmitting },
  } = methods;
  const protocol = watch('protocol');
  const submit = handleSubmit(async (values) => {
    setError('');
    if (current && values.protocol === 'vless' && !values.short_ids.length) {
      methods.setError('short_ids', { message: '编辑时至少保留一个 Short ID' });
      return;
    }
    try {
      const payload = inboundPayload(values);
      if (current) await updateInbound(current.id, payload);
      else await createInbound(serverId, payload);
      toast.success('已保存，等待节点应用');
      await onSaved();
      onClose();
    } catch (err) {
      const message = getErrorMessage(err);
      setError(message);
      toast.error(message);
    }
  });
  const settings = current?.settings;
  return (
    <Dialog
      open
      fullWidth
      maxWidth="sm"
      onClose={() => {
        if (!isSubmitting) onClose();
      }}
    >
      <DialogTitle>{current ? '编辑入站' : '新增入站'}</DialogTitle>
      <DialogContent dividers>
        <Form methods={methods} onSubmit={submit}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5 }}>
            {error && <Alert severity="error">{error}</Alert>}
            <Box
              role="group"
              aria-label="协议"
              sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 1 }}
            >
              {(Object.keys(PROTOCOLS) as ProxyProtocol[]).map((p) => (
                <Button
                  key={p}
                  disabled={isSubmitting || (!!current && p !== protocol)}
                  aria-pressed={p === protocol}
                  variant={p === protocol ? 'outlined' : 'text'}
                  onClick={() => {
                    if (!current && p !== protocol) {
                      reset(inboundDefaults(p, inbounds));
                      setError('');
                    }
                  }}
                  sx={{
                    p: 1.5,
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                    textAlign: 'left',
                    gap: 0.75,
                  }}
                >
                  <Label color={PROTOCOLS[p].color}>{PROTOCOLS[p].label}</Label>
                  <Typography variant="caption" color="text.secondary">
                    {PROTOCOLS[p].description}
                  </Typography>
                </Button>
              ))}
            </Box>
            <Field.Text
              name="listen_port"
              label="监听端口"
              type="number"
              disabled={isSubmitting}
              helperText={`传输：${PROTOCOLS[protocol].transports.join(' + ').toUpperCase()}；停用入站仍保留端口`}
            />
            <Field.Text name="remark" label="备注" disabled={isSubmitting} />
            <Field.Switch name="enabled" label="启用入站" disabled={isSubmitting} />
            {protocol === 'vless' && (
              <>
                <Field.Text
                  name="handshake_server"
                  label="Reality 握手站点"
                  disabled={isSubmitting}
                  helperText="填写域名或 IP，例如 www.microsoft.com"
                />
                <Field.Text
                  name="handshake_port"
                  label="握手端口"
                  type="number"
                  disabled={isSubmitting}
                />
                <Field.Text
                  name="short_ids"
                  label="短标识（Short IDs）"
                  disabled={isSubmitting}
                  helperText={
                    current
                      ? '逗号或空格分隔；修改后客户端需刷新订阅'
                      : '可留空自动生成；逗号或空格分隔'
                  }
                />
                {settings?.public_key && (
                  <CopyField label="Reality 公钥" value={settings.public_key} />
                )}
                {settings?.private_key && (
                  <CopyField label="Reality 私钥" value={settings.private_key} secret />
                )}
              </>
            )}
            {protocol === 'shadowsocks' && (
              <>
                <Alert severity="info">固定加密方法：2022-blake3-aes-128-gcm</Alert>
                {settings?.server_psk && (
                  <CopyField label="服务端 PSK" value={settings.server_psk} secret />
                )}
              </>
            )}
            {protocol === 'hysteria2' && (
              <>
                <Field.Switch
                  name="obfs_enabled"
                  label="启用 Salamander 混淆"
                  disabled={isSubmitting}
                />
                <Field.Text
                  name="up_mbps"
                  label="上传带宽（Mbps）"
                  type="number"
                  disabled={isSubmitting}
                  helperText="0 表示不指定"
                />
                <Field.Text
                  name="down_mbps"
                  label="下载带宽（Mbps）"
                  type="number"
                  disabled={isSubmitting}
                  helperText="0 表示不指定"
                />
                <Field.Switch
                  name="ignore_client_bandwidth"
                  label="忽略客户端带宽设置"
                  disabled={isSubmitting}
                />
                {settings?.obfs_password && (
                  <CopyField label="混淆密码" value={settings.obfs_password} secret />
                )}
              </>
            )}
            {protocol === 'tuic' && (
              <>
                <Field.Select name="congestion_control" label="拥塞控制" disabled={isSubmitting}>
                  {['bbr', 'cubic', 'new_reno'].map((value) => (
                    <MenuItem key={value} value={value}>
                      {value}
                    </MenuItem>
                  ))}
                </Field.Select>
                <Field.Switch
                  name="zero_rtt"
                  label="启用 0-RTT"
                  disabled={isSubmitting}
                  helperText="可减少连接延迟；需客户端同时支持"
                />
              </>
            )}
            {!current && (
              <Typography variant="caption" color="text.secondary">
                密钥由服务端自动生成，保存后可在编辑窗口查看。HY2 / TUIC 共用节点证书。
              </Typography>
            )}
          </Box>
        </Form>
      </DialogContent>
      <DialogActions>
        <Button color="inherit" disabled={isSubmitting} onClick={onClose}>
          取消
        </Button>
        <Button variant="contained" loading={isSubmitting} onClick={submit}>
          保存
        </Button>
      </DialogActions>
    </Dialog>
  );
}
