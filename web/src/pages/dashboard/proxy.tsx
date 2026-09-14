import { CONFIG } from 'src/global-config';

import { ProxyListView } from 'src/sections/proxy/list/proxy-list-view';

// ----------------------------------------------------------------------

const metadata = { title: `代理 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <ProxyListView />
    </>
  );
}
