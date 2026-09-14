import type { ServerSnapshot } from 'src/types/realtime';

import { useShallow } from 'zustand/react/shallow';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';

import { daysUntil, formatRate, formatBytes } from 'src/utils/format';

import { useRealtime } from 'src/store/realtime';

// ----------------------------------------------------------------------

/** 「即将到期」的阈值（天）。 */
const EXPIRING_SOON_DAYS = 7;

/**
 * 顶部汇总条。
 *
 * 它每秒都会重渲染——这是有意的，上下行是实时值。用 useShallow 逐字段比较，
 * 至少在数值没变的那些秒（比如全部离线时）能省掉一次渲染。
 */
export function SummaryBar() {
  const summary = useRealtime(useShallow(selectSummary));

  return (
    <Card
      sx={{
        p: 2.5,
        gap: 2,
        display: 'grid',
        gridTemplateColumns: { xs: 'repeat(2, 1fr)', md: 'repeat(5, 1fr)' },
      }}
    >
      <SummaryItem
        label="在线"
        value={`${summary.online} / ${summary.total}`}
        tone={summary.total > 0 && summary.online === 0 ? 'error' : undefined}
      />
      <SummaryItem label="总上行" value={formatRate(summary.up)} />
      <SummaryItem label="总下行" value={formatRate(summary.down)} />
      <SummaryItem
        label="本月总流量"
        value={formatBytes(summary.traffic)}
        hint="全部节点当前账期已用流量之和"
      />
      <SummaryItem
        label={`${EXPIRING_SOON_DAYS} 天内到期`}
        value={String(summary.expiring)}
        hint="含已经过期的节点"
        tone={summary.expiring > 0 ? 'warning' : undefined}
      />
    </Card>
  );
}

// ----------------------------------------------------------------------

function SummaryItem({
  label,
  value,
  tone,
  hint,
}: {
  label: string;
  value: string;
  tone?: 'warning' | 'error';
  hint?: string;
}) {
  const content = (
    <Box sx={{ minWidth: 0 }}>
      <Typography noWrap variant="caption" sx={{ display: 'block', color: 'text.secondary' }}>
        {label}
      </Typography>
      <Typography
        noWrap
        variant="h6"
        sx={{ color: tone ? `${tone}.main` : undefined, fontVariantNumeric: 'tabular-nums' }}
      >
        {value}
      </Typography>
    </Box>
  );

  return hint ? (
    <Tooltip title={hint} placement="top-start">
      {content}
    </Tooltip>
  ) : (
    content
  );
}

// ----------------------------------------------------------------------

type Summary = {
  online: number;
  total: number;
  up: number;
  down: number;
  expiring: number;
  traffic: number;
};

function selectSummary(state: { servers: Record<number, ServerSnapshot> }): Summary {
  const servers = Object.values(state.servers);

  let online = 0;
  let up = 0;
  let down = 0;
  let expiring = 0;
  let traffic = 0;

  for (const server of servers) {
    traffic += server.traffic?.used ?? 0;
    if (server.online) {
      online += 1;
      // 离线节点的速率是最后一帧的旧值，累加进总量会让"全站上行"虚高
      up += server.net.up;
      down += server.net.down;
    }
    // 已过期（负数）也算进来：那比「还剩 3 天」更需要处理。卡片上会单独标「已过期 N 天」。
    const days = daysUntil(server.expire_at);
    if (days !== null && days <= EXPIRING_SOON_DAYS) {
      expiring += 1;
    }
  }

  return { online, total: servers.length, up, down, expiring, traffic };
}
