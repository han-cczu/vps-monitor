import Alert from '@mui/material/Alert';

import { useParams } from 'src/routes/hooks';

import { CONFIG } from 'src/global-config';

import { ProxyDetailView } from 'src/sections/proxy/detail/proxy-detail-view';

export default function Page() {
  const { serverId } = useParams();
  const id = Number(serverId);
  if (!serverId || !/^[1-9]\d*$/.test(serverId) || !Number.isSafeInteger(id))
    return <Alert severity="error">节点 ID 不合法</Alert>;
  return (
    <>
      <title>{`节点代理 - ${CONFIG.appName}`}</title>
      <ProxyDetailView key={id} serverId={id} />
    </>
  );
}
