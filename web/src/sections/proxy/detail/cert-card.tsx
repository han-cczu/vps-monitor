import type { NodeCert } from 'src/types/proxy';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Button from '@mui/material/Button';
import Typography from '@mui/material/Typography';

import { regenerateCert } from 'src/api/proxy';

import { toast } from 'src/components/snackbar';

import { copyText, ActionDialog } from '../shared';

export function CertCard({
  serverId,
  cert,
  onSaved,
}: {
  serverId: number;
  cert: NodeCert | null;
  onSaved: () => Promise<void>;
}) {
  const [confirm, setConfirm] = useState(false);
  return (
    <Card sx={{ p: { xs: 2, sm: 3 } }}>
      <Typography variant="h6" sx={{ mb: 2 }}>
        节点证书
      </Typography>
      {cert ? (
        <>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            伪装 SNI：{cert.sni}
          </Typography>
          <Typography variant="body2" sx={{ mt: 1 }}>
            有效期至：{new Date(cert.not_after * 1000).toLocaleDateString()}
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center', mt: 1 }}>
            <Typography variant="subtitle2" sx={{ flex: 1 }}>
              SHA-256 指纹
            </Typography>
            <Button size="small" onClick={() => copyText(cert.fingerprint_sha256)}>
              复制指纹
            </Button>
          </Box>
          <Box
            component="code"
            sx={{
              display: 'block',
              overflowWrap: 'anywhere',
              fontSize: 12,
              color: 'text.secondary',
              mt: 1,
            }}
          >
            {cert.fingerprint_sha256}
          </Box>
          <Button color="warning" sx={{ mt: 2 }} onClick={() => setConfirm(true)}>
            重新生成证书
          </Button>
        </>
      ) : (
        <Typography variant="body2" color="text.secondary">
          创建 Hysteria2 或 TUIC 入站时自动生成。
        </Typography>
      )}
      {confirm && (
        <ActionDialog
          title="重新生成证书"
          danger
          onClose={() => setConfirm(false)}
          onConfirm={async () => {
            await regenerateCert(serverId);
            await onSaved();
            toast.success('证书已重新生成，等待节点应用');
          }}
        >
          <Typography>
            重新生成后所有 Hysteria2 / TUIC 订阅中的指纹失效，客户端需刷新订阅。
          </Typography>
        </ActionDialog>
      )}
    </Card>
  );
}
