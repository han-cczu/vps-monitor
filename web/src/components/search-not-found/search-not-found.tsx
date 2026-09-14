import type { BoxProps } from '@mui/material/Box';
import type { Theme, SxProps } from '@mui/material/styles';
import type { TypographyProps } from '@mui/material/Typography';

import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';

// ----------------------------------------------------------------------

type SearchNotFoundProps = BoxProps & {
  query?: string;
  sx?: SxProps<Theme>;
  slotProps?: {
    title?: TypographyProps;
    description?: TypographyProps;
  };
};

export function SearchNotFound({ query, sx, slotProps, ...other }: SearchNotFoundProps) {
  if (!query) {
    return (
      <Typography variant="body2" {...slotProps?.description}>
        请输入关键词
      </Typography>
    );
  }

  return (
    <Box
      sx={[
        {
          gap: 1,
          display: 'flex',
          borderRadius: 1.5,
          textAlign: 'center',
          flexDirection: 'column',
        },
        ...(Array.isArray(sx) ? sx : [sx]),
      ]}
      {...other}
    >
      <Typography
        variant="h6"
        {...slotProps?.title}
        sx={[
          { color: 'text.primary' },
          ...(Array.isArray(slotProps?.title?.sx) ? slotProps.title.sx : [slotProps?.title?.sx]),
        ]}
      >
        未找到结果
      </Typography>

      <Typography variant="body2" {...slotProps?.description}>
        没有找到与<strong>{`“${query}”`}</strong>相关的结果。
        <br /> 请检查输入内容，或换一个关键词。
      </Typography>
    </Box>
  );
}
