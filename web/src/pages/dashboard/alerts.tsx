import { CONFIG } from 'src/global-config';

import { AlertsView } from 'src/sections/alerts/alerts-view';

// ----------------------------------------------------------------------

const metadata = { title: `告警 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <AlertsView />
    </>
  );
}
