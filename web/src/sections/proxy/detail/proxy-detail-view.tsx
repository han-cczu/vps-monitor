import { useState } from 'react';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import LinearProgress from '@mui/material/LinearProgress';

import { paths } from 'src/routes/paths';

import { useServers } from 'src/api/servers';
import { DashboardContent } from 'src/layouts/dashboard';
import { useCore, useCert, useInbounds, useProxyAssignments } from 'src/api/proxy';

import { CustomBreadcrumbs } from 'src/components/custom-breadcrumbs';

import { getErrorMessage } from 'src/auth/utils';

import { CertCard } from './cert-card';
import { LogDrawer } from './log-drawer';
import { InboundList } from './inbound-list';
import { AdvancedHint } from './advanced-hint';
import { CoreStatusCard } from './core-status-card';
import { RevisionsDialog } from './revisions-dialog';

export function ProxyDetailView({ serverId }: { serverId: number }) {
  const { servers } = useServers();
  const core = useCore(serverId);
  const inbounds = useInbounds(serverId);
  const cert = useCert(serverId);
  const assignments = useProxyAssignments();
  const [dialog, setDialog] = useState<'logs' | 'revisions' | null>(null);
  const refresh = async () => {
    // 写操作已经成功；刷新失败由各资源的错误条显示，不能让表单误报保存失败并重复提交。
    await Promise.allSettled([
      core.mutate(),
      inbounds.mutate(),
      cert.mutate(),
      assignments.mutate(),
    ]);
  };
  return (
    <DashboardContent maxWidth="xl">
      <CustomBreadcrumbs
        heading={servers.find((s) => s.id === serverId)?.name ?? `节点 #${serverId}`}
        links={[{ name: '代理', href: paths.dashboard.proxy.root }, { name: '节点代理' }]}
        sx={{ mb: 3 }}
      />
      {core.isLoading && <LinearProgress sx={{ mb: 3 }} />}
      {[
        { resource: core, name: '核心状态' },
        { resource: inbounds, name: '入站' },
        { resource: cert, name: '证书' },
        { resource: assignments, name: '用户分配' },
      ].map(
        ({ resource, name }) =>
          resource.error && (
            <Alert
              key={String(name)}
              severity="error"
              sx={{ mb: 2 }}
              action={<Button onClick={() => resource.mutate()}>重试</Button>}
            >
              {String(name)}：{getErrorMessage(resource.error)}
            </Alert>
          )
      )}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: { xs: 'minmax(0, 1fr)', lg: 'minmax(320px, 2fr) minmax(0, 3fr)' },
          gap: 3,
          alignItems: 'start',
        }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
          {core.core && (
            <CoreStatusCard
              core={core.core}
              onRefresh={async () => {
                await Promise.allSettled([core.mutate()]);
              }}
              onLogs={() => setDialog('logs')}
              onRevisions={() => setDialog('revisions')}
            />
          )}
          {cert.data && <CertCard serverId={serverId} cert={cert.data.cert} onSaved={refresh} />}
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
          {inbounds.isLoading && <LinearProgress aria-label="加载入站" />}
          {inbounds.data && (
            <InboundList
              serverId={serverId}
              inbounds={inbounds.data.inbounds}
              core={core.core}
              subscribers={assignments.data?.subscribers}
              onSaved={refresh}
            />
          )}
          <AdvancedHint serverId={serverId} />
        </Box>
      </Box>
      {dialog === 'logs' && (
        <LogDrawer
          serverId={serverId}
          online={!!core.core?.online}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'revisions' && (
        <RevisionsDialog
          serverId={serverId}
          online={!!core.core?.online}
          onClose={() => setDialog(null)}
          onSaved={refresh}
        />
      )}
    </DashboardContent>
  );
}
