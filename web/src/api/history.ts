import type { SWRConfiguration } from 'swr';
import type { HistoryRange, HistoryResponse } from 'src/types/history';

import useSWR from 'swr';
import { useMemo } from 'react';

import { fetcher, endpoints } from 'src/lib/axios';

// ----------------------------------------------------------------------

/**
 * 历史曲线每分钟刷一次。
 *
 * 不跟着实时快照每秒刷：分钟表本来就一分钟才多一个点，每秒拉一次纯属浪费；
 * 页面切到后台时也不刷（revalidateOnFocus 回来时会补一次）。
 */
const swrOptions: SWRConfiguration = {
  refreshInterval: 60_000,
  revalidateOnFocus: true,
  keepPreviousData: true,
};

export function useHistory(id: number, range: HistoryRange) {
  const { data, isLoading, error, isValidating } = useSWR<HistoryResponse>(
    endpoints.servers.history(id, range),
    fetcher,
    swrOptions
  );

  return useMemo(
    () => ({
      history: data,
      historyLoading: isLoading,
      historyError: error,
      historyValidating: isValidating,
      // 切换时间窗时 keepPreviousData 会先给上一段的数据，用 step 判断是不是还没换过来
      historyEmpty: !isLoading && !data?.points.length,
    }),
    [data, error, isLoading, isValidating]
  );
}
