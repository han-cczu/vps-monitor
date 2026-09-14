import type { PingPoint, PingSummary } from 'src/types/ping';

import Box from '@mui/material/Box';
import Tooltip from '@mui/material/Tooltip';
import { useTheme } from '@mui/material/styles';
import Typography from '@mui/material/Typography';

import { usePingRecent } from 'src/api/ping';

export function PingRows({
  serverID,
  tasks,
  online,
}: {
  serverID: number;
  tasks: PingSummary[];
  online: boolean;
}) {
  const { data, error } = usePingRecent(serverID, tasks.length > 0);
  if (tasks.length === 0) {
    return null;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      {tasks.map((task) => (
        <Box key={task.task_id}>
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5 }}>
            <Typography variant="caption" noWrap sx={{ flex: 1 }}>
              {task.name}
            </Typography>
            <Typography
              variant="caption"
              sx={{
                color:
                  task.last_ts !== null && task.latency === null ? 'error.main' : 'text.primary',
              }}
            >
              {task.last_ts == null
                ? '等待探测'
                : task.latency === null
                  ? '超时'
                  : `${task.latency.toFixed(1)} ms`}
            </Typography>
            <Typography variant="caption" sx={{ color: 'text.secondary' }}>
              {task.last_ts == null ? '—' : `${task.loss.toFixed(1)}% 丢包`}
            </Typography>
          </Box>
          <PingBlocks
            points={data?.tasks.find((item) => item.task_id === task.task_id)?.results ?? []}
          />
        </Box>
      ))}
      {error && (
        <Typography variant="caption" color="error">
          最近探测记录加载失败
        </Typography>
      )}
      {!online && (
        <Typography variant="caption" color="text.secondary">
          节点离线，显示最后的探测记录
        </Typography>
      )}
    </Box>
  );
}

function PingBlocks({ points }: { points: PingPoint[] }) {
  const theme = useTheme();
  const recent = points.slice(-30);
  const slots = Array.from({ length: 30 }, (_, index) => recent[index - (30 - recent.length)]);
  const color = (point: PingPoint | undefined) => {
    if (!point) {
      return theme.palette.action.disabledBackground;
    }
    if (point.latency === null) {
      return theme.palette.error.main;
    }
    if (point.latency < 100) {
      return theme.palette.success.main;
    }
    if (point.latency < 200) {
      return theme.palette.warning.main;
    }
    return theme.palette.warning.dark;
  };
  return (
    <Tooltip title="最近 30 次探测，最新在右；绿 <100ms，黄 <200ms，橙 ≥200ms，红为丢包，灰为无数据">
      <Box
        component="svg"
        viewBox="0 0 300 14"
        role="img"
        aria-label={`最近 ${recent.length} 次探测，${recent.filter((p) => p.latency === null).length} 次丢包`}
        sx={{ width: 1, height: 14, display: 'block' }}
      >
        {slots.map((point, index) => (
          <rect key={index} x={index * 10} width={8} height={14} rx={2} fill={color(point)}>
            <title>
              {point
                ? `${new Date(point.ts * 1000).toLocaleString()} · ${point.latency === null ? '超时' : `${point.latency.toFixed(1)} ms`}`
                : '无数据'}
            </title>
          </rect>
        ))}
      </Box>
    </Tooltip>
  );
}
