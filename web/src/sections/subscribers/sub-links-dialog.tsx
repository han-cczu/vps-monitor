import { useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';

import Box from '@mui/material/Box';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';

import { useSubscriber, subscriptionURL, subscriberAction } from 'src/api/subscribers';

import { getErrorMessage } from 'src/auth/utils';

import { CopyField, ActionDialog } from '../proxy/shared';

export const SUB_FORMATS = [
  {
    value: 'singbox',
    label: 'sing-box',
    description: '仅 outbounds 片段，合并到自己的 sing-box 配置；HY2/TUIC 内嵌证书验证。',
  },
  {
    value: 'uri',
    label: 'URI',
    description:
      'Base64 URI 列表，适用于 Shadowrocket/NekoBox 等兼容客户端。TUIC URI 不校验证书；HY2 需要客户端支持 pinSHA256。',
  },
  {
    value: 'clash',
    label: 'Clash',
    description: '适用于支持四协议的 mihomo 客户端，如 Clash Verge Rev。',
  },
  {
    value: 'clash-provider',
    label: 'Clash Provider',
    description: '仅代理列表，放到已有配置的 proxy-providers 中使用。',
  },
];
export function SubLinksDialog({ id, onClose }: { id: number; onClose: () => void }) {
  const { data, error, mutate } = useSubscriber(id);
  const [format, setFormat] = useState('clash');
  const [confirm, setConfirm] = useState(false);
  const url = data?.subscriber.sub_token ? subscriptionURL(data.subscriber.sub_token, format) : '';
  return (
    <>
      <Dialog open fullWidth maxWidth="sm" onClose={onClose}>
        <DialogTitle>订阅链接 · {data?.subscriber.name}</DialogTitle>
        <DialogContent>
          {error && <Alert severity="error">{getErrorMessage(error)}</Alert>}
          <Tabs
            value={format}
            onChange={(_, value) => setFormat(value)}
            variant="scrollable"
            sx={{ mb: 2 }}
          >
            {SUB_FORMATS.map((f) => (
              <Tab key={f.value} value={f.value} label={f.label} />
            ))}
          </Tabs>
          <Alert severity="info" sx={{ mb: 2 }}>
            {SUB_FORMATS.find((f) => f.value === format)?.description}{' '}
            链接和二维码等同密码，请妥善保管。
          </Alert>
          {url && (
            <>
              <CopyField label="订阅 URL" value={url} />
              <Box sx={{ p: 2, mt: 2, bgcolor: 'white', width: 'fit-content', mx: 'auto' }}>
                <QRCodeSVG value={url} size={224} title="订阅链接二维码" />
              </Box>
            </>
          )}
        </DialogContent>
        <DialogActions>
          <Button color="error" disabled={!url} onClick={() => setConfirm(true)}>
            重置订阅 token
          </Button>
          <Button onClick={onClose}>关闭</Button>
        </DialogActions>
      </Dialog>
      {confirm && (
        <ActionDialog
          title="重置订阅 token"
          danger
          onClose={() => setConfirm(false)}
          onConfirm={async () => {
            const subscriber = await subscriberAction(id, 'reset-token');
            await mutate({ subscriber }, false);
            setConfirm(false);
          }}
        >
          旧订阅链接和二维码将立即失效，已有代理凭据保持不变。
        </ActionDialog>
      )}
    </>
  );
}
