import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';

import { formatPercent } from 'src/utils/format';

import { SegmentBar } from './segment-bar';

// ----------------------------------------------------------------------

type Props = {
  /** 「CPU」「内存」「磁盘」 */
  label: string;
  /** 0–100 */
  percent: number;
  /** 大数字下面那行小字，如 `437 MB / 1.58 GB`、`2 核` */
  caption: string;
  /** 负载这类没有百分比语义的，传 false 就只显示数值不显示 % */
  showPercent?: boolean;
  /** showPercent 为 false 时显示的原始值 */
  value?: string;
};

/** 卡片里的一个资源块：标题 + 大数字 + 副文本 + 分段条。 */
export function StatBlock({ label, percent, caption, showPercent = true, value }: Props) {
  return (
    <Box sx={{ gap: 0.75, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
      <Box sx={{ gap: 1, display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}>
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>
          {label}
        </Typography>
        <Typography variant="subtitle2" sx={{ fontVariantNumeric: 'tabular-nums' }}>
          {showPercent ? formatPercent(percent) : value}
        </Typography>
      </Box>

      <SegmentBar value={percent} />

      {/* 用 text.secondary 而不是 text.disabled：后者在深浅两套主题下都达不到 WCAG AA
          （实测 1.67:1 / 1.84:1），层级靠字号和字重拉开就够了 */}
      <Typography
        noWrap
        variant="caption"
        sx={{ color: 'text.secondary', fontVariantNumeric: 'tabular-nums' }}
      >
        {caption}
      </Typography>
    </Box>
  );
}
