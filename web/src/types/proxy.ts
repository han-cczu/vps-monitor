export type ProxyProtocol = 'vless' | 'shadowsocks' | 'hysteria2' | 'tuic';

export type InboundSettings = {
  handshake_server?: string;
  handshake_port?: number;
  private_key?: string;
  public_key?: string;
  short_ids?: string[];
  method?: string;
  server_psk?: string;
  obfs_enabled?: boolean;
  obfs_password?: string;
  up_mbps?: number;
  down_mbps?: number;
  ignore_client_bandwidth?: boolean;
  congestion_control?: 'bbr' | 'cubic' | 'new_reno';
  zero_rtt?: boolean;
};

export type InboundPayload = {
  protocol: ProxyProtocol;
  listen_port: number;
  remark: string;
  enabled: boolean;
  settings: InboundSettings;
};
export type Inbound = InboundPayload & {
  id: number;
  server_id: number;
  tag: string;
  created_at: number;
  updated_at: number;
};
export type CoreState = {
  server_id: number;
  core: 'sing-box';
  online: boolean;
  running: boolean;
  pending: boolean;
  current_version: string;
  installed_version: string | null;
  desired_version: string | null;
  applied_revision: number;
  desired_revision: number;
  config_sha256: string | null;
  listening: string[];
  firewall: string | null;
  last_error: string | null;
  updated_at: number | null;
  inbounds: { name: string; up: number; down: number }[];
};
export type NodeCert = {
  server_id: number;
  sni: string;
  cert_pem: string;
  fingerprint_sha256: string;
  not_after: number;
  created_at: number;
};
export type CoreRevision = {
  server_id: number;
  revision: number;
  sha256: string;
  created_at: number;
  created_by: string;
  version: string;
  ports: string[];
  applied: boolean;
};
export type RevisionDetail = CoreRevision & { config_json: Record<string, unknown> };
// 列表接口不含订阅 token 或代理凭据；只用于统计已分配人数。
export type ProxyAssignment = {
  id: number;
  assigned_inbounds: { inbound_id: number; server_id: number }[];
};
