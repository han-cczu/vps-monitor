import type { CoreArch } from 'src/types/corefiles';

import { useState } from 'react';

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
import LinearProgress from '@mui/material/LinearProgress';

import { fetchCoreFile, uploadCoreFile } from 'src/api/corefiles';

import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

export function CoreUploadDialog({
  mode,
  pinnedVersion,
  onClose,
  onSaved,
}: {
  mode: 'upload' | 'fetch';
  pinnedVersion: string;
  onClose: () => void;
  onSaved: () => Promise<unknown>;
}) {
  const [version, setVersion] = useState(pinnedVersion);
  const [arch, setArch] = useState<CoreArch>('amd64');
  const [file, setFile] = useState<File | null>(null);
  const [url, setUrl] = useState('');
  const [sha256, setSHA256] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const close = () => {
    if (!busy) onClose();
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!/^v\d{1,4}\.\d{1,4}\.\d{1,4}$/.test(version.trim())) {
      setError('版本格式为 v1.14.0。');
      return;
    }
    if (!/^[a-fA-F0-9]{64}$/.test(sha256.trim())) {
      setError('请输入 SHA256SUMS 中对应文件的 64 位校验和。');
      return;
    }
    if (mode === 'upload' && (!file || file.size > 64 * 1024 * 1024)) {
      setError('请选择不超过 64 MiB 的核心文件。');
      return;
    }
    setBusy(true);
    setError('');
    setProgress(0);
    try {
      if (mode === 'upload')
        await uploadCoreFile(version.trim(), arch, file!, sha256.trim(), setProgress);
      else await fetchCoreFile(version.trim(), arch, url.trim(), sha256.trim());
      toast.success(`${version.trim()} / ${arch} 已保存`);
      await onSaved();
      onClose();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open fullWidth maxWidth="sm" onClose={close}>
      <Box component="form" onSubmit={submit}>
        <DialogTitle>{mode === 'upload' ? '上传核心' : '从 GitHub 获取'}</DialogTitle>
        <DialogContent
          sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: '12px !important' }}
        >
          {error && <Alert severity="error">{error}</Alert>}
          <TextField
            label="版本"
            required
            value={version}
            disabled={busy}
            onChange={(e) => setVersion(e.target.value)}
            helperText="与构建产物一致，例如 v1.14.0"
          />
          <TextField
            select
            label="架构"
            value={arch}
            disabled={busy}
            onChange={(e) => setArch(e.target.value as CoreArch)}
          >
            <MenuItem value="amd64">amd64（x86-64）</MenuItem>
            <MenuItem value="arm64">arm64（AArch64）</MenuItem>
          </TextField>
          {mode === 'upload' ? (
            <Box>
              <Button component="label" variant="outlined" disabled={busy}>
                选择核心文件
                <input
                  type="file"
                  aria-label="核心文件"
                  hidden
                  onChange={(e) => setFile(e.target.files?.[0] ?? null)}
                />
              </Button>
              <Typography variant="body2" sx={{ mt: 1, overflowWrap: 'anywhere' }}>
                {file
                  ? `${file.name} · ${(file.size / 1024 / 1024).toFixed(1)} MiB`
                  : '选择 Linux 二进制文件，最大 64 MiB。'}
              </Typography>
            </Box>
          ) : (
            <TextField
              label="GitHub Release 文件链接"
              type="url"
              required
              value={url}
              disabled={busy}
              onChange={(e) => setUrl(e.target.value)}
              helperText="填写本项目构建工作流发布的文件链接，仓库需公开可读。"
            />
          )}
          <TextField
            label="SHA256 校验和"
            required
            value={sha256}
            disabled={busy}
            onChange={(e) => setSHA256(e.target.value)}
            helperText="复制同次构建的 SHA256SUMS 中对应架构的校验和。"
            slotProps={{ htmlInput: { maxLength: 64 } }}
          />
          {busy && (
            <Box>
              <LinearProgress
                variant={mode === 'upload' ? 'determinate' : 'indeterminate'}
                value={progress}
              />
              <Typography variant="caption">
                {mode === 'upload' && progress < 100
                  ? `上传中 ${progress}%`
                  : '正在下载或校验文件…'}
              </Typography>
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button color="inherit" onClick={close} disabled={busy}>
            取消
          </Button>
          <Button type="submit" variant="contained" loading={busy}>
            {mode === 'upload' ? '上传并校验' : '下载并校验'}
          </Button>
        </DialogActions>
      </Box>
    </Dialog>
  );
}
