import type {
  Inbound,
  CoreState,
  ProxyProtocol,
  InboundPayload,
  ProxyAssignment,
} from '../../types/proxy';

import * as z from 'zod';

export const PROTOCOLS = {
  vless: {
    label: 'VLESS',
    color: 'info',
    port: 443,
    transports: ['tcp'],
    description: 'Reality · TCP，使用伪装握手站点',
  },
  shadowsocks: {
    label: 'SS',
    color: 'secondary',
    port: 8388,
    transports: ['tcp', 'udp'],
    description: 'Shadowsocks 2022 · TCP + UDP',
  },
  hysteria2: {
    label: 'HY2',
    color: 'warning',
    port: 8443,
    transports: ['udp'],
    description: 'Hysteria2 · QUIC，支持带宽与混淆设置',
  },
  tuic: {
    label: 'TUIC',
    color: 'success',
    port: 8444,
    transports: ['udp'],
    description: 'TUIC · QUIC，支持拥塞控制',
  },
} as const;

export function suggestPort(
  protocol: ProxyProtocol,
  inbounds: Pick<Inbound, 'protocol' | 'listen_port'>[]
) {
  const transports: readonly string[] = PROTOCOLS[protocol].transports;
  const occupied = new Set(
    inbounds
      .filter((x) => PROTOCOLS[x.protocol].transports.some((t) => transports.includes(t)))
      .map((x) => x.listen_port)
  );
  if (transports.includes('tcp')) occupied.add(10085); // 核心统计接口保留。
  for (let port: number = PROTOCOLS[protocol].port; port <= 65535; port += 1) {
    if (!occupied.has(port)) return port;
  }
  for (let port = 1; port < PROTOCOLS[protocol].port; port += 1) {
    if (!occupied.has(port)) return port;
  }
  return undefined;
}

export function coreStatus(core: Pick<CoreState, 'installed_version' | 'pending' | 'running'>) {
  if (!core.installed_version) return { label: '未安装', color: 'default' } as const;
  if (core.pending) return { label: '待应用', color: 'warning' } as const;
  return core.running
    ? ({ label: '运行中', color: 'success' } as const)
    : ({ label: '已停止', color: 'error' } as const);
}

export function countAssignments(
  subscribers: ProxyAssignment[],
  serverId: number,
  inboundId?: number
) {
  return subscribers.filter((s) =>
    s.assigned_inbounds.some(
      (a) => a.server_id === serverId && (inboundId === undefined || a.inbound_id === inboundId)
    )
  ).length;
}

const port = z.coerce
  .number()
  .int('端口必须是整数')
  .min(1, '端口范围为 1–65535')
  .max(65535, '端口范围为 1–65535');
const base = {
  listen_port: port,
  remark: z.string().trim().max(64, '备注最多 64 个字符'),
  enabled: z.boolean(),
};
const bandwidth = z.coerce.number().int('带宽必须是整数').min(0).max(1_000_000);
const shortIDs = z
  .string()
  .transform((s) => (s.trim() ? s.trim().split(/[\s,]+/) : []))
  .refine(
    (ids) =>
      ids.length <= 16 &&
      new Set(ids).size === ids.length &&
      ids.every((s) => /^(?:[0-9a-f]{2}){1,8}$/.test(s)),
    'Short ID 需为 2–16 位偶数长度小写十六进制，最多 16 项且不可重复'
  );
export const inboundSchema = z
  .discriminatedUnion('protocol', [
    z.object({
      ...base,
      protocol: z.literal('vless'),
      handshake_server: z
        .string()
        .trim()
        .min(1, '请填写握手站点')
        .max(253)
        .regex(/^[A-Za-z0-9.:-]+$/, '填写域名或 IP，不带协议或路径'),
      handshake_port: port,
      short_ids: shortIDs,
    }),
    z.object({ ...base, protocol: z.literal('shadowsocks') }),
    z.object({
      ...base,
      protocol: z.literal('hysteria2'),
      obfs_enabled: z.boolean(),
      up_mbps: bandwidth,
      down_mbps: bandwidth,
      ignore_client_bandwidth: z.boolean(),
    }),
    z.object({
      ...base,
      protocol: z.literal('tuic'),
      congestion_control: z.enum(['bbr', 'cubic', 'new_reno']),
      zero_rtt: z.boolean(),
    }),
  ])
  .superRefine((v, ctx) => {
    if (v.listen_port === 10085 && v.protocol !== 'hysteria2' && v.protocol !== 'tuic')
      ctx.addIssue({
        code: 'custom',
        path: ['listen_port'],
        message: '10085/tcp 为核心统计保留端口',
      });
  });

export function inboundDefaults(
  protocol: ProxyProtocol,
  inbounds: Inbound[],
  current?: Inbound
): z.input<typeof inboundSchema> {
  const settings = current?.settings;
  const common = {
    listen_port: current?.listen_port ?? suggestPort(protocol, inbounds) ?? '',
    remark: current?.remark ?? '',
    enabled: current?.enabled ?? true,
  };
  switch (protocol) {
    case 'vless':
      return {
        ...common,
        protocol,
        handshake_server: settings?.handshake_server ?? 'www.microsoft.com',
        handshake_port: settings?.handshake_port ?? 443,
        short_ids: settings?.short_ids?.join(', ') ?? '',
      };
    case 'shadowsocks':
      return { ...common, protocol };
    case 'hysteria2':
      return {
        ...common,
        protocol,
        obfs_enabled: settings?.obfs_enabled ?? false,
        up_mbps: settings?.up_mbps ?? 0,
        down_mbps: settings?.down_mbps ?? 0,
        ignore_client_bandwidth: settings?.ignore_client_bandwidth ?? false,
      };
    case 'tuic':
      return {
        ...common,
        protocol,
        congestion_control: settings?.congestion_control ?? 'bbr',
        zero_rtt: settings?.zero_rtt ?? false,
      };
    default:
      throw new Error('不支持的代理协议');
  }
}

export function inboundPayload(v: z.output<typeof inboundSchema>): InboundPayload {
  const { protocol, listen_port, remark, enabled } = v;
  const settings: InboundPayload['settings'] = {};
  switch (v.protocol) {
    case 'vless':
      Object.assign(
        settings,
        { handshake_server: v.handshake_server, handshake_port: v.handshake_port },
        v.short_ids.length ? { short_ids: v.short_ids } : {}
      );
      break;
    case 'hysteria2':
      Object.assign(settings, {
        obfs_enabled: v.obfs_enabled,
        up_mbps: v.up_mbps,
        down_mbps: v.down_mbps,
        ignore_client_bandwidth: v.ignore_client_bandwidth,
      });
      break;
    case 'tuic':
      Object.assign(settings, { congestion_control: v.congestion_control, zero_rtt: v.zero_rtt });
      break;
    case 'shadowsocks':
      break;
    // no default
  }
  // 生成凭据不回传，避免编辑时覆盖另一管理员刚重生的密钥。
  return { protocol, listen_port, remark, enabled, settings };
}

export function revisionApplied(
  core: CoreState,
  target: Pick<CoreRevisionTarget, 'revision' | 'sha256' | 'version'>
) {
  return (
    core.online &&
    core.running &&
    !core.pending &&
    core.applied_revision === target.revision &&
    core.config_sha256 === target.sha256 &&
    core.installed_version === target.version
  );
}
type CoreRevisionTarget = { revision: number; sha256: string; version: string };

/** 日志以纯文本展示，移除终端颜色控制序列。 */
export function plainLog(text: string) {
  // eslint-disable-next-line no-control-regex
  return text.replace(/\x1b\[[0-9;]*m/g, '');
}
