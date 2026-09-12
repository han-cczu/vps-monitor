import type { AccountDrawerProps } from './components/account-drawer';

import { paths } from 'src/routes/paths';

import { Iconify } from 'src/components/iconify';

// ----------------------------------------------------------------------

export const _account: AccountDrawerProps['data'] = [
  {
    label: '账号设置',
    href: paths.dashboard.settings.account,
    icon: <Iconify icon="solar:settings-bold-duotone" />,
  },
];
