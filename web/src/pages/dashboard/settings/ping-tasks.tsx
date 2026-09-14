import { CONFIG } from 'src/global-config';

import { AccountLayout } from 'src/sections/account/account-layout';
import { PingTasksView } from 'src/sections/settings/ping-tasks/ping-tasks-view';

export default function Page() {
  return (
    <>
      <title>{`Ping 任务 - ${CONFIG.appName}`}</title>
      <AccountLayout>
        <PingTasksView />
      </AccountLayout>
    </>
  );
}
