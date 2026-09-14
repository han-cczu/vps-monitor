import type { PingRange } from 'src/types/ping';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Typography from '@mui/material/Typography';
import LinearProgress from '@mui/material/LinearProgress';

import { usePingRecent, usePingHistory } from 'src/api/ping';

import { Chart, useChart } from 'src/components/chart';
import { EmptyContent } from 'src/components/empty-content';

export function PingCharts({ serverID, range }: { serverID: number; range: PingRange }) {
  const { data, error, isLoading } = usePingRecent(serverID);
  if (error) {
    return <Alert severity="error">延迟任务加载失败，请稍后重试。</Alert>;
  }
  if (isLoading) {
    return <LinearProgress aria-label="加载延迟任务" />;
  }
  if (!data?.tasks.length) {
    return (
      <Card sx={{ p: 3 }}>
        <EmptyContent
          title="没有启用的延迟任务"
          description="在设置 → Ping 任务中配置探测目标，并选择此节点。"
        />
      </Card>
    );
  }
  return (
    <Box sx={{ display: 'grid', gap: 3, gridTemplateColumns: { xs: '1fr', lg: '1fr 1fr' } }}>
      {data.tasks.map((task) => (
        <PingTaskChart
          key={task.task_id}
          serverID={serverID}
          taskID={task.task_id}
          name={task.name}
          range={range}
        />
      ))}
    </Box>
  );
}
function PingTaskChart({
  serverID,
  taskID,
  name,
  range,
}: {
  serverID: number;
  taskID: number;
  name: string;
  range: PingRange;
}) {
  const { data, error, isLoading } = usePingHistory(serverID, taskID, range);
  // 缺测桶补 null，避免把节点离线时段连成一条成功探测的曲线。
  const points: { ts: number; avg: number | null; max: number | null; loss: number | null }[] = [];
  if (data?.points.length) {
    const byTime = new Map(data.points.map((point) => [point.ts, point]));
    for (let ts = data.from; ts < data.to; ts += data.step) {
      points.push(byTime.get(ts) ?? { ts, avg: null, max: null, loss: null });
    }
  }
  const latencyMax = Math.max(1, ...points.map((point) => (point.max ?? 0) * 1.1));
  const options = useChart({
    chart: { animations: { enabled: false } },
    xaxis: { type: 'datetime', labels: { datetimeUTC: false } },
    stroke: { curve: 'straight', width: [2, 1, 0] },
    fill: { opacity: [1, 1, 0.25], type: 'solid' },
    markers: { size: 2, strokeWidth: 0 },
    legend: { show: true, position: 'top', horizontalAlign: 'right', clusterGroupedSeries: false },
    yaxis: [
      {
        seriesName: '平均延迟',
        min: 0,
        max: latencyMax,
        title: { text: 'ms' },
        labels: { formatter: (v: number) => v.toFixed(v < 10 ? 1 : 0) },
      },
      { seriesName: '峰值延迟', show: false, min: 0, max: latencyMax },
      {
        seriesName: '丢包率',
        opposite: true,
        min: 0,
        max: 100,
        title: { text: '丢包 %' },
        labels: { formatter: (v: number) => v.toFixed(v < 10 ? 1 : 0) },
      },
    ],
    tooltip: {
      x: { format: 'MM-dd HH:mm' },
      y: [
        { formatter: (v: number) => `${v?.toFixed(1) ?? '—'} ms` },
        { formatter: (v: number) => `${v?.toFixed(1) ?? '—'} ms` },
        { formatter: (v: number) => `${v?.toFixed(1) ?? '—'}%` },
      ],
    },
  });
  const series = [
    { name: '平均延迟', type: 'line', data: points.map((p) => ({ x: p.ts * 1000, y: p.avg })) },
    { name: '峰值延迟', type: 'line', data: points.map((p) => ({ x: p.ts * 1000, y: p.max })) },
    { name: '丢包率', type: 'column', data: points.map((p) => ({ x: p.ts * 1000, y: p.loss })) },
  ];
  return (
    <Card sx={{ p: 2.5, minWidth: 0 }}>
      <Typography variant="subtitle2" sx={{ mb: 1 }}>
        {name}
      </Typography>
      {isLoading ? (
        <LinearProgress aria-label={`加载${name}延迟`} />
      ) : error ? (
        <Alert severity="error">曲线加载失败，请稍后重试。</Alert>
      ) : !points.length ? (
        <EmptyContent
          title="还没有探测数据"
          description="任务下发后等待首次探测，记录每 5 秒保存一次。"
        />
      ) : (
        <Chart type="line" options={options} series={series} sx={{ height: 280 }} />
      )}
    </Card>
  );
}
