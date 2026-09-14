import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import LinearProgress from '@mui/material/LinearProgress';

export function bytesText(n: number) {
  if (n < 1024) return `${n} B`;
  const index = Math.min(4, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / 1024 ** index).toFixed(2)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][index]}`;
}
export function QuotaBar({ used, limit }: { used: number; limit: number }) {
  const percent = limit > 0 ? (used / limit) * 100 : 0;
  return (
    <Box sx={{ width: 1, py: 1 }}>
      <Typography variant="caption">
        {bytesText(used)} / {limit ? bytesText(limit) : '不限额'}
      </Typography>
      {limit > 0 && (
        <LinearProgress
          variant="determinate"
          value={Math.min(100, percent)}
          color={percent >= 85 ? 'error' : percent >= 60 ? 'warning' : 'success'}
          aria-label="额度使用比例"
        />
      )}
    </Box>
  );
}
