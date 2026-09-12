import type { ServerItem, ServerPayload, ServerCreateResult } from 'src/types/server';

import * as z from 'zod';
import dayjs from 'dayjs';
import { useForm } from 'react-hook-form';
import { useState, useEffect } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';

import Box from '@mui/material/Box';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import MenuItem from '@mui/material/MenuItem';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';
import InputAdornment from '@mui/material/InputAdornment';

import { createServer, updateServer } from 'src/api/servers';

import { toast } from 'src/components/snackbar';
import { Form, Field } from 'src/components/hook-form';

import { getErrorMessage } from 'src/auth/utils';

import { joinTraffic, splitTraffic, TRAFFIC_UNITS } from './utils';
import {
  REGION_LABELS,
  REGION_OPTIONS,
  CURRENCY_OPTIONS,
  TRAFFIC_MODE_OPTIONS,
  BILLING_CYCLE_OPTIONS,
} from './constants';

// ----------------------------------------------------------------------

// 与服务端 servers_input.go 的规则保持一致，改一边记得改另一边
const ServerSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, { error: '请填写节点名称' })
    .max(64, { error: '节点名称最多 64 个字符' }),
  region: z
    .string()
    .trim()
    .regex(/^[A-Za-z]{2}$|^$/, { error: '地区需要是两位国家码，如 HK、JP、US' }),
  group_name: z.string().trim().max(32, { error: '分组名最多 32 个字符' }),
  tags: z.array(z.string().trim().max(16, { error: '单个标签最多 16 个字符' })).max(10, {
    error: '标签最多 10 个',
  }),
  sort_order: z.coerce
    .number({ error: '排序值需要是数字' })
    .int({ error: '排序值需要是整数' })
    .min(-1_000_000, { error: '排序值超出范围' })
    .max(1_000_000, { error: '排序值超出范围' }),
  public_host: z
    .string()
    .trim()
    .max(253, { error: '公网地址过长' })
    .refine((val) => val === '' || /^[A-Za-z0-9.:_-]+$/.test(val), {
      error: '公网地址需要是域名或 IP，不带协议和端口',
    }),
  price: z.coerce
    .number({ error: '价格需要是数字' })
    .min(0, { error: '价格不能为负' })
    .max(1e9, { error: '价格过大' }),
  currency: z.string().trim().length(3, { error: '货币需要是三位代码' }),
  billing_cycle: z.enum(['month', 'quarter', 'year', 'once']),
  expire_at: z.string().nullable(),
  auto_renew: z.boolean(),
  traffic_limit_value: z.coerce
    .number({ error: '流量上限需要是数字' })
    .min(0, { error: '流量上限不能为负' })
    .max(1e6, { error: '流量上限过大' }),
  traffic_limit_unit: z.enum(['GB', 'TB']),
  traffic_reset_day: z.coerce.number().int().min(1).max(31),
  traffic_mode: z.enum(['out', 'in', 'sum', 'max']),
  bandwidth_label: z.string().trim().max(32, { error: '带宽标签最多 32 个字符' }),
  note: z.string().trim().max(500, { error: '备注最多 500 个字符' }),
});

// coerce 的 input 是 unknown（数字输入框在敲的过程中是字符串），output 才是 number，
// 所以 useForm 用三个泛型：表单里存 input，提交回调拿 output。
type ServerFormValues = z.infer<typeof ServerSchema>;
type ServerFormInput = z.input<typeof ServerSchema>;

type TabValue = 'basic' | 'access' | 'billing' | 'plan';

// 校验失败时要切到出错的那一页，否则用户只看到「保存」没反应
const FIELD_TABS: Record<string, TabValue> = {
  name: 'basic',
  region: 'basic',
  group_name: 'basic',
  tags: 'basic',
  sort_order: 'basic',
  note: 'basic',
  public_host: 'access',
  price: 'billing',
  currency: 'billing',
  billing_cycle: 'billing',
  expire_at: 'billing',
  auto_renew: 'billing',
  traffic_limit_value: 'plan',
  traffic_limit_unit: 'plan',
  traffic_reset_day: 'plan',
  traffic_mode: 'plan',
  bandwidth_label: 'plan',
};

const TABS: { value: TabValue; label: string }[] = [
  { value: 'basic', label: '基础' },
  { value: 'access', label: '接入' },
  { value: 'billing', label: '账单' },
  { value: 'plan', label: '套餐' },
];

