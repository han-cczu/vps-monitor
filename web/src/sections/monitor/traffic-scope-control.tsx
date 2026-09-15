import type { TrafficScope } from 'src/utils/traffic-display';

import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import ToggleButton from '@mui/material/ToggleButton';
import ToggleButtonGroup from '@mui/material/ToggleButtonGroup';

import { useTrafficDisplay } from 'src/store/traffic-display';

export function TrafficScopeControl() {
  const scope = useTrafficDisplay((state) => state.scope);
  const setScope = useTrafficDisplay((state) => state.setScope);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 1.5 }}>
        <Typography variant="subtitle2">出入站累计</Typography>
        <ToggleButtonGroup
          exclusive
          size="small"
          color="primary"
          value={scope}
          aria-label="出入站累计统计口径"
          onChange={(_, value: TrafficScope | null) => {
            if (value) setScope(value);
          }}
        >
          <ToggleButton value="period">本期流量</ToggleButton>
          <ToggleButton value="boot">开机累计</ToggleButton>
        </ToggleButtonGroup>
      </Box>
      <Typography variant="caption" sx={{ color: 'text.secondary' }}>
        {scope === 'boot'
          ? '读取系统网卡累计，包含接入探针前的流量；重启或网卡重置后可能归零。'
          : '从接入探针后的首次采样开始累计，按各节点账期重置。'}
        {' 套餐用量与剩余流量按账期统计。'}
      </Typography>
    </Box>
  );
}
