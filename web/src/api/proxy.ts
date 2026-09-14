import type {
  Inbound,
  NodeCert,
  CoreState,
  CoreRevision,
  RevisionDetail,
  InboundPayload,
  ProxyAssignment,
} from 'src/types/proxy';

import useSWR from 'swr';
import { useEffect } from 'react';

import axios, { fetcher } from 'src/lib/axios';
import { useRealtime } from 'src/store/realtime';

const node = (id: number) => `/api/servers/${id}`;
const corePath = (id: number) => `${node(id)}/core`;
const polling = { refreshInterval: 5000 };

export function useCore(id: number) {
  const result = useSWR<{ core: CoreState }>(corePath(id), fetcher, polling);
  // 比较业务字段，避免每秒收到的新对象引用触发额外 HTTP 请求。
  const signal = useRealtime((state) => {
    const s = state.servers[id];
    return JSON.stringify([s?.online, s?.core]);
  });
  const { mutate } = result;
  useEffect(() => {
    void mutate().catch(() => undefined); // 错误由 SWR error 与页面错误条呈现。
  }, [signal, mutate]);
  return { ...result, core: result.data?.core };
}
export function useInbounds(id: number) {
  return useSWR<{ inbounds: Inbound[] }>(id ? `${node(id)}/inbounds` : null, fetcher, polling);
}
export function useCert(id: number) {
  return useSWR<{ cert: NodeCert | null }>(`${node(id)}/cert`, fetcher, polling);
}
export function useRevisions(id: number) {
  return useSWR<{ revisions: CoreRevision[] }>(`${corePath(id)}/revisions`, fetcher, polling);
}
export function useAdvanced(id: number) {
  return useSWR<{ advanced: { extra_json: Record<string, unknown> } }>(
    `${node(id)}/advanced`,
    fetcher
  );
}
export function useProxyAssignments() {
  return useSWR<{ subscribers: ProxyAssignment[] }>('/api/subscribers?include_relay=1', fetcher, polling);
}

export async function installCore(id: number) {
  await axios.post(`${corePath(id)}/install`);
}
export async function restartCore(id: number) {
  await axios.post(`${corePath(id)}/restart`);
}
export async function applyCore(id: number) {
  return (await axios.post<{ revision: CoreRevision; core: CoreState }>(`${corePath(id)}/apply`))
    .data;
}
export async function fetchLogs(id: number, lines: number, signal?: AbortSignal) {
  return (
    await axios.get<{ text: string }>(`${corePath(id)}/logs`, {
      params: { lines },
      timeout: 15_000,
      signal,
    })
  ).data.text;
}
export async function fetchRevision(id: number, revision: number) {
  return (await axios.get<{ revision: RevisionDetail }>(`${corePath(id)}/revisions/${revision}`))
    .data.revision;
}
export async function rollback(id: number, revision: number) {
  return (await axios.post<{ revision: CoreRevision }>(`${corePath(id)}/rollback/${revision}`)).data
    .revision;
}
export async function createInbound(id: number, payload: InboundPayload) {
  return (await axios.post<{ inbound: Inbound }>(`${node(id)}/inbounds`, payload)).data.inbound;
}
export async function updateInbound(id: number, payload: Partial<InboundPayload>) {
  return (await axios.put<{ inbound: Inbound }>(`/api/inbounds/${id}`, payload)).data.inbound;
}
export async function deleteInbound(id: number) {
  await axios.delete(`/api/inbounds/${id}`);
}
export async function regenerateKeys(id: number) {
  return (await axios.post<{ inbound: Inbound }>(`/api/inbounds/${id}/regenerate-keys`)).data
    .inbound;
}
export async function regenerateCert(id: number) {
  return (await axios.post<{ cert: NodeCert }>(`${node(id)}/cert/regenerate`)).data.cert;
}
