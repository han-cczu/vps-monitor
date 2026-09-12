import { CONFIG } from 'src/global-config';

import { BlankView } from 'src/sections/blank/view';

// ----------------------------------------------------------------------

const metadata = { title: `监控总览 - ${CONFIG.appName}` };

export default function Page() {
  return (
    <>
      <title>{metadata.title}</title>

      <BlankView title="监控总览" description="步骤 06 实现：服务器卡片网格、汇总条、筛选排序。" />
    </>
  );
}
