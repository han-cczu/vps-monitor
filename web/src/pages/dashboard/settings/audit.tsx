import { AuditView } from 'src/sections/settings/audit-view';
import { AccountLayout } from 'src/sections/account/account-layout';

export default function Page() {
  return (
    <AccountLayout>
      <AuditView />
    </AccountLayout>
  );
}
