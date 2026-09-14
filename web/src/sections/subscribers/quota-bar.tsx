import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import LinearProgress from '@mui/material/LinearProgress';

import { formatBytes } from 'src/utils/format';

export const bytesText = formatBytes;
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
