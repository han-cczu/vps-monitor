import Box from '@mui/material/Box';

// ----------------------------------------------------------------------

/** 分成几段。截图里是 10 段小方块，不是连续条。 */
const SEGMENTS = 10;

/** 配色阈值：低负载用主色，60% 起转黄，85% 起转红。 */
function toneOf(percent: number): 'primary' | 'warning' | 'error' {
  if (percent >= 85) {
    return 'error';
  }
  if (percent >= 60) {
    return 'warning';
  }
  return 'primary';
}

type Props = {
  /** 0–100，越界会被夹回来 */
  value: number;
  /** 离线卡片整体压暗，这里不再单独处理 */
  height?: number;
};

/**
 * 分段进度条。
 *
 * 用方块而不是连续条是对齐设计稿；另一个好处是每秒刷新时视觉上跳动更小——
 * 连续条 1% 的变化肉眼可见地抖，方块要跨过 10% 才会动一格。
 */
export function SegmentBar({ value, height = 6 }: Props) {
  const percent = Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0));
  // 有值就至少点亮一格：4% 和 0% 在界面上是两件事，都显示成空条会让人以为没数据
  const filled = percent > 0 ? Math.max(1, Math.round((percent / 100) * SEGMENTS)) : 0;
  const tone = toneOf(percent);

  return (
    <Box sx={{ gap: 0.5, width: 1, display: 'flex' }}>
      {Array.from({ length: SEGMENTS }, (_, index) => (
        <Box
          key={index}
          sx={(theme) => ({
            height,
            flex: '1 1 0',
            borderRadius: 0.5,
            bgcolor:
              index < filled
                ? theme.vars.palette[tone].main
                : `rgba(${theme.vars.palette.grey['500Channel']} / 0.16)`,
          })}
        />
      ))}
    </Box>
  );
}
