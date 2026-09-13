import type { HistoryPoint, HistoryResponse } from 'src/types/history';

import { useMemo } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Typography from '@mui/material/Typography';

import { formatRate, formatBytes, formatPercent } from 'src/utils/format';

import { Chart, useChart } from 'src/components/chart';
import { EmptyContent } from 'src/components/empty-content';

// ----------------------------------------------------------------------

const CHART_HEIGHT = 260;

type Props = {
  history: HistoryResponse | undefined;
  loading: boolean;
};

/** 五张曲线：CPU、内存/Swap、磁盘、网速、连接数与进程数。 */
export function HistoryCharts({ history, loading }: Props) {
  const points = history?.points ?? [];

  if (!loading && points.length === 0) {
    return (
      <Card sx={{ p: 2.5 }}>
        <EmptyContent
          title="还没有历史数据"
          description="节点刚接入，指标每分钟汇总一次，攒几分钟后这里就有曲线了。"
          sx={{ py: 8 }}
        />
      </Card>
    );
  }

  return (
    <Box sx={{ gap: 3, display: 'grid', gridTemplateColumns: { xs: '1fr', lg: '1fr 1fr' } }}>
      <CPUChart points={points} />
      <MemoryChart points={points} total={history?.mem_total ?? 0} />
      <DiskChart points={points} total={history?.disk_total ?? 0} />
      <NetworkChart points={points} />
      <ConnectionsChart points={points} sx={{ gridColumn: { lg: '1 / -1' } }} />
    </Box>
  );
}

// ----------------------------------------------------------------------

function CPUChart({ points }: { points: HistoryPoint[] }) {
  const series = useMemo(
    () => [
      { name: '平均', data: points.map((p) => [p.ts * 1000, p.cpu] as [number, number]) },
      { name: '峰值', data: points.map((p) => [p.ts * 1000, p.cpu_max] as [number, number]) },
    ],
    [points]
  );

  const options = useChart({
    ...datetimeAxis,
    yaxis: { min: 0, max: 100, labels: { formatter: (v: number) => `${Math.round(v)}%` } },
    tooltip: { ...datetimeAxis.tooltip, y: { formatter: (v: number) => formatPercent(v) } },
    // 平均画成渐变面积，峰值只画一条细线叠在上面——一眼能看出「平均不高但有尖刺」。
    // 两条都填充的话，后画的峰值会把平均整个盖住，图就白画了。
    stroke: { curve: 'smooth', width: [2, 1] },
    fill: { type: ['gradient', 'solid'], opacity: [1, 0], gradient: { opacityFrom: 0.4, opacityTo: 0 } },
  });

  return (
    <ChartCard title="CPU">
      <Chart type="area" series={series} options={options} sx={{ height: CHART_HEIGHT }} />
    </ChartCard>
  );
}

function MemoryChart({ points, total }: { points: HistoryPoint[]; total: number }) {
  const series = useMemo(
    () => [
      { name: '内存', data: points.map((p) => [p.ts * 1000, p.mem] as [number, number]) },
      { name: 'Swap', data: points.map((p) => [p.ts * 1000, p.swap] as [number, number]) },
    ],
    [points]
  );

  const options = useChart({
    ...datetimeAxis,
    yaxis: { min: 0, max: total || undefined, labels: { formatter: byteLabel } },
    tooltip: { ...datetimeAxis.tooltip, y: { formatter: (v: number) => formatBytes(v) } },
    stroke: { curve: 'smooth', width: 2 },
    fill: { type: 'gradient', gradient: { opacityFrom: 0.4, opacityTo: 0 } },
  });

  return (
    <ChartCard title="内存 / Swap" subtitle={total ? `总量 ${formatBytes(total)}` : undefined}>
      <Chart type="area" series={series} options={options} sx={{ height: CHART_HEIGHT }} />
    </ChartCard>
  );
}

