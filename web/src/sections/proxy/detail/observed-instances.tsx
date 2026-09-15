import type { GridColDef } from '@mui/x-data-grid';
import type {
  ObservedInbound,
  ObservedInstance,
  ProxyObservations,
} from 'src/types/proxy-observation';

import { useState } from 'react';

import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import Button from '@mui/material/Button';
import { DataGrid } from '@mui/x-data-grid';
import Accordion from '@mui/material/Accordion';
import Typography from '@mui/material/Typography';
import AccordionSummary from '@mui/material/AccordionSummary';
import AccordionDetails from '@mui/material/AccordionDetails';

import { refreshProxyObservations } from 'src/api/proxy-observation';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

import { observedBytes, observationIssues } from '../observation-helpers';

const at = (value: number) =>
  value > 0 ? new Date(value * 1000).toLocaleString('zh-CN') : '尚未成功采集';
const sources: Record<string, string> = {
  'sing-box-yg': 'sing-box-yg',
  'x-ui-yg': 'x-ui-yg / Xray',
  generic: '通用识别',
  manual: '手动绑定',
};
const columns: GridColDef<ObservedInbound>[] = [
  {
    field: 'tag',
    headerName: '入站',
    minWidth: 155,
    flex: 1,
    valueGetter: (_, row) => row.tag || row.id,
  },
  { field: 'protocol', headerName: '协议', width: 112 },
  { field: 'port', headerName: '配置端口', width: 112 },
  {
    field: 'up',
    headerName: '上行用量',
    width: 150,
    valueGetter: (_, r) => observedBytes(r.usage?.up),
  },
  {
    field: 'down',
    headerName: '下行用量',
    width: 150,
    valueGetter: (_, r) => observedBytes(r.usage?.down),
  },
  {
    field: 'transport',
    headerName: '传输 / 加密',
    width: 170,
    valueGetter: (_, r) =>
      [r.transport, r.reality ? 'Reality' : r.tls ? 'TLS' : ''].filter(Boolean).join(' / ') ||
      '默认',
  },
  {
    field: 'enabled',
    headerName: '原管理器',
    width: 116,
    valueGetter: (_, r) => (r.enabled == null ? '未知' : r.enabled ? '启用' : '停用'),
  },
  {
    field: 'listening',
    headerName: '端口观测',
    width: 150,
    valueGetter: (_, r) =>
      !r.in_config
        ? '仅数据库记录'
        : r.listening == null
          ? '不可确认'
          : r.listening
            ? '该进程已绑定'
            : '未观测到绑定',
  },
  {
    field: 'users',
    headerName: '配置用户数',
    width: 110,
    valueGetter: (_, r) => r.users ?? '未知',
  },
  {
    field: 'limit',
    headerName: '原管理器额度',
    width: 155,
    valueGetter: (_, r) => (r.usage?.limit === 0 ? '未设置' : observedBytes(r.usage?.limit)),
  },
];

