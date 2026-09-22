import type { GridColDef } from '@mui/x-data-grid';
import type {
  ObservedInbound,
  ObservedInstance,
  ProxyObservations,
  ProxyObservationScan,
} from 'src/types/proxy-observation';

import { useState } from 'react';
import { useSWRConfig } from 'swr';

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

import {
  proxyObservationsKey,
  deleteProxyObservation,
  resetProxyObservations,
  refreshProxyObservations,
} from 'src/api/proxy-observation';

import { Label } from 'src/components/label';
import { toast } from 'src/components/snackbar';

import { getErrorMessage } from 'src/auth/utils';

import { ActionDialog } from '../shared';
import {
  scanNotice,
  observedBytes,
  snapshotNotice,
  instanceStatus,
  splitObservations,
  observationIssues,
  instanceFromLatestScan,
} from '../observation-helpers';

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

function InboundsTable({ item }: { item: ObservedInstance }) {
  return (
    <>
      <Typography variant="subtitle2" sx={{ mb: 1 }}>
        入站声明与原管理器记录（{item.inbounds.length}
        {item.truncated ? '，已截断' : ''}）
      </Typography>
      <Typography variant="caption" color="text.secondary">
        配置声明和进程绑定均不代表端到端连通性验证；未知协议仍按原名称展示。
      </Typography>
      <Box sx={{ height: Math.min(620, 240 + Math.max(1, item.inbounds.length) * 52), mt: 1 }}>
        <DataGrid
          rows={item.inbounds}
          columns={columns}
          disableRowSelectionOnClick
          pageSizeOptions={[10, 25, 50]}
          initialState={{ pagination: { paginationModel: { pageSize: 10 } } }}
        />
      </Box>
    </>
  );
}

function StatsNote({ item }: { item: ObservedInstance }) {
  return (
    <>
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
    </>
  );
}

function PortsAccordion({ item }: { item: ObservedInstance }) {
  return (
    <Accordion sx={{ mt: 2 }}>
      <AccordionSummary>
        <Typography variant="body2">该进程实际绑定的端口（{item.ports.length}）</Typography>
      </AccordionSummary>
      <AccordionDetails>
        <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
          {item.ports.map((p) => `${p.address}:${p.port}/${p.network}`).join('，') || '暂无可用记录'}
        </Typography>
      </AccordionDetails>
    </Accordion>
  );
}

function InstanceCard({
  item,
  online,
  scan,
  onRemove,
}: {
  item: ObservedInstance;
  online: boolean;
  scan: ProxyObservationScan | null;
  onRemove?: () => void;
}) {
  // 「节点还在上报、但这一条已经过期」要在卡片里直接看出来：状态标签和标题旁的时间
  // 都要说明这一点，不能只靠下面那条提示。
  const fromLatest = instanceFromLatestScan(item, scan);
  const status = instanceStatus(item, { online, fromLatest });
  const notice = snapshotNotice(item, { online, scan });
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
        <Label color={status.color}>{status.label}</Label>
        <Typography variant="body2" color="text.secondary">
          来源：{sources[item.source] || '未知来源'}
        </Typography>
        {!fromLatest && online && (
          <Typography variant="caption" color="warning.main">
            这一条不在节点最近一次采集里（最近收到 {at(item.received_at)}）
          </Typography>
        )}
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
      {notice && (
        <Alert severity={notice.severity} sx={{ mb: 2 }}>
          {notice.text}
        </Alert>
      )}
      <StatsNote item={item} />
      <Stack direction="row" spacing={1} sx={{ mb: 2 }}>
        {onRemove && (
          <Button size="small" color="error" onClick={onRemove}>
            删除观测记录
          </Button>
        )}
      </Stack>
      <InboundsTable item={item} />
      <PortsAccordion item={item} />
    </Card>
  );
}

function HistoryCard({
  items,
  onRemove,
}: {
  items: ObservedInstance[];
  onRemove: (item: ObservedInstance) => void;
}) {
  return (
    <Accordion>
      <AccordionSummary>
        <Typography variant="subtitle1">历史观测记录（{items.length}）</Typography>
      </AccordionSummary>
      <AccordionDetails>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
          这些实例在最近一次完整扫描中没有出现。卡片里的 PID、内存、端口绑定和入站都来自最后一次快照，
          不是实时状态；用量按那次上报的结果展示，读不到时是空的。探针重新发现时它们会回到上面的当前实例中。
          在这里删除是物理删除这条记录，被删掉的记录不会再出现在历史里，直到探针重新发现它。
        </Typography>
        <Stack spacing={2}>
          {items.map((item) => (
            <Card key={item.id} variant="outlined" sx={{ p: 2 }}>
              <Stack
                direction="row"
                spacing={1}
                useFlexGap
                sx={{ alignItems: 'center', flexWrap: 'wrap', mb: 1.5 }}
              >
                <Typography variant="subtitle1">
                  {item.core === 'xray' ? 'Xray' : 'sing-box'} {item.version || '版本未知'}
                </Typography>
                <Label color="default">历史快照 · 非实时</Label>
                <Typography variant="body2" color="text.secondary">
                  {item.absent_at > 0
                    ? `确认消失：${at(item.absent_at)}`
                    : '确认消失：未记录（升级前的历史记录没有这个时间）'}
                </Typography>
              </Stack>
              <Box sx={{ display: 'grid', gap: 0.6, mb: 1.5, overflowWrap: 'anywhere' }}>
                <Typography variant="body2">
                  最后一次观测到的状态：{item.running ? '运行中' : '未运行'} · PID：
                  {item.pid || '—'} · 内存：{observedBytes(item.rss_bytes)}
                </Typography>
                <Typography variant="body2">
                  服务：{item.service || '普通进程 / 未识别启动器'} · 核心路径：{item.binary}
                </Typography>
                <Typography variant="body2">
                  配置路径：{item.config_paths.join('、') || '未知'} · 归属：
                  {item.ownership === 'external' ? '外部管理 · 只读' : '本项目管理'}
                </Typography>
                <Typography variant="caption" color="text.secondary">
                  最后观测时间：{at(item.received_at)} · 配置读取：{at(item.config_read_at)}
                </Typography>
              </Box>
              <Stack direction="row" spacing={1} sx={{ mb: 1.5 }}>
                <Button size="small" color="error" onClick={() => onRemove(item)}>
                  删除观测记录
                </Button>
              </Stack>
              <Accordion>
                <AccordionSummary>
                  <Typography variant="body2">
                    最后一次快照的入站（{item.inbounds.length} 个
                    {item.truncated ? '，已截断' : ''}）
                  </Typography>
                </AccordionSummary>
                <AccordionDetails>
                  <StatsNote item={item} />
                  <InboundsTable item={item} />
                </AccordionDetails>
              </Accordion>
              <Accordion>
                <AccordionSummary>
                  <Typography variant="body2">
                    那次快照观测到的绑定端口（{item.ports.length} 个）
                  </Typography>
                </AccordionSummary>
                <AccordionDetails>
                  <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
                    {item.ports
                      .map((p) => `${p.address}:${p.port}/${p.network}`)
                      .join('，') || '那次快照没有读到绑定端口'}
                  </Typography>
                  <Typography variant="caption" color="text.secondary">
                    地址、端口号和网络类型都来自最后那次读取，不代表现在仍然绑定。
                  </Typography>
                </AccordionDetails>
              </Accordion>
            </Card>
          ))}
        </Stack>
      </AccordionDetails>
    </Accordion>
  );
}