function toFormValues(server?: ServerItem | null): ServerFormInput {
  const traffic = splitTraffic(server?.traffic_limit ?? 0);

  return {
    name: server?.name ?? '',
    region: server?.region ?? '',
    group_name: server?.group_name ?? '',
    tags: server?.tags ?? [],
    sort_order: server?.sort_order ?? 0,
    public_host: server?.public_host ?? '',
    price: server?.price ?? 0,
    currency: server?.currency ?? 'CNY',
    billing_cycle: server?.billing_cycle ?? 'month',
    expire_at: server?.expire_at ?? null,
    auto_renew: server?.auto_renew ?? false,
    traffic_limit_value: traffic.value,
    traffic_limit_unit: traffic.unit,
    traffic_reset_day: server?.traffic_reset_day ?? 1,
    traffic_mode: server?.traffic_mode ?? 'max',
    bandwidth_label: server?.bandwidth_label ?? '',
    note: server?.note ?? '',
  };
}

function toPayload(values: ServerFormValues): ServerPayload {
  return {
    name: values.name,
    region: values.region.toUpperCase(),
    group_name: values.group_name,
    tags: values.tags,
    sort_order: values.sort_order,
    public_host: values.public_host,
    price: values.price,
    currency: values.currency.toUpperCase(),
    billing_cycle: values.billing_cycle,
    expire_at: values.expire_at ? dayjs(values.expire_at).format('YYYY-MM-DD') : null,
    auto_renew: values.auto_renew,
    traffic_limit: joinTraffic(values.traffic_limit_value, values.traffic_limit_unit),
    traffic_reset_day: values.traffic_reset_day,
    traffic_mode: values.traffic_mode,
    bandwidth_label: values.bandwidth_label,
    note: values.note,
  };
}

// ----------------------------------------------------------------------

type Props = {
  open: boolean;
  /** 有值就是编辑态 */
  currentServer?: ServerItem | null;
  onClose: () => void;
  /** 新建成功：外层拿 token 弹「只显示一次」的对话框 */
  onCreated: (result: ServerCreateResult) => void;
  onUpdated: () => void;
  /** 编辑态里点「重置 token」 */
  onResetToken: (server: ServerItem) => void;
};

