import type { Inbound } from 'src/types/proxy';

import useSWR from 'swr';

import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import Checkbox from '@mui/material/Checkbox';
import Typography from '@mui/material/Typography';
import { TreeItem } from '@mui/x-tree-view/TreeItem';
import { SimpleTreeView } from '@mui/x-tree-view/SimpleTreeView';

import { fetcher } from 'src/lib/axios';
import { useServers } from 'src/api/servers';

import { Label } from 'src/components/label';

import { getErrorMessage } from 'src/auth/utils';

import { PROTOCOLS } from '../proxy/helpers';

export function AssignmentPicker({
  value,
  onChange,
}: {
  value: number[];
  onChange: (ids: number[]) => void;
}) {
  const { servers, serversError, serversLoading } = useServers();
  const result = useSWR(
    servers.length ? ['assignment-inbounds', servers.map((s) => s.id)] : null,
    async ([, ids]: [string, number[]]) =>
      Promise.all(
        ids.map(async (id) => ({
          id,
          inbounds: (await fetcher<{ inbounds: Inbound[] }>(`/api/servers/${id}/inbounds`))
            .inbounds,
        }))
      )
  );
  const toggle = (ids: number[], checked: boolean) =>
    onChange(checked ? [...new Set([...value, ...ids])] : value.filter((id) => !ids.includes(id)));
  if (serversError || result.error)
    return (
      <Alert severity="error">
        加载分配失败：{getErrorMessage(serversError || result.error)}。现有选择会保留。
      </Alert>
    );
  if (serversLoading || result.isLoading) return <Typography>加载入站…</Typography>;
  return (
    <Box>
      {value.length === 0 && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          未分配入站，订阅将没有节点。
        </Alert>
      )}
      {!servers.length && <Typography>请先新增节点和入站。</Typography>}
      <SimpleTreeView disableSelection>
        {servers.map((server) => {
          const inbounds = result.data?.find((s) => s.id === server.id)?.inbounds ?? [];
          const ids = inbounds.map((i) => i.id);
          const selected = ids.filter((id) => value.includes(id)).length;
          return (
            <TreeItem
              key={server.id}
              itemId={`server-${server.id}`}
              label={
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  <Checkbox
                    disabled={!ids.length}
                    checked={ids.length > 0 && selected === ids.length}
                    indeterminate={selected > 0 && selected < ids.length}
                    onClick={(event) => event.stopPropagation()}
                    onChange={(_, checked) => toggle(ids, checked)}
                    slotProps={{ input: { 'aria-label': `全选 ${server.name}` } }}
                  />
                  {server.name}
                  {!server.public_host && (
                    <Label color="warning" sx={{ ml: 1 }}>
                      未填写公开地址
                    </Label>
                  )}
                </Box>
              }
            >
              {inbounds.map((inbound) => (
                <TreeItem
                  key={inbound.id}
                  itemId={`inbound-${inbound.id}`}
                  label={
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        opacity: inbound.enabled ? 1 : 0.55,
                        gap: 1,
                      }}
                    >
                      <Checkbox
                        checked={value.includes(inbound.id)}
                        onChange={(_, checked) => toggle([inbound.id], checked)}
                        slotProps={{
                          input: {
                            'aria-label': `${server.name} ${inbound.protocol} ${inbound.listen_port}`,
                          },
                        }}
                      />
                      <Label color={PROTOCOLS[inbound.protocol].color}>
                        {PROTOCOLS[inbound.protocol].label}
                      </Label>
                      <Typography variant="body2">
                        :{inbound.listen_port} {inbound.remark} {!inbound.enabled && '（入站停用）'}
                      </Typography>
                    </Box>
                  }
                />
              ))}
            </TreeItem>
          );
        })}
      </SimpleTreeView>
    </Box>
  );
}