export function ObservedInstances({
  serverId,
  data,
}: {
  serverId: number;
  data: ProxyObservations;
}) {
  const { mutate } = useSWRConfig();
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState<ObservedInstance | null>(null);
  const [resetting, setResetting] = useState(false);
  const reload = () =>
    mutate((key) => typeof key === 'string' && key.startsWith(proxyObservationsKey(serverId)));
  const refresh = async () => {
    setBusy(true);
    try {
      await refreshProxyObservations(serverId);
      await reload();
      toast.success('已请求只读采集，结果会自动更新');
    } catch (error) {
      toast.error(getErrorMessage(error));
    } finally {
      setBusy(false);
    }
  };
  const remove = async (item: ObservedInstance) => {
    await deleteProxyObservation(serverId, item.id);
    await reload();
    toast.success('已删除该条观测记录，节点未受影响');
  };
  const reset = async () => {
    const result = await resetProxyObservations(serverId);
    await reload();
    if (result.requested) {
      toast.success('观测记录已清空，已请求重新采集；采集完成后会重新显示发现的实例');
    } else if (result.online) {
      toast.success('观测记录已清空；探针未确认观测能力，未请求采集');
    } else {
      toast.success('观测记录已清空；节点离线，等待探针上线后重新采集');
    }
  };
  const { current, history } = splitObservations(data.instances);
  const notice = scanNotice({
    scan: data.scan,
    online: data.online,
    supported: data.supported,
    hasCurrent: !!current.length,
    hasHistory: !!history.length,
  });
  const confirmDelete = async () => {
    if (!target) return;
    await remove(target);
  };
  return (
    <Stack spacing={3} sx={{ mb: 3 }}>
      <Stack
        direction={{ xs: 'column', sm: 'row' }}
        spacing={1}
        sx={{ justifyContent: 'space-between', alignItems: { sm: 'center' } }}
      >
        <Typography variant="h5">代理实例观测</Typography>
        <Stack direction="row" spacing={1}>
          <Button variant="outlined" disabled={busy || !data.online || !data.supported} onClick={refresh}>
            刷新观测
          </Button>
          <Button disabled={resetting || !data.instances.length} onClick={() => setResetting(true)}>
            重置本节点观测
          </Button>
        </Stack>
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
      {notice && <Alert severity={notice.severity}>{notice.text}</Alert>}
      {current.map((item) => (
        <InstanceCard
          key={item.id}
          item={item}
          online={data.online}
          scan={data.scan}
          onRemove={() => setTarget(item)}
        />
      ))}
      {!!history.length && <HistoryCard items={history} onRemove={setTarget} />}
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
      {target && (
        <ActionDialog
          danger
          title="删除观测记录"
          confirmLabel="删除记录"
          onClose={() => setTarget(null)}
          onConfirm={confirmDelete}
        >
          <Typography variant="body2">
            只删除面板保存的这条观测记录（{target.core === 'xray' ? 'Xray' : 'sing-box'}{' '}
            {target.version || '版本未知'}）。不会停止或卸载节点上的代理，也不会清零真实流量计数；
            探针以后重新发现它时会再次显示。
          </Typography>
        </ActionDialog>
      )}
      {resetting && (
        <ActionDialog
          title="重置本节点观测"
          confirmLabel="清空观测记录"
          onClose={() => setResetting(false)}
          onConfirm={reset}
        >
          <Typography variant="body2">
            清空本节点在面板保存的全部观测快照（{data.instances.length} 条）。
            {data.online
              ? '节点在线，清空后会请探针重新采集一次，采集完成并上报后发现的实例才会重新显示；在此之前列表是空的。'
              : '节点离线，清空后列表是空的，探针下次上线时会重新采集。'}{' '}
            不会删除代理、二进制、配置、systemd 服务或托管记录，也不清零真实流量计数。
          </Typography>
        </ActionDialog>
      )}
    </Stack>
  );
}
