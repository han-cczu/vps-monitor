import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import Tooltip from '@mui/material/Tooltip';
import IconButton from '@mui/material/IconButton';
import Typography from '@mui/material/Typography';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';

import { toast } from 'src/components/snackbar';
import { Iconify } from 'src/components/iconify';

// ----------------------------------------------------------------------

export type ServerTokenInfo = {
  serverName: string;
  token: string;
  installCommand: string;
  /** 新建节点还是重置 token，只影响标题文案 */
  reason: 'created' | 'reset';
};

type Props = {
  info: ServerTokenInfo | null;
  onClose: () => void;
};

export function ServerTokenDialog({ info, onClose }: Props) {
  const copy = async (text: string, what: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(`${what}已复制`);
    } catch (error) {
      console.error(error);
      toast.error('浏览器不允许复制，请手动选中复制');
    }
  };

  return (
    <Dialog fullWidth maxWidth="sm" open={!!info} onClose={onClose}>
      <DialogTitle>{info?.reason === 'reset' ? '新的 agent token' : '节点已创建'}</DialogTitle>

      <DialogContent dividers sx={{ display: 'grid', gap: 2.5 }}>
        <Alert severity="warning">
          token 只显示这一次，关掉就看不到了。丢了只能重置——重置后这台节点上的 agent 需要重装。
        </Alert>

        {info?.reason === 'reset' && (
          <Typography variant="body2" sx={{ color: 'text.secondary' }}>
            旧 token 已立即失效。
          </Typography>
        )}

        <CopyBlock
          label="在节点上执行（一键安装）"
          value={info?.installCommand ?? ''}
          onCopy={() => copy(info?.installCommand ?? '', '安装命令')}
        />

        <CopyBlock
          label="agent token"
          value={info?.token ?? ''}
          onCopy={() => copy(info?.token ?? '', 'token')}
        />
      </DialogContent>

      <DialogActions>
        <Button variant="contained" onClick={onClose}>
          我已保存
        </Button>
      </DialogActions>
    </Dialog>
  );
}

// ----------------------------------------------------------------------

type CopyBlockProps = {
  label: string;
  value: string;
  onCopy: () => void;
};

function CopyBlock({ label, value, onCopy }: CopyBlockProps) {
  return (
    <Box sx={{ display: 'grid', gap: 1 }}>
      <Typography variant="subtitle2">{label}</Typography>

      <Box
        sx={(theme) => ({
          p: 1.5,
          gap: 1,
          display: 'flex',
          borderRadius: 1,
          alignItems: 'flex-start',
          bgcolor: theme.vars.palette.background.neutral,
        })}
      >
        <Box
          component="code"
          sx={{
            flexGrow: 1,
            fontSize: 13,
            minWidth: 0,
            overflowWrap: 'anywhere',
            fontFamily: 'monospace',
          }}
        >
          {value}
        </Box>

        <Tooltip title="复制">
          <IconButton size="small" onClick={onCopy}>
            <Iconify width={18} icon="solar:copy-bold" />
          </IconButton>
        </Tooltip>
      </Box>
    </Box>
  );
}
