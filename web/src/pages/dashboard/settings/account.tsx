import { CONFIG } from 'src/global-config';

import { AccountLayout } from 'src/sections/account/account-layout';
import { AccountChangePassword } from 'src/sections/account/account-change-password';

// ----------------------------------------------------------------------

const metadata = { title: `账号设置 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <AccountLayout>
        <AccountChangePassword />
      </AccountLayout>
    </>
  );
}
