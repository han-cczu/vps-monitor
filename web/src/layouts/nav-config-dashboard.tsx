import type { NavSectionProps } from 'src/components/nav-section';

import { paths } from 'src/routes/paths';

import { CONFIG } from 'src/global-config';

import { SvgColor } from 'src/components/svg-color';

// ----------------------------------------------------------------------

const icon = (name: string) => (
  <SvgColor src={`${CONFIG.assetsDir}/assets/icons/navbar/${name}.svg`} />
);

const ICONS = {
  overview: icon('ic-dashboard'),
  servers: icon('ic-analytics'),
  proxy: icon('ic-lock'),
  subscribers: icon('ic-user'),
  alerts: icon('ic-label'),
  settings: icon('ic-params'),
};

// ----------------------------------------------------------------------

/**
 * 侧栏导航。步骤 01 只放占位入口，页面在各自步骤里补（06 总览、03 节点、14 代理、
 * 15 订阅用户、19 告警、02/20 设置）。
 */
export const navData: NavSectionProps['data'] = [
  {
    subheader: '监控',
    items: [
      { title: '监控总览', path: paths.dashboard.overview, icon: ICONS.overview },
      { title: '节点', path: paths.dashboard.servers.root, icon: ICONS.servers },
    ],
  },
  {
    subheader: '代理',
    items: [
      { title: '代理', path: paths.dashboard.proxy.root, icon: ICONS.proxy },
      { title: '订阅用户', path: paths.dashboard.subscribers.root, icon: ICONS.subscribers },
    ],
  },
  {
    subheader: '系统',
    items: [
      { title: '告警', path: paths.dashboard.alerts.root, icon: ICONS.alerts },
      { title: '设置', path: paths.dashboard.settings.root, icon: ICONS.settings },
    ],
  },
];
