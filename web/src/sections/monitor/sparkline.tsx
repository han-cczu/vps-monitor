import Box from '@mui/material/Box';

// ----------------------------------------------------------------------

const VIEW_WIDTH = 100;
const VIEW_HEIGHT = 24;

type Props = {
  /** 采样点，按时间先后；不足两个点不画线 */
  values: number[];
  /** 语义色名，取 theme.vars.palette[color].main */
  color?: 'primary' | 'info' | 'warning' | 'error' | 'success';
};

/**
 * 速率小折线。
 *
 * 纯 SVG，不引图表库：这里只有一条 30 点的折线，ApexCharts 那一套（每个卡片一个实例、
 * 每秒 setState）在十几张卡片上开销和闪烁都不划算。
 *
 * 纵轴按本组件内的最大值归一化，所以它表达的是「这条线自己的起伏」，不是绝对量级——
 * 绝对值就写在旁边的文字里。
 */
export function Sparkline({ values, color = 'primary' }: Props) {
  const points = toPoints(values);

  return (
    <Box
      component="svg"
      aria-hidden="true"
      viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
      preserveAspectRatio="none"
      sx={{ width: 1, height: VIEW_HEIGHT, display: 'block', overflow: 'visible' }}
    >
      {points.length >= 2 && (
        <>
          <Box
            component="polyline"
            points={points.map(([x, y]) => `${x},${y}`).join(' ')}
            sx={(theme) => ({
              fill: 'none',
              strokeWidth: 1.5,
              strokeLinecap: 'round',
              strokeLinejoin: 'round',
              stroke: theme.vars.palette[color].main,
              vectorEffect: 'non-scaling-stroke',
            })}
          />
          <Box
            component="circle"
            cx={points[points.length - 1][0]}
            cy={points[points.length - 1][1]}
            r={2}
            sx={(theme) => ({ fill: theme.vars.palette[color].main })}
          />
        </>
      )}
    </Box>
  );
}

// ----------------------------------------------------------------------

/** 把采样值映射到 viewBox 坐标；全 0 时压成一条贴底的直线。 */
function toPoints(values: number[]): [number, number][] {
  if (values.length < 2) {
    return [];
  }

  const max = Math.max(...values, 1);
  const step = VIEW_WIDTH / (values.length - 1);
  // 上下各留 2px，末点的圆点才不会被裁掉
  const top = 2;
  const usable = VIEW_HEIGHT - top * 2;

  return values.map((value, index) => [
    index * step,
    top + usable - (Math.max(0, value) / max) * usable,
  ]);
}
