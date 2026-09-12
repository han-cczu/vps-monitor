import { mergeClasses } from 'minimal-shared/utils';
import * as flags from 'country-flag-icons/react/3x2';

import { styled } from '@mui/material/styles';

import { flagIconClasses } from './classes';

// ----------------------------------------------------------------------

type FlagComponent = React.ComponentType<React.SVGProps<SVGSVGElement>>;

const flagComponents = flags as unknown as Record<string, FlagComponent | undefined>;

export type FlagIconProps = React.ComponentProps<typeof FlagRoot> & {
  /** ISO 3166-1 alpha-2，大小写都行 */
  code?: string;
};

/**
 * 国旗 SVG 来自本地的 country-flag-icons，不走外网。
 * 找不到对应国家码时返回 null，不占位。
 */
export function FlagIcon({ code, className, sx, ...other }: FlagIconProps) {
  const Flag = code ? flagComponents[code.toUpperCase()] : undefined;

  if (!Flag) {
    return null;
  }

  return (
    <FlagRoot className={mergeClasses([flagIconClasses.root, className])} sx={sx} {...other}>
      <Flag className={flagIconClasses.img} aria-label={code} />
    </FlagRoot>
  );
}

// ----------------------------------------------------------------------

const FlagRoot = styled('span')(({ theme }) => ({
  width: 26,
  height: 20,
  flexShrink: 0,
  overflow: 'hidden',
  borderRadius: '5px',
  alignItems: 'center',
  display: 'inline-flex',
  justifyContent: 'center',
  backgroundColor: theme.vars.palette.background.neutral,
  [`& .${flagIconClasses.img}`]: {
    width: '100%',
    height: '100%',
    maxWidth: 'unset',
    objectFit: 'cover',
  },
}));
