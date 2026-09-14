import { useState, useEffect } from 'react';

import Box from '@mui/material/Box';
import Link from '@mui/material/Link';
import Menu from '@mui/material/Menu';
import Tooltip from '@mui/material/Tooltip';
import MenuItem from '@mui/material/MenuItem';
import IconButton from '@mui/material/IconButton';
import Typography from '@mui/material/Typography';

import { paths } from 'src/routes/paths';
import { RouterLink } from 'src/routes/components';

import { formatDuration } from 'src/utils/format';

import { REGION_LABELS } from 'src/constants/server';

import { Label } from 'src/components/label';
import { Iconify } from 'src/components/iconify';
import { FlagIcon } from 'src/components/flag-icon';

// ----------------------------------------------------------------------

type Props = {
  id: number;
  name: string;
  region: string;
  group: string;
  v4: boolean;
  v6: boolean;
  online: boolean;
  /** 服务端收到最后一条 metrics 的时刻（秒），null = 从没连过 */
  lastSeen: number | null;
};

/** 卡片顶部：国旗 + 名称 + 地区分组 + 协议栈标记；离线时右上角显示离线时长。 */
export function CardHeader({ id, name, region, group, v4, v6, online, lastSeen }: Props) {
  const subtitle = [formatRegion(region), group].filter(Boolean).join(' · ');
  const [menu, setMenu] = useState<HTMLElement | null>(null);

  return (
    <Box sx={{ gap: 1, display: 'flex', alignItems: 'flex-start' }}>
      {/* 固定宽度的槽位：FlagIcon 在国家码认不出来时返回 null，不占位会让标题跳一下 */}
      <Box sx={{ width: 24, flexShrink: 0, display: 'flex', justifyContent: 'center' }}>
        <FlagIcon code={region} sx={{ width: 24, height: 24 }} />
      </Box>

      <Box sx={{ minWidth: 0, flexGrow: 1 }}>
        {/* 标题就是进详情页的入口。步骤 06 时这条路由还不存在，那会儿留了个空位。 */}
        <Tooltip title={name} placement="top-start">
          <Link
            component={RouterLink}
            href={paths.dashboard.servers.details(id)}
            color="inherit"
            underline="hover"
            sx={{ display: 'block' }}
          >
            <Typography noWrap variant="subtitle2" sx={{ lineHeight: 1.4 }}>
              {name}
            </Typography>
          </Link>
        </Tooltip>

        {/* 地区也写成文字：国旗认不出来的国家码（或者没填地区）时，卡片上不能一点线索都没有 */}
        {subtitle && (
          <Typography noWrap variant="caption" sx={{ display: 'block', color: 'text.secondary' }}>
            {subtitle}
          </Typography>
        )}

        <Box sx={{ gap: 0.5, mt: 0.5, display: 'flex', alignItems: 'center' }}>
          {v4 && (
            <Label variant="soft" color="info">
              V4
            </Label>
          )}
          {v6 && (
            <Label variant="soft" color="success">
              V6
            </Label>
          )}
          {!v4 && !v6 && (
            <Label variant="soft" color="default">
              未探测
            </Label>
          )}
        </Box>
      </Box>

      <OfflineTag online={online} lastSeen={lastSeen} />
      <IconButton
        size="small"
        aria-label={`${name} 更多操作`}
        onClick={(event) => setMenu(event.currentTarget)}
      >
        <Iconify icon="eva:more-vertical-fill" />
      </IconButton>
      <Menu open={!!menu} anchorEl={menu} onClose={() => setMenu(null)}>
        <MenuItem
          component={RouterLink}
          href={paths.dashboard.proxy.detail(id)}
          onClick={() => setMenu(null)}
        >
          代理管理
        </MenuItem>
      </Menu>
    </Box>
  );
}

// ----------------------------------------------------------------------

/** 地区显示成「香港 HK」；认不出的国家码就原样显示，没填就是空串。 */
function formatRegion(region: string): string {
  if (!region) {
    return '';
  }
  const label = REGION_LABELS[region];
  return label ? `${label} ${region}` : region;
}

/**
 * 离线时长。从没连过的节点显示「未连接」而不是一个没有意义的时长。
 *
 * 时长靠定时器算，不在渲染里读 Date.now()：一来渲染期取当前时间是不纯的
 * （react-hooks/purity 会报），二来离线节点的快照不再变化，不自己走一下的话
 * 这个数字会永远停在掉线那一刻。文案只精确到分钟，30 秒一跳。
 */
function OfflineTag({ online, lastSeen }: { online: boolean; lastSeen: number | null }) {
  const [seconds, setSeconds] = useState(0);

  useEffect(() => {
    if (online || !lastSeen) {
      return undefined;
    }

    const update = () => setSeconds(Math.max(0, Math.floor(Date.now() / 1000) - lastSeen));
    update();

    const timer = setInterval(update, 30_000);
    return () => clearInterval(timer);
  }, [online, lastSeen]);

  if (online) {
    return null;
  }

  if (!lastSeen) {
    return (
      <Label variant="soft" color="default">
        未连接
      </Label>
    );
  }

  return (
    <Label variant="soft" color="error">
      {seconds < 60 ? '刚刚离线' : `离线 ${formatDuration(seconds)}`}
    </Label>
  );
}
