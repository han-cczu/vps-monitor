import useSWR from 'swr';

import { setFormatPreferences } from 'src/utils/format';

import axios, { fetcher } from 'src/lib/axios';

export type SiteSettings = {
  'site.title': string;
  'site.tz': string;
  'site.bytes_base': 1000 | 1024;
  'retention.metrics_minute_days': number;
  'retention.metrics_hour_days': number;
  'retention.ping_days': number;
  'retention.audit_days': number;
  'alert.cooldown_minutes': number;
  'enforce.count_mode': 'sum' | 'download';
  'sub.clash_template': string;
};
const apply = (data: SiteSettings) => {
  setFormatPreferences(data['site.bytes_base'], data['site.tz']);
  return data;
};
export function useSiteSettings() {
  return useSWR<SiteSettings>(
    '/api/settings',
    async (url: string) => apply(await fetcher<SiteSettings>(url)),
    { revalidateOnFocus: true }
  );
}
export async function saveSiteSettings(values: Partial<SiteSettings>) {
  return apply((await axios.put<SiteSettings>('/api/settings', values)).data);
}
export type AuditEntry = {
  id: number;
  ts: number;
  actor: string;
  action: string;
  target_type: string;
  target_id: string;
  before: string;
  after: string;
  ip: string;
};
export function useAudit(query: URLSearchParams) {
  return useSWR<{ items: AuditEntry[]; total: number }>(`/api/audit?${query.toString()}`, fetcher, {
    keepPreviousData: true,
  });
}
export function useTOTP() {
  return useSWR<{ enabled: boolean }>('/api/auth/totp', fetcher);
}
export async function setupTOTP(password: string) {
  return (await axios.post<{ secret: string; url: string }>('/api/auth/totp/setup', { password }))
    .data;
}
export async function toggleTOTP(enabled: boolean, code: string) {
  await axios.post(`/api/auth/totp/${enabled ? 'enable' : 'disable'}`, { code });
}
export type AgentVersionItem = {
  id: number;
  version: string;
  online: boolean;
  supported: boolean;
  update_available: boolean;
};
export function useAgentVersions() {
  return useSWR<{ version: string; servers: AgentVersionItem[] }>('/api/agent-version', fetcher, {
    refreshInterval: 5000,
  });
}
export async function updateAgents(id?: number) {
  return (
    await axios.post<{ queued: boolean | number[]; skipped?: { id: number; reason: string }[] }>(
      id === undefined ? '/api/servers/agent/update-all' : `/api/servers/${id}/agent/update`
    )
  ).data;
}
