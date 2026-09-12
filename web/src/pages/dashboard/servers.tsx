import { CONFIG } from 'src/global-config';

import { ServersListView } from 'src/sections/servers/view';

// ----------------------------------------------------------------------

const metadata = { title: `节点 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <ServersListView />
    </>
  );
}
