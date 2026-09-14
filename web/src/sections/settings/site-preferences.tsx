import { Fragment } from 'react';

import { useSiteSettings } from 'src/api/settings';
// Re-render the current page when units/timezone change; no metric values are rewritten.
export function SitePreferences({ children }: { children: React.ReactNode }) {
  const { data } = useSiteSettings();
  return (
    <Fragment key={`${data?.['site.bytes_base'] ?? 1000}:${data?.['site.tz'] ?? ''}`}>
      {children}
    </Fragment>
  );
}
