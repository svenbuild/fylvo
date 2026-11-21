import { alpha, createTheme } from "@mui/material/styles";
import type { Theme, ThemeOptions } from "@mui/material/styles";

// The Fylvo look, the same as the web app (frontend/src/fylvoTheme.ts and
// inventory/fylvo_theme.json): neutral near-black, flat, compact, accent #7484F4, Inter.

const FONT = '"Inter Variable", Inter, "Segoe UI", system-ui, sans-serif';
const RADIUS = 8;

const isDark = (theme: Theme) => theme.palette.mode === "dark";

// Frame around the page (sidebar, popup header).
export const shellSurface = (theme: Theme) => (isDark(theme) ? "#000000" : theme.palette.grey[100]);
// Cards and quiet fills one step above the page.
export const quietSurface = (theme: Theme) => (isDark(theme) ? "#141416" : theme.palette.grey[100]);
// Menus, dialogs and popovers.
const raisedSurface = (theme: Theme) => (isDark(theme) ? "#141416" : "#ffffff");
const hoverSurface = (theme: Theme) => (isDark(theme) ? "#141416" : alpha("#000000", 0.04));
const selectedSurface = (theme: Theme) => (isDark(theme) ? "#1e1e22" : alpha("#000000", 0.07));

const palettes = {
  light: {
    primary: { main: "#5565E8", light: "#7484F4", dark: "#3F4FCB", contrastText: "#FFFFFF" },
    secondary: { main: "#7C4DDB", light: "#A78BFA", dark: "#5B34B0" },
  },
  dark: {
    primary: { main: "#7484F4", light: "#A3AEFA", dark: "#5565E8", contrastText: "#FFFFFF" },
    secondary: { main: "#A78BFA", light: "#C9B8FD", dark: "#7C4DDB" },
    background: { default: "#0A0A0A", paper: "#0A0A0A" },
    text: { primary: "#F4F4F5", secondary: "#A9A9B0" },
    divider: "rgba(255, 255, 255, 0.09)",
    grey: { 900: "#141416", 800: "#1E1E22", 700: "#26262B" },
  },
};

const floating = (theme: Theme) => ({
  backgroundColor: raisedSurface(theme),
  backgroundImage: "none",
  border: `1px solid ${theme.palette.divider}`,
  boxShadow: isDark(theme) ? "0 12px 32px rgba(0, 0, 0, 0.6)" : "0 12px 32px rgba(0, 0, 0, 0.12)",
});

