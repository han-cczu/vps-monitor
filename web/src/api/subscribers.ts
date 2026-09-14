import type { Subscriber, SubscriberPayload, SubscriberTraffic } from 'src/types/subscriber';

import useSWR, { mutate } from 'swr';

import { CONFIG } from 'src/global-config';
import axios, { fetcher } from 'src/lib/axios';

const base = '/api/subscribers';
export function useSubscribers() {
  return useSWR<{ subscribers: Subscriber[] }>(base, fetcher, { refreshInterval: 5000 });
}
export function useSubscriber(id?: number) {
  return useSWR<{ subscriber: Subscriber }>(id ? `${base}/${id}` : null, fetcher, {
    refreshInterval: 5000,
  });
}
export function useSubscriberTraffic(id: number) {
  return useSWR<SubscriberTraffic>(`${base}/${id}/traffic`, fetcher, { refreshInterval: 10000 });
}
export async function refreshSubscribers(id?: number) {
  await Promise.allSettled([
    mutate(base),
    ...(id ? [mutate(`${base}/${id}`), mutate(`${base}/${id}/traffic`)] : []),
  ]);
}
export async function saveSubscriber(id: number | undefined, payload: SubscriberPayload) {
  const result = id ? await axios.put(`${base}/${id}`, payload) : await axios.post(base, payload);
  return result.data.subscriber as Subscriber;
}
export async function assignSubscriber(id: number, inboundIds: number[]) {
  return (
    await axios.put<{ subscriber: Subscriber }>(`${base}/${id}/assignments`, {
      inbound_ids: inboundIds,
    })
  ).data.subscriber;
}
export async function deleteSubscriber(id: number) {
  await axios.delete(`${base}/${id}`);
  await refreshSubscribers(id);
}
export async function subscriberAction(
  id: number,
  action: 'reset-token' | 'regenerate-credentials' | 'reset-usage'
) {
  const result = (await axios.post<{ subscriber: Subscriber }>(`${base}/${id}/${action}`)).data
    .subscriber;
  await refreshSubscribers(id);
  return result;
}
export function subscriptionURL(token: string, format: string) {
  return `${(CONFIG.serverUrl || window.location.origin).replace(/\/$/, '')}/sub/${encodeURIComponent(token)}?format=${encodeURIComponent(format)}`;
}
