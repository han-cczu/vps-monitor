import { memo } from 'react';

import Box from '@mui/material/Box';
import Switch from '@mui/material/Switch';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import InputAdornment from '@mui/material/InputAdornment';
import FormControlLabel from '@mui/material/FormControlLabel';

import { Iconify } from 'src/components/iconify';

// ----------------------------------------------------------------------

/** 排序方式。默认 `sort` 跟随节点管理页里手工排的顺序。 */
export type SortKey = 'sort' | 'name' | 'cpu' | 'mem' | 'traffic';

export const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: 'sort', label: '默认顺序' },
  { value: 'name', label: '名称' },
  { value: 'cpu', label: 'CPU 使用率' },
  { value: 'mem', label: '内存使用率' },
  { value: 'traffic', label: '出站流量' },
];

export type Filters = {
  keyword: string;
  group: string;
  tag: string;
  sort: SortKey;
  showOffline: boolean;
};

export const DEFAULT_FILTERS: Filters = {
  keyword: '',
  group: '',
  tag: '',
  sort: 'sort',
  showOffline: true,
};

type Props = {
  filters: Filters;
  /** 可选项由当前快照里的节点算出来，节点改了分组会自动跟着变 */
  groups: string[];
  tags: string[];
  onChange: (patch: Partial<Filters>) => void;
};

/**
 * 筛选 / 排序 / 搜索。
 *
 * 套 memo：按 CPU 排序时可见顺序每秒都在变，外层 view 会跟着每秒重渲染一次，
 * 没必要把这一排 4 个输入控件和十几个菜单项也带着重渲染。
 */
export const Toolbar = memo(function Toolbar({ filters, groups, tags, onChange }: Props) {
  return (
    <Box
      sx={{
        gap: 2,
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
      }}
    >
      <TextField
        size="small"
        value={filters.keyword}
        onChange={(event) => onChange({ keyword: event.target.value })}
        placeholder="搜索名称 / 地区 / 标签"
        sx={{ width: { xs: 1, sm: 260 } }}
        slotProps={{
          // 搜索框没有可见 label，可访问名不能只靠 placeholder 兜底
          htmlInput: { 'aria-label': '搜索节点' },
          input: {
            startAdornment: (
              <InputAdornment position="start">
                <Iconify icon="eva:search-fill" width={18} sx={{ color: 'text.disabled' }} />
              </InputAdornment>
            ),
          },
        }}
      />

      <TextField
        select
        size="small"
        label="分组"
        value={filters.group}
        onChange={(event) => onChange({ group: event.target.value })}
        sx={{ minWidth: 120 }}
      >
        <MenuItem value="">全部</MenuItem>
        {groups.map((group) => (
          <MenuItem key={group} value={group}>
            {group}
          </MenuItem>
        ))}
      </TextField>

      <TextField
        select
        size="small"
        label="标签"
        value={filters.tag}
        onChange={(event) => onChange({ tag: event.target.value })}
        sx={{ minWidth: 120 }}
      >
        <MenuItem value="">全部</MenuItem>
        {tags.map((tag) => (
          <MenuItem key={tag} value={tag}>
            {tag}
          </MenuItem>
        ))}
      </TextField>

      <TextField
        select
        size="small"
        label="排序"
        value={filters.sort}
        onChange={(event) => onChange({ sort: event.target.value as SortKey })}
        sx={{ minWidth: 140 }}
      >
        {SORT_OPTIONS.map((option) => (
          <MenuItem key={option.value} value={option.value}>
            {option.label}
          </MenuItem>
        ))}
      </TextField>

      <FormControlLabel
        label="显示离线"
        sx={{ ml: { sm: 'auto' } }}
        control={
          <Switch
            checked={filters.showOffline}
            onChange={(event) => onChange({ showOffline: event.target.checked })}
          />
        }
      />
    </Box>
  );
});
