import useSWR from 'swr';

import axios, { fetcher } from 'src/lib/axios';

export type UpdateStatus = 'unknown' | 'available' | 'current' | 'ahead';

export type UpdateInfo = {
  repository: string;
  state: 'unchecked' | 'ok' | 'error';
  checked_at: number;
  next_check_at: number;
  message: string;
  latest: { version: string; url: string; published_at: string } | null;
  panel_version: string;
  panel_status: UpdateStatus;
  agent_version: string;
  agent_status: UpdateStatus;
};

export function useUpdates() {
  return useSWR<UpdateInfo>('/api/updates', fetcher, { revalidateOnFocus: false });
}

export async function checkUpdates() {
  return (await axios.post<UpdateInfo>('/api/updates/check', undefined, { timeout: 20_000 })).data;
}
