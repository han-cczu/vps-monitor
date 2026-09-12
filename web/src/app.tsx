import 'src/global.css';
import 'dayjs/locale/zh-cn';

import { useEffect } from 'react';

import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';

import { usePathname } from 'src/routes/hooks';

import { themeConfig, ThemeProvider } from 'src/theme';

import { Snackbar } from 'src/components/snackbar';
import { ProgressBar } from 'src/components/progress-bar';
import { MotionLazy } from 'src/components/animate/motion-lazy';
import { SettingsDrawer, defaultSettings, SettingsProvider } from 'src/components/settings';

import { AuthProvider } from 'src/auth/context/jwt';

// ----------------------------------------------------------------------

type AppProps = {
  children: React.ReactNode;
};

export default function App({ children }: AppProps) {
  useScrollToTop();

  return (
    <AuthProvider>
      <SettingsProvider defaultSettings={defaultSettings}>
        <ThemeProvider
          modeStorageKey={themeConfig.modeStorageKey}
          defaultMode={themeConfig.defaultMode}
        >
          {/* 日期选择器的上下文：starter 没挂，用到 <Field.DatePicker /> 的页面不挂就会直接抛错 */}
          <LocalizationProvider dateAdapter={AdapterDayjs} adapterLocale="zh-cn">
            <MotionLazy>
              <ProgressBar />
              {/* toast 宿主：starter 没有挂它，sections 里的 toast.success/error 需要 */}
              <Snackbar />
              <SettingsDrawer defaultSettings={defaultSettings} />
              {children}
            </MotionLazy>
          </LocalizationProvider>
        </ThemeProvider>
      </SettingsProvider>
    </AuthProvider>
  );
}

// ----------------------------------------------------------------------

function useScrollToTop() {
  const pathname = usePathname();

  useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);

  return null;
}
