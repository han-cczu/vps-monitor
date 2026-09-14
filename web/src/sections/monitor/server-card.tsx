import { memo } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';

import { formatBytes } from 'src/utils/format';

import { useServer } from 'src/store/realtime';

import { NetRow } from './net-row';
import { PingRows } from './ping-rows';
import { StatBlock } from './stat-block';
import { CardHeader } from './card-header';
import { TotalsRow, CardFooter, TrafficRow } from './card-rows';

// ----------------------------------------------------------------------

type Props = {
  id: number;
};

/**
 * 一张节点卡片。
 *
 * 只接一个 id，数据自己从 store 订阅——这样上层网格重渲染时不会把新 props 灌下来，
 * 配合 memo 就能做到「每秒只有数值真的变了的卡片重渲染」（验收第 6 条）。
 */
export const ServerCard = memo(function ServerCard({ id }: Props) {
  const server = useServer(id);

  if (!server) {
    return null;
  }

  const memPercent = ratio(server.mem.used, server.mem.total);
  const diskPercent = ratio(server.disk.used, server.disk.total);
  // 负载没有天然的百分比，按「每核 1.0 算满」折算，多核机器才不会一直顶格
  const loadPercent = server.cores ? (server.load[0] / server.cores) * 100 : 0;

  return (
    <Card
      sx={{
        p: 2.5,
        gap: 2,
        display: 'flex',
        flexDirection: 'column',
        // 离线整卡压暗。0.8 是压暗与可读之间的折中：整卡 opacity 会把里面所有文字的
        // 对比度一起按比例拉低，0.55 时副文本只剩 1.7:1 左右，等于看不见。
        // 真正表达「离线」的是标题右上角那个红色标签，压暗只是辅助。
        opacity: server.online ? 1 : 0.8,
        transition: (theme) => theme.transitions.create('opacity'),
      }}
    >
      <CardHeader
        id={server.id}
        name={server.name}
        region={server.region}
        group={server.group}
        v4={server.v4}
        v6={server.v6}
        online={server.online}
        lastSeen={server.last_seen}
      />

      <Box sx={{ gap: 2, display: 'grid', gridTemplateColumns: '1fr 1fr' }}>
        <StatBlock
          label="CPU"
          percent={server.cpu}
          caption={server.cores ? `${server.cores} 核` : '核数未知'}
        />
        <StatBlock
          label="内存"
          percent={memPercent}
          caption={`${formatBytes(server.mem.used)} / ${formatBytes(server.mem.total)}`}
        />
        <StatBlock
          label="磁盘"
          percent={diskPercent}
          caption={`${formatBytes(server.disk.used)} / ${formatBytes(server.disk.total)}`}
        />
        <StatBlock
          label="负载"
          percent={loadPercent}
          showPercent={false}
          value={server.load[0].toFixed(2)}
          caption={server.load.map((item) => item.toFixed(2)).join(' / ')}
        />
      </Box>

      {/* 离线后这两个速率是最后一帧的旧值，继续当实时值显示会误导；置零让折线自然走平 */}
      <NetRow up={server.online ? server.net.up : 0} down={server.online ? server.net.down : 0} />

      <TotalsRow outTotal={server.net.out_total} inTotal={server.net.in_total} />

      <TrafficRow traffic={server.traffic} />

      <PingRows serverID={server.id} tasks={server.ping} online={server.online} />

      <CardFooter server={server} />
    </Card>
  );
});

// ----------------------------------------------------------------------

function ratio(used: number, total: number): number {
  if (!total || total <= 0) {
    return 0;
  }
  return (used / total) * 100;
}
