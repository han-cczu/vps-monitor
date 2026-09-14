import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';

import { useAdvanced } from 'src/api/proxy';

export function AdvancedHint({ serverId }: { serverId: number }) {
  const { data, error, mutate } = useAdvanced(serverId);
  if (error)
    return (
      <Alert severity="error" action={<Button onClick={() => mutate()}>重试</Button>}>
        高级配置状态加载失败。
      </Alert>
    );
  if (!data) return null;
  return (
    <Alert severity="info">
      高级 JSON：{Object.keys(data.advanced.extra_json).length ? '已配置，参与配置渲染' : '未配置'}
      。编辑入口将在后续步骤提供。
    </Alert>
  );
}
