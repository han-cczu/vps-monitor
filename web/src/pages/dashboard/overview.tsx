import { CONFIG } from 'src/global-config';

import { OverviewView } from 'src/sections/monitor/view';

// ----------------------------------------------------------------------

const metadata = { title: `监控总览 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <OverviewView />
    </>
  );
}
