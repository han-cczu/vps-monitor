import type { Inbound } from 'src/types/proxy';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';

import { copyText } from '../shared';
import { PROTOCOLS } from '../helpers';

export function FirewallPorts({
  inbounds,
  firewall,
}: {
  inbounds: Inbound[];
  firewall?: string | null;
}) {
  const ports = [
    ...new Set(
      inbounds
        .filter((i) => i.enabled)
        .flatMap((i) =>
          PROTOCOLS[i.protocol].transports.map((transport) => `${i.listen_port}/${transport}`)
        )
    ),
  ]
    .sort((a, b) => Number.parseInt(a, 10) - Number.parseInt(b, 10) || a.localeCompare(b))
    .join(', ');
  return (
    <Alert
      severity={firewall === 'none' ? 'warning' : 'info'}
      action={
        <Button disabled={!ports} onClick={() => copyText(ports)}>
          复制
        </Button>
      }
    >
      安全组待核对端口：{ports || '暂无启用入站'}。
      {firewall === 'none'
        ? '节点未检测到受支持防火墙，请手动核对云安全组及主机防火墙。'
        : '请在云平台安全组放行；此处仅列需求，未探测实际放行状态。'}
    </Alert>
  );
}