function DiskChart({ points, total }: { points: HistoryPoint[]; total: number }) {
  const series = useMemo(
    () => [{ name: '已用', data: points.map((p) => [p.ts * 1000, p.disk] as [number, number]) }],
    [points]
  );

  const options = useChart({
    ...datetimeAxis,
    yaxis: { min: 0, max: total || undefined, labels: { formatter: byteLabel } },
    tooltip: { ...datetimeAxis.tooltip, y: { formatter: (v: number) => formatBytes(v) } },
    stroke: { curve: 'smooth', width: 2 },
    fill: { type: 'gradient', gradient: { opacityFrom: 0.4, opacityTo: 0 } },
  });

  return (
    <ChartCard title="磁盘" subtitle={total ? `总量 ${formatBytes(total)}` : undefined}>
      <Chart type="area" series={series} options={options} sx={{ height: CHART_HEIGHT }} />
    </ChartCard>
  );
}

function NetworkChart({ points }: { points: HistoryPoint[] }) {
  const series = useMemo(
    () => [
      { name: '上行', data: points.map((p) => [p.ts * 1000, p.tx] as [number, number]) },
      { name: '下行', data: points.map((p) => [p.ts * 1000, p.rx] as [number, number]) },
    ],
    [points]
  );

  const options = useChart({
    ...datetimeAxis,
    yaxis: { min: 0, labels: { formatter: (v: number) => formatRate(v) } },
    tooltip: { ...datetimeAxis.tooltip, y: { formatter: (v: number) => formatRate(v) } },
    stroke: { curve: 'smooth', width: 2 },
  });

  return (
    <ChartCard title="网速" subtitle="区间平均">
      <Chart type="line" series={series} options={options} sx={{ height: CHART_HEIGHT }} />
    </ChartCard>
  );
}

function ConnectionsChart({ points, sx }: { points: HistoryPoint[]; sx?: object }) {
  const series = useMemo(
    () => [
      { name: 'TCP', data: points.map((p) => [p.ts * 1000, p.tcp] as [number, number]) },
      { name: 'UDP', data: points.map((p) => [p.ts * 1000, p.udp] as [number, number]) },
      { name: '进程', data: points.map((p) => [p.ts * 1000, p.procs] as [number, number]) },
    ],
    [points]
  );

  const options = useChart({
    ...datetimeAxis,
    yaxis: { min: 0, labels: { formatter: (v: number) => String(Math.round(v)) } },
    tooltip: { ...datetimeAxis.tooltip, y: { formatter: (v: number) => String(Math.round(v)) } },
    stroke: { curve: 'smooth', width: 2 },
  });

  return (
    <ChartCard title="连接数 / 进程数" sx={sx}>
      <Chart type="line" series={series} options={options} sx={{ height: CHART_HEIGHT }} />
    </ChartCard>
  );
}

// ----------------------------------------------------------------------

function ChartCard({
  title,
  subtitle,
  sx,
  children,
}: {
  title: string;
  subtitle?: string;
  sx?: object;
  children: React.ReactNode;
}) {
  return (
    <Card sx={{ p: 2.5, ...sx }}>
      <Box sx={{ mb: 1, gap: 1, display: 'flex', alignItems: 'baseline' }}>
        <Typography variant="subtitle2">{title}</Typography>
        {subtitle && (
          <Typography variant="caption" sx={{ color: 'text.secondary' }}>
            {subtitle}
          </Typography>
        )}
      </Box>
      {children}
    </Card>
  );
}

/**
 * 时间轴与 tooltip 的公共部分。
 *
 * x 轴用 datetime：缺失的分钟（节点掉线）不补零，ApexCharts 会按真实时间留空，
 * 不会把两侧的点连成一条假的直线。
 */
const datetimeAxis = {
  chart: { animations: { enabled: false } },
  xaxis: { type: 'datetime' as const },
  tooltip: { x: { format: 'MM-dd HH:mm' } },
  markers: { size: 0 },
  legend: { show: true, position: 'top' as const, horizontalAlign: 'right' as const },
};

/** y 轴的字节标签。轴上空间小，用短单位。 */
function byteLabel(v: number): string {
  return formatBytes(v);
}
