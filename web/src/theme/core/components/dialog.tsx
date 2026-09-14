import type { Theme, Components } from '@mui/material/styles';

// ----------------------------------------------------------------------

const MuiDialog: Components<Theme>['MuiDialog'] = {
  // ▼▼▼▼▼▼▼▼ 🎨 STYLE ▼▼▼▼▼▼▼▼
  styleOverrides: {
    paper: {
      variants: [
        {
          props: (props) => !props.fullScreen,
          style: ({ theme }) => ({
            margin: theme.spacing(2),
            boxShadow: theme.vars.customShadows.dialog,
            borderRadius: Number(theme.shape.borderRadius) * 2,
            [theme.breakpoints.down('sm')]: {
              margin: 0,
              width: '100%',
              maxWidth: '100%',
              height: '100%',
              maxHeight: '100%',
              borderRadius: 0,
            },
          }),
        },
      ],
    },
  },
};

const MuiDialogTitle: Components<Theme>['MuiDialogTitle'] = {
  // ▼▼▼▼▼▼▼▼ 🎨 STYLE ▼▼▼▼▼▼▼▼
  styleOverrides: {
    root: ({ theme }) => ({
      padding: theme.spacing(3),
    }),
  },
};

const MuiDialogContent: Components<Theme>['MuiDialogContent'] = {
  // ▼▼▼▼▼▼▼▼ 🎨 STYLE ▼▼▼▼▼▼▼▼
  styleOverrides: {
    root: ({ theme }) => ({
      padding: theme.spacing(0, 3),
    }),
    dividers: ({ theme }) => ({
      borderTop: 0,
      borderBottomStyle: 'dashed',
      paddingBottom: theme.spacing(3),
    }),
  },
};

const MuiDialogActions: Components<Theme>['MuiDialogActions'] = {
  // ▼▼▼▼▼▼▼▼ ⚙️ PROPS ▼▼▼▼▼▼▼▼
  defaultProps: {
    disableSpacing: true,
  },
  // ▼▼▼▼▼▼▼▼ 🎨 STYLE ▼▼▼▼▼▼▼▼
  styleOverrides: {
    root: ({ theme }) => ({
      padding: theme.spacing(3),
      '& > :not(:first-of-type)': {
        marginLeft: theme.spacing(1.5),
      },
    }),
  },
};

/* **********************************************************************
 * 🚀 Export
 * **********************************************************************/
export const dialog: Components<Theme> = {
  MuiDialog,
  MuiDialogTitle,
  MuiDialogContent,
  MuiDialogActions,
};
