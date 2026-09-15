import type { ProxyObservations } from 'src/types/proxy-observation';

import useSWR from 'swr';

import axios, { fetcher } from 'src/lib/axios';

export function useProxyObservations(id: number) {
  return useSWR<ProxyObservations>(`/api/servers/${id}/proxy-observations`, fetcher, {
    refreshInterval: 5000,
  });
}
export async function refreshProxyObservations(id: number) {
  await axios.post(`/api/servers/${id}/proxy-observations/refresh`);
}
