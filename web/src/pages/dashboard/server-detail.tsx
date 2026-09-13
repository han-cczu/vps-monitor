import { CONFIG } from 'src/global-config';

import { ServerDetailView } from 'src/sections/servers/view';

// ----------------------------------------------------------------------

const metadata = { title: `节点详情 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <ServerDetailView />
    </>
  );
}
