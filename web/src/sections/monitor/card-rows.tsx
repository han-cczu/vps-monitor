// 卡片下半部分的几行：累计流量、剩余流量、页脚。
//
// 这几块都很短，合在一个文件里比拆成三个文件更好读；真正复杂的（资源块、速率折线）
// 才单独成文件。三网延迟要到步骤 09 才有数据，那时再加一块。

import type { ServerSnapshot } from 'src/types/realtime';
import type { TrafficScope } from 'src/utils/traffic-display';

import Box from '@mui/material/Box';
import Divider from '@mui/material/Divider';
import Typography from '@mui/material/Typography';

import { daysUntil, formatPrice, formatDuration, formatTrafficBytes } from 'src/utils/format';

import { Label } from 'src/components/label';

import { SegmentBar } from './segment-bar';

// ----------------------------------------------------------------------

/** 出站 / 入站累计。 */
export function TotalsRow({
  outTotal,
  inTotal,
  scope,
}: {
  outTotal: number | null;
  inTotal: number | null;
  scope: TrafficScope;
}) {
  const label = scope === 'boot' ? '开机累计' : '本期';
  return (
    <Box sx={{ gap: 2, display: 'grid', gridTemplateColumns: '1fr 1fr' }}>
      <MetaPair
        label={`出站 · ${label}`}
        value={outTotal === null ? '—' : formatTrafficBytes(outTotal)}
      />
      <MetaPair
        label={`入站 · ${label}`}
        value={inTotal === null ? '—' : formatTrafficBytes(inTotal)}
        align="right"
      />
    </Box>
  );
}

// ----------------------------------------------------------------------

/** 当前账期剩余流量。无限额仍显示实际用量，空条表示未设上限。 */
export function TrafficRow({ traffic }: { traffic: ServerSnapshot['traffic'] }) {
  const limited = !!traffic && traffic.limit > 0;
  const percent = limited ? (traffic.used / traffic.limit) * 100 : 0;
  const remaining = traffic
    ? limited
      ? formatTrafficBytes(Math.max(0, traffic.limit - traffic.used))
      : '∞'
    : '—';
  return (
    <Box sx={{ gap: 0.75, display: 'flex', flexDirection: 'column' }}>
      <Box
        sx={{ gap: 1, display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}
      >
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>
          本期剩余 {remaining}
        </Typography>
        <Typography
          variant="caption"
          sx={{ color: 'text.secondary', fontVariantNumeric: 'tabular-nums' }}
        >
          {traffic
            ? `${formatTrafficBytes(traffic.used)} / ${limited ? formatTrafficBytes(traffic.limit) : '∞'}`
            : '—'}
        </Typography>
      </Box>
      <SegmentBar
        value={percent}
        height={4}
        segments={20}
        color={percent > 95 ? 'error' : percent > 80 ? 'warning' : 'primary'}
      />
    </Box>
  );
}

// ----------------------------------------------------------------------

/**
 * 页脚：机器运行时长、到期天数、带宽与价格。
 *
 * 「运行」说的是机器开机多久（agent 报的 uptime），和顶部的连接状态是两回事——
 * 一台离线的机器完全可能还在跑着，所以这里不叫「在线」，免得和「离线 N 分钟」打架。
 *
 * 分组与标签不放在卡片上：它们是筛选维度（工具条里就能筛），而且长标签会把这一排撑破
 * （Label 不限宽也不换行）。分组已经跟着地区显示在标题下面了。
 */
export function CardFooter({ server }: { server: ServerSnapshot }) {
  const remainingDays = daysUntil(server.expire_at);
  const price = formatPrice(server.price, server.currency, server.cycle);

  return (
    <Box sx={{ gap: 1.5, display: 'flex', flexDirection: 'column' }}>
      <Divider sx={{ borderStyle: 'dashed' }} />

      <Box sx={{ gap: 2, display: 'grid', gridTemplateColumns: '1fr 1fr' }}>
        <MetaPair label="运行" value={server.uptime ? formatDuration(server.uptime) : '—'} />
        <ExpirePair days={remainingDays} />
      </Box>

      <Box sx={{ gap: 0.5, display: 'flex', flexWrap: 'wrap', alignItems: 'center' }}>
        {server.bandwidth && (
          <Label variant="soft" color="default">
            {server.bandwidth}
          </Label>
        )}
        {price !== '—' && (
          <Label variant="soft" color="default">
            {price}
          </Label>
        )}
      </Box>
    </Box>
  );
}

// ----------------------------------------------------------------------

function MetaPair({
  label,
  value,
  align = 'left',
}: {
  label: string;
  value: string;
  align?: 'left' | 'right';
}) {
  return (
    <Box sx={{ minWidth: 0, textAlign: align }}>
      <Typography variant="caption" sx={{ display: 'block', color: 'text.secondary' }}>
        {label}
      </Typography>
      <Typography
        noWrap
        variant="caption"
        sx={{ fontWeight: 'fontWeightSemiBold', fontVariantNumeric: 'tabular-nums' }}
      >
        {value}
      </Typography>
    </Box>
  );
}

/** 到期天数：过期标红，7 天内标黄，没设到期显示 `—`。 */
function ExpirePair({ days }: { days: number | null }) {
  if (days === null) {
    return <MetaPair label="到期" value="—" align="right" />;
  }

  const color = days < 7 ? 'error.main' : days < 30 ? 'warning.main' : undefined;
  const text = days < 0 ? `已过期 ${Math.abs(days)} 天` : `${days} 天`;

  return (
    <Box sx={{ minWidth: 0, textAlign: 'right' }}>
      <Typography variant="caption" sx={{ display: 'block', color: 'text.secondary' }}>
        到期
      </Typography>
      <Typography
        noWrap
        variant="caption"
        sx={{ color, fontWeight: 'fontWeightSemiBold', fontVariantNumeric: 'tabular-nums' }}
      >
        {text}
      </Typography>
    </Box>
  );
}