export function ServerFormDialog({
  open,
  currentServer,
  onClose,
  onCreated,
  onUpdated,
  onResetToken,
}: Props) {
  const isEdit = !!currentServer;
  const [tab, setTab] = useState<TabValue>('basic');

  const methods = useForm<ServerFormInput, unknown, ServerFormValues>({
    mode: 'onSubmit',
    resolver: zodResolver(ServerSchema),
    defaultValues: toFormValues(currentServer),
  });

  const {
    reset,
    handleSubmit,
    formState: { isSubmitting },
  } = methods;

  // 每次打开都按当前节点重置表单，避免上一次的输入残留
  useEffect(() => {
    if (open) {
      reset(toFormValues(currentServer));
      setTab('basic');
    }
  }, [open, currentServer, reset]);

  const onSubmit = handleSubmit(
    async (values) => {
      try {
        const payload = toPayload(values);

        if (currentServer) {
          await updateServer(currentServer.id, payload);
          toast.success('节点已保存');
          onUpdated();
        } else {
          const result = await createServer(payload);
          onCreated(result);
        }
        onClose();
      } catch (error) {
        console.error(error);
        toast.error(getErrorMessage(error));
      }
    },
    (errors) => {
      const firstField = Object.keys(errors)[0];
      if (firstField && FIELD_TABS[firstField]) {
        setTab(FIELD_TABS[firstField]);
      }
    }
  );

  return (
    <Dialog fullWidth maxWidth="sm" open={open} onClose={onClose}>
      <Form methods={methods} onSubmit={onSubmit}>
        <DialogTitle sx={{ pb: 1 }}>{isEdit ? '编辑节点' : '新增节点'}</DialogTitle>

        <Tabs
          value={tab}
          onChange={(_, value) => setTab(value)}
          variant="scrollable"
          scrollButtons={false}
          sx={{ px: 3, minHeight: 40 }}
        >
          {TABS.map((item) => (
            <Tab key={item.value} value={item.value} label={item.label} />
          ))}
        </Tabs>

        <DialogContent dividers sx={{ pt: 3 }}>
          <TabPanel active={tab === 'basic'}>
            <Field.Text name="name" label="名称" placeholder="hk-01" />

            <Field.Autocomplete
              name="region"
              label="地区"
              placeholder="选择或输入两位国家码"
              freeSolo
              autoSelect
              options={REGION_OPTIONS.map((item) => item.code)}
              getOptionLabel={(option) =>
                typeof option === 'string' ? (REGION_LABELS[option] ?? option) : ''
              }
              renderOption={(props, option) => (
                <li {...props} key={String(option)}>
                  {REGION_LABELS[String(option)] ?? option} {String(option)}
                </li>
              )}
              helperText="订阅与卡片上的国旗按这个国家码显示"
            />

            <Box sx={{ display: 'grid', gap: 2.5, gridTemplateColumns: { sm: '1fr 1fr' } }}>
              <Field.Text name="group_name" label="分组" placeholder="亚洲" />
              <Field.Text name="sort_order" label="排序" type="number" helperText="小的排前面" />
            </Box>

            <Field.Autocomplete
              name="tags"
              label="标签"
              placeholder="回车添加"
              multiple
              freeSolo
              autoSelect
              options={[]}
              helperText="最多 10 个，每个 16 字以内"
            />

            <Field.Text name="note" label="备注" multiline rows={2} />
          </TabPanel>

          <TabPanel active={tab === 'access'}>
            <Field.Text
              name="public_host"
              label="公网地址"
              placeholder="hk1.example.com 或 1.2.3.4"
              helperText="订阅链接里用的地址，留空则用 agent 上报的公网 IP"
            />

            {isEdit ? (
              <Alert
                severity="info"
                action={
                  <Button
                    color="warning"
                    size="small"
                    onClick={() => currentServer && onResetToken(currentServer)}
                  >
                    重置 token
                  </Button>
                }
              >
                agent token 只在创建时显示一次。丢了就重置一个，重置后这台节点上的 agent 需要重装。
              </Alert>
            ) : (
              <Alert severity="info">保存后会生成 agent token 与一键安装命令，只显示这一次。</Alert>
            )}
          </TabPanel>

          <TabPanel active={tab === 'billing'}>
            <Box sx={{ display: 'grid', gap: 2.5, gridTemplateColumns: { sm: '1fr 1fr' } }}>
              <Field.Text name="price" label="价格" type="number" />
              <Field.Select name="currency" label="货币">
                {CURRENCY_OPTIONS.map((item) => (
                  <MenuItem key={item.code} value={item.code}>
                    {item.label}
                  </MenuItem>
                ))}
              </Field.Select>
            </Box>

            <Field.Select name="billing_cycle" label="周期">
              {BILLING_CYCLE_OPTIONS.map((item) => (
                <MenuItem key={item.value} value={item.value}>
                  {item.label}
                </MenuItem>
              ))}
            </Field.Select>

            <Field.DatePicker name="expire_at" label="到期日" format="YYYY-MM-DD" />

            <Field.Switch
              name="auto_renew"
              label="到期自动顺延"
              helperText="开启后，到期时按周期自动把到期日推后一期（结算在步骤 18）"
            />
          </TabPanel>

          <TabPanel active={tab === 'plan'}>
            <Box sx={{ display: 'grid', gap: 2.5, gridTemplateColumns: { sm: '2fr 1fr' } }}>
              <Field.Text
                name="traffic_limit_value"
                label="流量上限"
                type="number"
                helperText="0 表示不限"
                slotProps={{
                  input: {
                    endAdornment: <InputAdornment position="end">/ 月</InputAdornment>,
                  },
                }}
              />
              <Field.Select name="traffic_limit_unit" label="单位">
                {TRAFFIC_UNITS.map((unit) => (
                  <MenuItem key={unit} value={unit}>
                    {unit}
                  </MenuItem>
                ))}
              </Field.Select>
            </Box>

            <Field.Select name="traffic_reset_day" label="每月重置日">
              {Array.from({ length: 31 }, (_, i) => i + 1).map((day) => (
                <MenuItem key={day} value={day}>
                  {day} 号
                </MenuItem>
              ))}
            </Field.Select>

            <Field.Select
              name="traffic_mode"
              label="统计模式"
              helperText={
                TRAFFIC_MODE_OPTIONS.find((item) => item.value === methods.watch('traffic_mode'))
                  ?.help
              }
            >
              {TRAFFIC_MODE_OPTIONS.map((item) => (
                <MenuItem key={item.value} value={item.value}>
                  {item.label}
                </MenuItem>
              ))}
            </Field.Select>

            <Field.Text name="bandwidth_label" label="带宽标签" placeholder="1 Gbps" />
          </TabPanel>
        </DialogContent>

        <DialogActions>
          <Button variant="outlined" color="inherit" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" variant="contained" loading={isSubmitting}>
            {isEdit ? '保存' : '创建'}
          </Button>
        </DialogActions>
      </Form>
    </Dialog>
  );
}

// ----------------------------------------------------------------------

/**
 * 所有 Tab 的字段始终挂载（只切显示），这样切走的页签里的校验错误不会丢，
 * 提交失败时切回去就能看到红字。
 */
function TabPanel({ active, children }: { active: boolean; children: React.ReactNode }) {
  return (
    <Box
      role="tabpanel"
      aria-hidden={!active}
      sx={{ display: active ? 'grid' : 'none', gap: 2.5, pt: 0.5 }}
    >
      {children}
    </Box>
  );
}
