import type { ServerHostInfo } from 'src/types/server';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Typography from '@mui/material/Typography';

import { formatBytes } from 'src/utils/format';
import { fDateTime } from 'src/utils/format-time';

import { Label } from 'src/components/label';
import { EmptyContent } from 'src/components/empty-content';

// ----------------------------------------------------------------------

type Props = {
  host: ServerHostInfo | null;
};

/** 主机信息：agent 上报的静态信息，节点从没连过时显示占位。 */
export function HostInfoPanel({ host }: Props) {
  if (!host) {
    return (
      <Card sx={{ p: 2.5 }}>
        <EmptyContent
          title="还没有主机信息"
          description="agent 连上之后会上报系统、内核、CPU 与内存磁盘总量。"
          sx={{ py: 5 }}
        />
      </Card>
    );
  }

  const items: { label: string; value: React.ReactNode }[] = [
    { label: '主机名', value: host.hostname || '—' },
    { label: '系统', value: host.os || '—' },
    { label: '内核', value: host.kernel || '—' },
    { label: '架构', value: host.arch || '—' },
    { label: 'CPU', value: host.cpu_model || '—' },
    { label: '核数', value: host.cores ? `${host.cores} 核` : '—' },
    { label: '内存', value: host.mem_total ? formatBytes(host.mem_total) : '—' },
    { label: '磁盘', value: host.disk_total ? formatBytes(host.disk_total) : '—' },
    { label: '公网 IP', value: host.public_ip || '—' },
    { label: 'agent 版本', value: host.agent_version || '—' },
    // 只显示开机时刻，不算「已运行多久」：那要在渲染期读当前时间（不纯），
    // 而且卡片页脚的「运行」那一格已经在显示时长了。
    { label: '开机时间', value: host.boot_time ? fDateTime(host.boot_time * 1000) : '—' },
    {
      label: '协议栈',
      value: (
        <Box sx={{ gap: 0.5, display: 'flex' }}>
          {host.ipv4 && (
            <Label variant="soft" color="info">
              V4
            </Label>
          )}
          {host.ipv6 && (
            <Label variant="soft" color="success">
              V6
            </Label>
          )}
          {!host.ipv4 && !host.ipv6 && (
            <Label variant="soft" color="default">
              未探测
            </Label>
          )}
        </Box>
      ),
    },
  ];

  return (
    <Card sx={{ p: 2.5 }}>
      <Typography variant="subtitle2" sx={{ mb: 2 }}>
        主机信息
      </Typography>

      <Box
        sx={{
          gap: 2,
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)', lg: 'repeat(4, 1fr)' },
        }}
      >
        {items.map((item) => (
          <Box key={item.label} sx={{ minWidth: 0 }}>
            <Typography variant="caption" sx={{ display: 'block', color: 'text.secondary' }}>
              {item.label}
            </Typography>
            {typeof item.value === 'string' ? (
              <Typography noWrap variant="body2" title={item.value}>
                {item.value}
              </Typography>
            ) : (
              item.value
            )}
          </Box>
        ))}
      </Box>

      <Typography variant="caption" sx={{ mt: 2, display: 'block', color: 'text.secondary' }}>
        {`最后上报：${host.updated_at ? fDateTime(host.updated_at * 1000) : '—'}`}
      </Typography>
    </Card>
  );
}
