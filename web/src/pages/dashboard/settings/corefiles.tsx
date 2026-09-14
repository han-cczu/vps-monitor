import { CONFIG } from 'src/global-config';

import { CoreFilesView } from 'src/sections/settings/corefiles/corefiles-view';

export default function Page() {
  return (
    <>
      <title>{`代理核心 - ${CONFIG.appName}`}</title>
      <CoreFilesView />
    </>
  );
}
