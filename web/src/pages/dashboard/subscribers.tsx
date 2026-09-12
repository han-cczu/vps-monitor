import { CONFIG } from 'src/global-config';

import { BlankView } from 'src/sections/blank/view';

// ----------------------------------------------------------------------

const metadata = { title: `订阅用户 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <BlankView title="订阅用户" description="步骤 15 实现：订阅用户、分配、Clash 订阅链接。" />
    </>
  );
}
