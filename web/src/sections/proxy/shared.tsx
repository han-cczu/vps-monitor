import { useState } from 'react';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import TextField from '@mui/material/TextField';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';

import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success('已复制');
  } catch {
    toast.error('复制失败，请手动选择文本复制');
  }
}

export function CopyField({
  label,
  value,
  secret = false,
}: {
  label: string;
  value: string;
  secret?: boolean;
}) {
  const [visible, setVisible] = useState(false);
  return (
    <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
      <TextField
        fullWidth
        size="small"
        label={label}
        value={value}
        type={secret && !visible ? 'password' : 'text'}
        slotProps={{ input: { readOnly: true }, htmlInput: { style: { fontFamily: 'monospace' } } }}
      />
      {secret && (
        <Button
          size="small"
          onClick={() => setVisible(!visible)}
          aria-label={`${visible ? '隐藏' : '显示'}${label}`}
        >
          {visible ? '隐藏' : '显示'}
        </Button>
      )}
      <Button size="small" aria-label={`复制${label}`} onClick={() => copyText(value)}>
        复制
      </Button>
    </Box>
  );
}

export function ActionDialog({
  title,
  children,
  confirmLabel = '确认',
  danger = false,
  disabled = false,
  onClose,
  onConfirm,
}: {
  title: string;
  children: React.ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  disabled?: boolean;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  return (
    <Dialog
      open
      fullWidth
      maxWidth="sm"
      onClose={() => {
        if (!busy) onClose();
      }}
    >
      <DialogTitle>{title}</DialogTitle>
      <DialogContent>
        {error && (
          <Alert severity="error" sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}
        {children}
      </DialogContent>
      <DialogActions>
        <Button color="inherit" disabled={busy} onClick={onClose}>
          取消
        </Button>
        <Button
          variant="contained"
          color={danger ? 'error' : 'primary'}
          disabled={disabled}
          loading={busy}
          onClick={async () => {
            setBusy(true);
            setError('');
            try {
              await onConfirm();
              onClose();
            } catch (err) {
              const message = getErrorMessage(err);
              setError(message);
              toast.error(message);
            } finally {
              setBusy(false);
            }
          }}
        >
          {confirmLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
