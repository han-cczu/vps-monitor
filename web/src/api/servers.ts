import type { SWRConfiguration } from 'swr';
import type {
  ServerItem,
  ServerPayload,
  ServerTokenResult,
  ServerCreateResult,
} from 'src/types/server';

import useSWR from 'swr';
import { useMemo } from 'react';

import axios, { fetcher, endpoints } from 'src/lib/axios';

// ----------------------------------------------------------------------

type ServersData = { servers: ServerItem[] };
type ServerData = { server: ServerItem };

const swrOptions: SWRConfiguration = {
  revalidateOnFocus: false,
  revalidateOnReconnect: true,
};

/**
 * 节点列表。
 *
 * 在线状态要到步骤 05（实时 Hub）才会动，所以这里不做轮询：
 * 增删改之后由调用方 refreshServers() 主动刷新。
 */
export function useServers() {
  const { data, isLoading, error, isValidating, mutate } = useSWR<ServersData>(
    endpoints.servers.root,
    fetcher,
    swrOptions
  );

  return useMemo(
    () => ({
      servers: data?.servers ?? [],
      serversLoading: isLoading,
      serversError: error,
      serversValidating: isValidating,
      serversEmpty: !isLoading && !data?.servers.length,
      refreshServers: mutate,
    }),
    [data?.servers, error, isLoading, isValidating, mutate]
  );
}

// ----------------------------------------------------------------------

/** 新建节点。响应里的 token 是明文，只在这一次出现。 */
export async function createServer(payload: ServerPayload): Promise<ServerCreateResult> {
  const res = await axios.post<ServerCreateResult>(endpoints.servers.root, payload);
  return res.data;
}

/** 更新节点（全量覆盖可写字段，token 不受影响）。 */
export async function updateServer(id: number, payload: ServerPayload): Promise<ServerItem> {
  const res = await axios.put<ServerData>(endpoints.servers.byId(id), payload);
  return res.data.server;
}

/** 删除节点，连带删掉它的采集数据。 */
export async function deleteServer(id: number): Promise<void> {
  await axios.delete(endpoints.servers.byId(id));
}

/** 重置 agent token：旧 token 立刻失效，已装的 agent 需要重装或改配置。 */
export async function resetServerToken(id: number): Promise<ServerTokenResult> {
  const res = await axios.post<ServerTokenResult>(endpoints.servers.token(id));
  return res.data;
}
