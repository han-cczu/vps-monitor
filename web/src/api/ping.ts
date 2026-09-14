import type {
  PingTask,
  PingRange,
  PingHistory,
  PingRecentTask,
  PingTaskPayload,
} from 'src/types/ping';

import useSWR from 'swr';

import axios, { fetcher } from 'src/lib/axios';

const root = '/api/ping-tasks';
const polling = { refreshInterval: 60_000, revalidateOnFocus: true };

export function usePingTasks() {
  const { data, error, isLoading, mutate } = useSWR<{ tasks: PingTask[] }>(root, fetcher, polling);
  return { tasks: data?.tasks ?? [], error, isLoading, refresh: mutate };
}
export function usePingRecent(serverID: number, enabled = true) {
  return useSWR<{ tasks: PingRecentTask[] }>(
    enabled ? `/api/servers/${serverID}/ping/recent?n=30` : null,
    fetcher,
    polling
  );
}
export function usePingHistory(serverID: number, taskID: number, range: PingRange) {
  return useSWR<PingHistory>(
    `/api/servers/${serverID}/ping/history?task=${taskID}&range=${range}`,
    fetcher,
    polling
  );
}
export async function savePingTask(id: number | undefined, payload: PingTaskPayload) {
  const response = id
    ? await axios.put<{ task: PingTask; pushed: number }>(`${root}/${id}`, payload)
    : await axios.post<{ task: PingTask; pushed: number }>(root, payload);
  return response.data;
}
export async function deletePingTask(id: number) {
  await axios.delete(`${root}/${id}`);
}