function InstanceCard({ item }: { item: ObservedInstance }) {
  return (
    <Card sx={{ p: 3 }}>
      <Stack
        direction="row"
        spacing={1}
        useFlexGap
        sx={{ alignItems: 'center', flexWrap: 'wrap', mb: 2 }}
      >
        <Typography variant="h6">
          {item.core === 'xray' ? 'Xray' : 'sing-box'} {item.version || '版本未知'}
        </Typography>
        <Label color="info">
          {item.ownership === 'external' ? '外部管理 · 只读' : '本项目管理'}
        </Label>
        <Label color={item.stale ? 'warning' : item.running ? 'success' : 'default'}>
          {item.absent
            ? '本轮未发现'
            : item.stale
              ? '历史快照'
              : item.running
                ? '运行中'
                : '未运行'}
        </Label>
        <Typography variant="body2" color="text.secondary">
          来源：{sources[item.source] || '未知来源'}
        </Typography>
      </Stack>
      <Box sx={{ display: 'grid', gap: 0.8, mb: 2, overflowWrap: 'anywhere' }}>
        <Typography variant="body2">
          服务：{item.service || '普通进程 / 未识别启动器'} · PID：{item.pid || '—'} · 内存：
          {observedBytes(item.rss_bytes)}
        </Typography>
        {item.manager_running != null && (
          <Typography variant="body2">
            x-ui 管理器：{item.manager_running ? '运行中' : '未运行'} · PID：
            {item.manager_pid || '—'}；Xray 状态单独观测。
          </Typography>
        )}
        <Typography variant="body2">核心路径：{item.binary}</Typography>
        <Typography variant="body2">配置路径：{item.config_paths.join('、') || '未知'}</Typography>
        <Typography variant="caption" color="text.secondary">
          配置读取：{at(item.config_read_at)} · 最近收到：{at(item.received_at)}
        </Typography>
      </Box>
      {item.stale && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          当前信息已过期或尚未完成核验，保留上次读取结果供参考。
        </Alert>
      )}
      <Alert severity={item.stats_status === 'unavailable' ? 'warning' : 'info'} sx={{ mb: 2 }}>
        {item.stats_status === 'manager_total'
          ? '统计来源：x-ui 原数据库，按入站展示原管理器保存的累计上行和下行；其重置周期由原管理器决定。'
          : item.stats_status === 'reference'
            ? '统计来源：核心已有接口，只读参考值。原管理器清零或核心重启时可能变化，不作为账期用量。'
            : item.stats_status === 'managed_separately'
              ? '该实例的统计由本项目托管功能单独处理。'
              : item.stats_status === 'unavailable'
                ? '统计来源暂时不可用；未将缺失值视为零。'
                : '未配置可读取的统计来源，因此该实例的代理用量不可用。'}{' '}
        这些用量不计入本项目的节点账单或订阅用户结算。
      </Alert>
      {!!item.issues.length && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          {[...new Set(item.issues)]
            .map((issue) => observationIssues[issue] || '部分来源信息暂时不可用')
            .join('；')}
        </Alert>
      )}
      <Typography variant="subtitle2" sx={{ mb: 1 }}>
        入站声明与原管理器记录（{item.inbounds.length}
        {item.truncated ? '，已截断' : ''}）
      </Typography>
      <Typography variant="caption" color="text.secondary">
        配置声明和进程绑定均不代表端到端连通性验证；未知协议仍按原名称展示。
      </Typography>
      <Box sx={{ height: Math.min(470, 125 + Math.max(1, item.inbounds.length) * 52), mt: 1 }}>
        <DataGrid
          rows={item.inbounds}
          columns={columns}
          disableRowSelectionOnClick
          pageSizeOptions={[10, 25, 50]}
          initialState={{ pagination: { paginationModel: { pageSize: 10 } } }}
        />
      </Box>
      <Accordion sx={{ mt: 2 }}>
        <AccordionSummary>
          <Typography variant="body2">该进程实际绑定的端口（{item.ports.length}）</Typography>
        </AccordionSummary>
        <AccordionDetails>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {item.ports.map((p) => `${p.address}:${p.port}/${p.network}`).join('，') ||
              '暂无可用记录'}
          </Typography>
        </AccordionDetails>
      </Accordion>
    </Card>
  );
}

export function ObservedInstances({
  serverId,
  data,
}: {
  serverId: number;
  data: ProxyObservations;
}) {
  const [busy, setBusy] = useState(false);
  const refresh = async () => {
    setBusy(true);
    try {
      await refreshProxyObservations(serverId);
      toast.success('已请求只读采集，结果会自动更新');
    } catch (error) {
      toast.error(getErrorMessage(error));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Stack spacing={3} sx={{ mb: 3 }}>
      <Stack direction="row" sx={{ justifyContent: 'space-between', alignItems: 'center' }}>
        <Typography variant="h5">代理实例观测</Typography>
        <Button
          variant="outlined"
          disabled={busy || !data.online || !data.supported}
          onClick={refresh}
        >
          刷新观测
        </Button>
      </Stack>
      {!data.supported && (
        <Alert severity="warning">
          探针离线或尚未完成观测能力协商。请先升级探针，核心管理操作暂不可用。
        </Alert>
      )}
      {data.management === 'external' && (
        <Alert severity="info">
          该节点的代理由第三方项目管理。此处可查看状态、入站和可用统计；配置、重启和清零请在原管理器中操作。
        </Alert>
      )}
      {data.instances.map((item) => (
        <InstanceCard key={item.id} item={item} />
      ))}
      {data.supported && !data.instances.length && (
        <Alert severity="info">尚未发现代理实例，或正在等待首个完整快照。</Alert>
      )}
      <Accordion>
        <AccordionSummary>
          <Typography variant="subtitle2">未识别自定义路径？配置本地只读绑定</Typography>
        </AccordionSummary>
        <AccordionDetails>
          <Typography variant="body2">
            在该节点的 /etc/vps-agent/config.yaml 中填写实际路径，然后重启
            vps-agent。此设置只影响探针读取来源。
          </Typography>
          <Box
            component="pre"
            sx={{ p: 2, bgcolor: 'background.neutral', overflow: 'auto', fontSize: 13 }}
          >
            {
              'proxy_observe:\n  bindings:\n    - core: xray\n      binary: /opt/xray/xray\n      service: custom-xray.service\n      config_paths:\n        - /opt/xray/config.json\n      # database: /etc/x-ui-yg/x-ui-yg.db\n'
            }
          </Box>
          <Typography variant="caption">
            支持 core: sing-box 或 xray；只填写本机绝对路径。页面不展示原始配置、密钥或数据库内容。
          </Typography>
        </AccordionDetails>
      </Accordion>
    </Stack>
  );
}
