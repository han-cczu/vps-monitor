import { useRef, useEffect } from 'react';

import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';

import { formatRate } from 'src/utils/format';

import { Iconify } from 'src/components/iconify';

import { Sparkline } from './sparkline';

// ----------------------------------------------------------------------

/** 折线保留多少个采样点。每秒一帧，30 点 = 最近 30 秒。 */
const HISTORY_SIZE = 30;

type Props = {
  /** 上行速率，字节每秒 */
  up: number;
  /** 下行速率，字节每秒 */
  down: number;
};

/**
 * 上行 / 下行速率 + 各自的实时折线。
 *
 * 采样历史用 useRef 存在卡片本地，**不进全局 store**：每秒往 store 里写 30 个点，
 * 会让所有订阅者都跑一遍 selector，而这份数据除了本卡片没人要。
 *
 * 采样写在 effect 里而不是渲染期：渲染期改 ref 是副作用，StrictMode 会把渲染跑两遍，
 * 于是每帧被推进去两个一模一样的点——30 个点的窗口就只剩 15 秒了（审查时实测到的）。
 */
export function NetRow({ up, down }: Props) {
  const upHistory = useRef<number[]>([]);
  const downHistory = useRef<number[]>([]);

  useEffect(() => {
    push(upHistory.current, up);
    push(downHistory.current, down);
  }, [up, down]);

  return (
    <Box sx={{ gap: 2, display: 'grid', gridTemplateColumns: '1fr 1fr' }}>
      <NetCell
        icon="eva:arrow-upward-fill"
        label="上行"
        rate={up}
        history={upHistory.current}
        color="primary"
      />
      <NetCell
        icon="eva:arrow-downward-fill"
        label="下行"
        rate={down}
        history={downHistory.current}
        color="info"
      />
    </Box>
  );
}

// ----------------------------------------------------------------------

function NetCell({
  icon,
  label,
  rate,
  history,
  color,
}: {
  icon: 'eva:arrow-upward-fill' | 'eva:arrow-downward-fill';
  label: string;
  rate: number;
  history: number[];
  color: 'primary' | 'info';
}) {
  return (
    <Box sx={{ gap: 0.5, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
      <Box sx={{ gap: 0.5, display: 'flex', alignItems: 'center' }}>
        <Iconify icon={icon} width={14} sx={{ color: `${color}.main`, flexShrink: 0 }} />
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>
          {label}
        </Typography>
        <Typography
          noWrap
          variant="caption"
          sx={{ ml: 'auto', fontWeight: 'fontWeightSemiBold', fontVariantNumeric: 'tabular-nums' }}
        >
          {formatRate(rate)}
        </Typography>
      </Box>

      <Sparkline values={history} color={color} />
    </Box>
  );
}

/** 原地维护一个定长环形数组，避免每秒新建数组。 */
function push(history: number[], value: number) {
  history.push(Number.isFinite(value) ? value : 0);
  if (history.length > HISTORY_SIZE) {
    history.shift();
  }
}