const fylvoOptions: ThemeOptions = {
  shape: { borderRadius: RADIUS },
  typography: {
    fontFamily: FONT,
    fontSize: 13,
    h4: { fontSize: "1.25rem", fontWeight: 600, letterSpacing: "-0.01em" },
    h5: { fontSize: "1.125rem", fontWeight: 600, letterSpacing: "-0.01em" },
    h6: { fontSize: "0.9375rem", fontWeight: 600 },
    subtitle1: { fontSize: "0.875rem", fontWeight: 500 },
    subtitle2: { fontSize: "0.8125rem", fontWeight: 600 },
    body1: { fontSize: "0.8125rem" },
    body2: { fontSize: "0.8125rem" },
    button: { fontSize: "0.8125rem", fontWeight: 500, textTransform: "none" },
    caption: { fontSize: "0.75rem" },
    overline: { fontSize: "0.6875rem", fontWeight: 600, letterSpacing: "0.06em" },
  },
  components: {
    MuiCssBaseline: {
      styleOverrides: {
        body: {
          overscrollBehavior: "none",
          backgroundColor: "initial",
          fontFeatureSettings: '"cv11", "ss01"',
        },
        img: {
          userSelect: "none",
          pointerEvents: "none",
          WebkitUserDrag: "none",
        },
      },
    },
    MuiTypography: {
      styleOverrides: { root: { userSelect: "none" } },
    },
    MuiTooltip: {
      defaultProps: { enterDelay: 500 },
      styleOverrides: {
        tooltip: ({ theme }) => ({
          fontSize: "0.75rem",
          fontWeight: 500,
          padding: "4px 8px",
          borderRadius: 6,
          backgroundColor: isDark(theme) ? "#26262b" : "#1c1c20",
          color: "#ffffff",
        }),
      },
    },
    MuiButtonBase: {
      defaultProps: { disableRipple: true },
      styleOverrides: {
        // Ripples are off, so keyboard focus gets its own ring.
        root: ({ theme }) => ({
          "&.Mui-focusVisible": { outline: `2px solid ${alpha(theme.palette.primary.main, 0.8)}`, outlineOffset: 1 },
        }),
      },
    },
    MuiButton: {
      defaultProps: { disableElevation: true },
      styleOverrides: {
        root: { borderRadius: RADIUS, minHeight: 30, padding: "3px 10px", lineHeight: 1.4 },
        sizeLarge: { minHeight: 36, padding: "6px 14px" },
        outlined: ({ theme }) => ({ borderColor: theme.palette.divider }),
      },
    },
    MuiIconButton: {
      styleOverrides: { root: { borderRadius: RADIUS } },
    },
    MuiToggleButton: {
      styleOverrides: { root: { textTransform: "none" } },
    },
    MuiPaper: {
      styleOverrides: {
        root: { backgroundImage: "none" },
        outlined: ({ theme }) => ({ borderColor: theme.palette.divider }),
      },
    },
    MuiCard: {
      styleOverrides: {
        root: ({ theme }) => ({
          backgroundImage: "none",
          boxShadow: "none",
          backgroundColor: isDark(theme) ? "#0A0A0A" : "#ffffff",
          border: `1px solid ${theme.palette.divider}`,
        }),
      },
    },
    MuiListItemButton: {
      styleOverrides: {
        root: ({ theme }) => ({
          borderRadius: RADIUS,
          "@media (hover: hover)": { "&:hover": { backgroundColor: hoverSurface(theme) } },
          "&.Mui-selected, &.Mui-selected:hover": { backgroundColor: selectedSurface(theme) },
        }),
      },
    },
    MuiListItemIcon: {
      styleOverrides: { root: { minWidth: 32 } },
    },
    MuiTab: {
      styleOverrides: { root: { textTransform: "none" } },
    },
    MuiSkeleton: {
      defaultProps: { animation: "wave" },
    },
    MuiMenu: {
      styleOverrides: {
        paper: ({ theme }) => ({ ...floating(theme), borderRadius: 10 }),
        list: { padding: "4px 0" },
      },
    },
    MuiMenuItem: {
      styleOverrides: {
        root: ({ theme }) => ({
          borderRadius: 6,
          margin: "0px 4px",
          minHeight: 30,
          paddingLeft: 8,
          paddingRight: 8,
          fontSize: "0.8125rem",
          "@media (min-width: 600px)": { minHeight: 30 },
          "&.Mui-selected, &.Mui-selected:hover": {
            backgroundColor: isDark(theme) ? "#26262b" : selectedSurface(theme),
          },
        }),
      },
    },
    MuiPopover: {
      styleOverrides: { paper: ({ theme }) => ({ ...floating(theme), borderRadius: 10 }) },
    },
    MuiDialog: {
      styleOverrides: { paper: ({ theme }) => ({ ...floating(theme), borderRadius: 12 }) },
    },
    MuiDialogTitle: {
      styleOverrides: { root: { fontSize: "0.9375rem", fontWeight: 600, padding: "14px 18px" } },
    },
    MuiDialogContent: {
      styleOverrides: { root: { padding: "0 18px 14px" } },
    },
    MuiDialogActions: {
      styleOverrides: { root: { padding: "10px 14px" } },
    },
    MuiOutlinedInput: {
      styleOverrides: {
        root: ({ theme }) => ({
          borderRadius: RADIUS,
          "& .MuiOutlinedInput-notchedOutline": { borderColor: theme.palette.divider },
        }),
      },
    },
    MuiFilledInput: {
      styleOverrides: {
        root: ({ theme }) => ({
          borderRadius: RADIUS,
          // One step above the window, which may be the Windows backdrop rather than the page.
          backgroundColor: isDark(theme) ? "#1e1e22" : theme.palette.grey[100],
          "&:hover": { backgroundColor: isDark(theme) ? "#26262b" : theme.palette.grey[200] },
          "&.Mui-focused": { backgroundColor: isDark(theme) ? "#1e1e22" : theme.palette.grey[100] },
          "&::before, &::after": { borderBottom: "none" },
          "&:hover:not(.Mui-disabled, .Mui-error):before": { borderBottom: "none" },
        }),
      },
    },
    MuiAlert: {
      styleOverrides: {
        root: { borderRadius: RADIUS, fontSize: "0.8125rem" },
      },
    },
    MuiLinearProgress: {
      styleOverrides: {
        root: ({ theme }) => ({
          borderRadius: 4,
          backgroundColor: isDark(theme) ? "#26262b" : theme.palette.grey[200],
        }),
      },
    },
    MuiChip: {
      styleOverrides: { root: { borderRadius: 6 } },
    },
    MuiDivider: {
      styleOverrides: { root: ({ theme }) => ({ borderColor: theme.palette.divider }) },
    },
  },
};

export const createAppTheme = (mode: "light" | "dark" = "light") =>
  createTheme({ ...fylvoOptions, palette: { mode, ...palettes[mode] } });
