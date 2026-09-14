import type { BoxProps } from '@mui/material/Box';
import type { SettingsState } from '../types';

import Box from '@mui/material/Box';
import { alpha as hexAlpha } from '@mui/material/styles';

import { OptionButton } from './styles';

const presetLabels: Record<SettingsState['primaryColor'], string> = {
  default: '绿色',
  preset1: '天蓝色',
  preset2: '紫色',
  preset3: '蓝色',
  preset4: '橙色',
  preset5: '红色',
};

// ----------------------------------------------------------------------

export type PresetsOptionsProps = BoxProps & {
  icon: React.ReactNode;
  value: SettingsState['primaryColor'];
  options: { name: SettingsState['primaryColor']; value: string }[];
  onChangeOption: (newOption: SettingsState['primaryColor']) => void;
};

export function PresetsOptions({
  sx,
  icon,
  value,
  options,
  onChangeOption,
  ...other
}: PresetsOptionsProps) {
  return (
    <Box
      sx={[
        {
          gap: 1.5,
          display: 'grid',
          gridTemplateColumns: 'repeat(3, 1fr)',
        },
        ...(Array.isArray(sx) ? sx : [sx]),
      ]}
      {...other}
    >
      {options.map((option) => {
        const selected = value === option.name;

        return (
          <OptionButton
            key={option.name}
            aria-label={presetLabels[option.name]}
            title={presetLabels[option.name]}
            aria-pressed={selected}
            onClick={() => onChangeOption(option.name)}
            sx={{
              height: 64,
              color: option.value,
              ...(selected && {
                bgcolor: hexAlpha(option.value, 0.08),
              }),
            }}
          >
            {icon}
          </OptionButton>
        );
      })}
    </Box>
  );
}
