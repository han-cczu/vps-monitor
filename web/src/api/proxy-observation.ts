import type { ProxyObservations, ProxyObservationResetResult } from 'src/types/proxy-observation';

import useSWR from 'swr';

import axios, { fetcher } from 'src/lib/axios';

export const proxyObservationsKey = (id: number) => `/api/servers/${id}/proxy-observations`;

export function useProxyObservations(id: number) {
  return useSWR<ProxyObservations>(proxyObservationsKey(id), fetcher, {
    refreshInterval: 5000,
  });
}
export async function refreshProxyObservations(id: number) {
  await axios.post(`${proxyObservationsKey(id)}/refresh`);
}
/** 删除面板保存的一条观测记录；不影响探针、代理进程或真实流量计数。 */
export async function deleteProxyObservation(id: number, instance: string) {
  await axios.delete(`${proxyObservationsKey(id)}/${encodeURIComponent(instance)}`);
}
/** 清空本节点在面板保存的全部观测快照；在线时会一并请求重新采集。 */
export async function resetProxyObservations(id: number) {
  const { data } = await axios.post<ProxyObservationResetResult>(
    `${proxyObservationsKey(id)}/reset`
  );
  return data;
}
