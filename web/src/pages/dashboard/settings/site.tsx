import { SiteView } from 'src/sections/settings/site-view';
import { AccountLayout } from 'src/sections/account/account-layout';

export default function Page() {
  return (
    <AccountLayout>
      <SiteView />
    </AccountLayout>
  );
}
