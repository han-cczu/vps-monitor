import type { ServerSnapshot } from 'src/types/realtime';

import { useState, useCallback } from 'react';
import { useShallow } from 'zustand/react/shallow';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';

import { DashboardContent } from 'src/layouts/dashboard';
import { useRealtime, useRealtimeStatus } from 'src/store/realtime';

import { EmptyContent } from 'src/components/empty-content';
import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { SummaryBar } from '../summary-bar';
import { ServerCard } from '../server-card';
import { Toolbar, type Filters, DEFAULT_FILTERS } from '../toolbar';

// ----------------------------------------------------------------------

/** 监控总览：卡片网格 + 汇总条 + 筛选排序。数据来自每秒一帧的 WebSocket 快照。 */
export function OverviewView() {
  const [filters, setFilters] = useState<Filters>(DEFAULT_FILTERS);

  const status = useRealtimeStatus();
  const hasSnapshot = useRealtime((state) => state.ts > 0);

  // 只订阅「可见的 id 列表」，不订阅节点内容：卡片各自订阅自己那一台。
  // useShallow 保证内容没变时不触发本组件重渲染（按 CPU 排序时顺序每秒都在变，那时才会重渲染）。
  const visibleIds = useRealtime(useShallow((state) => selectVisibleIds(state.servers, filters)));
  // 分成两个选择器：useShallow 只比一层，包在一个对象里的话每帧都是新数组引用，等于没比。
  const groups = useRealtime(useShallow((state) => selectFacet(state.servers, 'group')));
  const tags = useRealtime(useShallow((state) => selectFacet(state.servers, 'tag')));

  const handleChange = useCallback((patch: Partial<Filters>) => {
    setFilters((prev) => ({ ...prev, ...patch }));
  }, []);

  const total = useRealtime((state) => state.order.length);

  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs heading="监控总览" sx={{ mb: { xs: 3, md: 4 } }} />

      <Box sx={{ gap: 3, display: 'flex', flexDirection: 'column' }}>
        {status !== 'open' && (
          <Alert severity={hasSnapshot ? 'warning' : 'info'}>
            {hasSnapshot ? '连接中断，正在重连——下面显示的是最后一帧数据' : '正在连接实时通道…'}
          </Alert>
        )}

        <SummaryBar />

        <Card sx={{ p: 2.5 }}>
          <Toolbar filters={filters} groups={groups} tags={tags} onChange={handleChange} />
        </Card>

        {/* 网格按最小宽度自动排列，而不是按断点写死列数：固定 4 列时 1600px 视口下
            每张卡片只有 276px，内存 / 磁盘 / 速率那几行会被省略号截断（审查时实测）。
            300px 是卡片不截断的下限。 */}
        {!hasSnapshot ? (
          <EmptyContent
            title="正在加载节点…"
            description="连上实时通道后会立刻显示第一帧数据。"
            sx={{ py: 10 }}
          />
        ) : visibleIds.length > 0 ? (
          <Box
            sx={{
              gap: 3,
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))',
            }}
          >
            {visibleIds.map((id) => (
              <ServerCard key={id} id={id} />
            ))}
          </Box>
        ) : (
          <EmptyContent
            title={total === 0 ? '还没有节点' : '没有符合条件的节点'}
            description={
              total === 0
                ? '到「节点」页新建一个，按一键安装命令在机器上装好 agent 就会出现在这里。'
                : '换个筛选条件试试，或者打开「显示离线」。'
            }
            sx={{ py: 10 }}
          />
        )}
      </Box>
    </DashboardContent>
  );
}

// ----------------------------------------------------------------------

/** 按筛选条件挑出要显示的节点，并按排序方式排好。 */
function selectVisibleIds(servers: Record<number, ServerSnapshot>, filters: Filters): number[] {
  const keyword = filters.keyword.trim().toLowerCase();

  const matched = Object.values(servers).filter((server) => {
    if (!filters.showOffline && !server.online) {
      return false;
    }
    if (filters.group && server.group !== filters.group) {
      return false;
    }
    if (filters.tag && !server.tags.includes(filters.tag)) {
      return false;
    }
    if (keyword) {
      const haystack = [server.name, server.region, server.group, ...server.tags]
        .join(' ')
        .toLowerCase();
      if (!haystack.includes(keyword)) {
        return false;
      }
    }
    return true;
  });

  matched.sort((a, b) => compare(a, b, filters.sort));

  return matched.map((server) => server.id);
}

function compare(a: ServerSnapshot, b: ServerSnapshot, key: Filters['sort']): number {
  switch (key) {
    case 'name':
      // 中文按拼音排，localeCompare 在 zh 下就是这个行为
      return a.name.localeCompare(b.name, 'zh-Hans-CN');
    case 'cpu':
      return b.cpu - a.cpu;
    case 'mem':
      return usage(b.mem) - usage(a.mem);
    case 'expire':
      return (a.expire_at ?? '9999-12-31').localeCompare(b.expire_at ?? '9999-12-31');
    case 'remaining':
      return remaining(a) - remaining(b) || a.id - b.id;
    case 'traffic':
      return b.net.out_total - a.net.out_total;
    default:
      return a.sort - b.sort || a.id - b.id;
  }
}

function usage(stat: { used: number; total: number }): number {
  return stat.total > 0 ? stat.used / stat.total : 0;
}

/** 分组或标签的可选项，从当前快照里现算。 */
function selectFacet(servers: Record<number, ServerSnapshot>, kind: 'group' | 'tag'): string[] {
  const values = new Set<string>();

  for (const server of Object.values(servers)) {
    if (kind === 'group') {
      if (server.group) {
        values.add(server.group);
      }
    } else {
      for (const tag of server.tags) {
        values.add(tag);
      }
    }
  }

  return [...values].sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'));
}

function remaining(server: ServerSnapshot): number {
  return server.traffic && server.traffic.limit > 0
    ? server.traffic.limit - server.traffic.used
    : Number.MAX_VALUE;
}
