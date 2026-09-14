import { CONFIG } from 'src/global-config';

import { SubscribersListView } from 'src/sections/subscribers/list/subscribers-list-view';

// ----------------------------------------------------------------------

const metadata = { title: `订阅用户 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <SubscribersListView />
    </>
  );
}
