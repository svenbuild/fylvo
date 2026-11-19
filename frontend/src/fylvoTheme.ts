import { alpha, Theme } from "@mui/material";
import { ThemeOptions } from "@mui/material/styles/createTheme";
import { deepmerge } from "@mui/utils";

// The Fylvo look: compact, flat and calm. Neutral surfaces separated by hairlines
// instead of shadows, small type, small controls. Colors come from the server
// setting "theme_options", everything about size and shape lives here.

const FONT = '"Inter Variable", Inter, "Segoe UI", system-ui, -apple-system, sans-serif';
const RADIUS = 8;
// Fingers need bigger targets than a mouse pointer.
const TOUCH = "@media (pointer: coarse)";

const isDark = (theme: Theme) => theme.palette.mode === "dark";
const hairline = (theme: Theme) => `1px solid ${theme.palette.divider}`;
// Menus, dialogs and popovers sit one step above the page.
const raised = (theme: Theme) => (isDark(theme) ? "#141416" : "#ffffff");
const floating = (theme: Theme) => ({
  backgroundColor: raised(theme),
  backgroundImage: "none",
  border: hairline(theme),
  boxShadow: isDark(theme) ? "0 12px 32px rgba(0, 0, 0, 0.6)" : "0 12px 32px rgba(0, 0, 0, 0.12)",
  // Rows on a raised surface go one step further up, otherwise hover has the color of the surface.
  "& .MuiListItemButton-root:hover, & .MuiTableRow-root.MuiTableRow-hover:hover": { backgroundColor: raisedHoverSurface(theme) },
  "& .MuiListItemButton-root, & .MuiTableRow-root": neutralSelected(theme, raisedSelectedSurface(theme)),
});

const fylvoOptions: ThemeOptions = {
  shape: { borderRadius: RADIUS },
  spacing: 6,
  mixins: { toolbar: { minHeight: 48 } },
  typography: {
    fontFamily: FONT,
    fontSize: 13,
    h1: { fontSize: "1.75rem", fontWeight: 600, letterSpacing: "-0.02em" },
    h2: { fontSize: "1.5rem", fontWeight: 600, letterSpacing: "-0.02em" },
    h3: { fontSize: "1.3rem", fontWeight: 600, letterSpacing: "-0.01em" },
    h4: { fontSize: "1.15rem", fontWeight: 600, letterSpacing: "-0.01em" },
    h5: { fontSize: "1.05rem", fontWeight: 600 },
    h6: { fontSize: "0.9375rem", fontWeight: 600 },
    subtitle1: { fontSize: "0.875rem", fontWeight: 500 },
    subtitle2: { fontSize: "0.8125rem", fontWeight: 600 },
    body1: { fontSize: "0.8125rem" },
    body2: { fontSize: "0.8125rem" },
    button: { fontSize: "0.8125rem", fontWeight: 500 },
    caption: { fontSize: "0.75rem" },
    overline: { fontSize: "0.6875rem", fontWeight: 600, letterSpacing: "0.06em" },
  },
  components: {
    MuiCssBaseline: {
      styleOverrides: {
        body: { fontFeatureSettings: '"cv11", "ss01"' },
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
      defaultProps: { size: "small", disableElevation: true },
      styleOverrides: {
        root: { borderRadius: RADIUS, minHeight: 30, padding: "3px 10px", lineHeight: 1.4, [TOUCH]: { minHeight: 40 } },
        sizeLarge: { minHeight: 34, padding: "5px 14px" },
        outlined: ({ theme }) => ({ borderColor: theme.palette.divider }),
        startIcon: { marginRight: 6, "& > *:nth-of-type(1)": { fontSize: 16 } },
        endIcon: { marginLeft: 6, "& > *:nth-of-type(1)": { fontSize: 16 } },
      },
    },
    MuiIconButton: {
      defaultProps: { size: "small" },
      styleOverrides: {
        root: { borderRadius: RADIUS },
        sizeSmall: { padding: 5 },
        sizeMedium: { padding: 6 },
        sizeLarge: { padding: 7 },
      },
    },
    MuiSvgIcon: {
      styleOverrides: {
        fontSizeMedium: { fontSize: "1.125rem" },
        fontSizeSmall: { fontSize: "1rem" },
        fontSizeLarge: { fontSize: "1.5rem" },
      },
    },
    MuiToggleButton: {
      defaultProps: { size: "small" },
      styleOverrides: {
        root: ({ theme }) => ({ borderColor: theme.palette.divider, padding: "4px 8px" }),
      },
    },
    MuiPaper: {
      styleOverrides: {
        root: { backgroundImage: "none" },
        outlined: ({ theme }) => ({ borderColor: theme.palette.divider }),
      },
    },
    MuiAppBar: {
      styleOverrides: { root: { boxShadow: "none", backgroundImage: "none" } },
    },
    MuiPopover: {
      styleOverrides: { paper: ({ theme }) => ({ ...floating(theme), borderRadius: 10 }) },
    },
    MuiMenu: {
      styleOverrides: {
        paper: ({ theme }) => ({ ...floating(theme), borderRadius: "10px" }),
        list: { padding: "4px 0" },
      },
    },
    MuiMenuItem: {
      styleOverrides: {
        root: ({ theme }) => ({
          // Menus are raised, so their items use the raised hover and selected steps.
          "&:hover": { backgroundColor: raisedHoverSurface(theme) },
          ...neutralSelected(theme, raisedSelectedSurface(theme)),
          // Same focus mark in every menu: neutral fill and a thin ring inside the item.
          "&.Mui-focusVisible": {
            outline: `1px solid ${theme.palette.primary.main}`,
            outlineOffset: -1,
            backgroundColor: raisedHoverSurface(theme),
          },
          borderRadius: "6px",
          margin: "0px 4px",
          minHeight: 30,
          paddingTop: 4,
          paddingBottom: 4,
          paddingLeft: "8px",
          paddingRight: "8px",
          fontSize: "0.8125rem",
          "@media (min-width: 600px)": { minHeight: 30 },
          [TOUCH]: { minHeight: 40 },
        }),
      },
    },
    MuiListItemButton: {
      styleOverrides: {
        root: ({ theme }) => ({
          ...neutralSelected(theme),
          borderRadius: RADIUS,
          paddingTop: 4,
          paddingBottom: 4,
          [TOUCH]: { minHeight: 40 },
        }),
      },
    },
    MuiListItemIcon: {
      styleOverrides: { root: { minWidth: 30 } },
    },
    MuiListItemText: {
      styleOverrides: { primary: { fontSize: "0.8125rem" }, secondary: { fontSize: "0.75rem" } },
    },
    MuiListSubheader: {
      styleOverrides: { root: { lineHeight: "30px", fontSize: "0.75rem", backgroundColor: "transparent" } },
    },
    MuiDialog: {
      styleOverrides: {
        paper: ({ theme }) => ({ ...floating(theme), borderRadius: 12 }),
      },
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
    MuiBackdrop: {
      styleOverrides: {
        root: { "&:not(.MuiBackdrop-invisible)": { backgroundColor: "rgba(0, 0, 0, 0.6)" } },
      },
    },
    MuiTextField: { defaultProps: { size: "small" } },
    MuiFormControl: { defaultProps: { size: "small" } },
    MuiSelect: {
      defaultProps: { size: "small" },
      // A value shown with the menu item's own text block must not make the field taller.
      styleOverrides: { select: { "& .MuiListItemText-root": { marginTop: 0, marginBottom: 0 } } },
    },
    MuiAutocomplete: {
      defaultProps: { size: "small" },
      styleOverrides: {
        paper: ({ theme }) => floating(theme),
        option: ({ theme }) => ({
          '&[aria-selected="true"], &[aria-selected="true"].Mui-focused': {
            backgroundColor: selectedSurface(theme),
          },
        }),
      },
    },
    MuiInputBase: {
      styleOverrides: { root: { fontSize: "0.8125rem" } },
    },
    MuiInputLabel: {
      styleOverrides: {
        root: { fontSize: "0.8125rem" },
        // Floating labels stay readable (11 px instead of under 10 px).
        shrink: {
          "&.MuiInputLabel-outlined": { transform: "translate(14px, -10px) scale(0.85)", maxWidth: "calc(117% - 32px)" },
          "&.MuiInputLabel-filled": { transform: "translate(12px, 6px) scale(0.85)" },
          "&.MuiInputLabel-filled.MuiInputLabel-sizeSmall": { transform: "translate(12px, 3px) scale(0.85)" },
        },
      },
    },
    MuiOutlinedInput: {
      styleOverrides: {
        root: ({ theme }) => ({
          borderRadius: RADIUS,
          "& .MuiOutlinedInput-notchedOutline": { borderColor: theme.palette.divider },
          // A disabled field must not stand out more than an active one.
          "&.Mui-disabled .MuiOutlinedInput-notchedOutline": { borderColor: theme.palette.divider },
          [TOUCH]: { minHeight: 40 },
        }),
        // 32 px high on desktop.
        inputSizeSmall: { paddingTop: 6.5, paddingBottom: 6.5 },
        // The gap in the border follows the larger floating label.
        notchedOutline: { "& legend": { fontSize: "0.85em" } },
      },
    },
    MuiFilledInput: {
      styleOverrides: { root: { borderRadius: RADIUS } },
    },
    MuiFormHelperText: {
      styleOverrides: { root: { fontSize: "0.75rem", marginLeft: 2 } },
    },
    MuiFormControlLabel: {
      styleOverrides: { label: { fontSize: "0.8125rem" } },
    },
    MuiCheckbox: { defaultProps: { size: "small" } },
    MuiRadio: { defaultProps: { size: "small" } },
    MuiSwitch: { defaultProps: { size: "small" } },
    MuiSlider: { defaultProps: { size: "small" } },
    MuiChip: {
      defaultProps: { size: "small" },
      styleOverrides: { root: { borderRadius: 6, fontSize: "0.75rem" } },
    },
    MuiTabs: {
      styleOverrides: { root: { minHeight: 36 } },
    },
    MuiTab: {
      styleOverrides: {
        root: { minHeight: 36, padding: "6px 12px", fontSize: "0.8125rem", minWidth: 0 },
      },
    },
    MuiTableRow: {
      styleOverrides: {
        root: ({ theme }) => ({
          "&.MuiTableRow-hover:hover": { backgroundColor: hoverSurface(theme) },
          ...neutralSelected(theme),
        }),
      },
    },
    MuiTableCell: {
      styleOverrides: {
        root: ({ theme }) => ({
          padding: "6px 12px",
          fontSize: "0.8125rem",
          borderBottom: hairline(theme),
        }),
        head: ({ theme }) => ({ fontSize: "0.75rem", fontWeight: 600, color: theme.palette.text.secondary }),
      },
    },
    MuiTooltip: {
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
    MuiAlert: {
      styleOverrides: {
        root: { borderRadius: RADIUS, padding: "2px 12px", fontSize: "0.8125rem" },
      },
    },
    MuiCard: {
      styleOverrides: {
        root: ({ theme }) => ({ backgroundImage: "none", boxShadow: "none", border: hairline(theme) }),
      },
    },
    MuiCardContent: {
      styleOverrides: { root: { padding: 14, "&:last-child": { paddingBottom: 14 } } },
    },
    MuiAccordion: {
      styleOverrides: {
        root: ({ theme }) => ({ boxShadow: "none", border: hairline(theme), backgroundImage: "none" }),
      },
    },
    MuiPagination: { defaultProps: { size: "small" } },
    MuiPaginationItem: {
      styleOverrides: {
        root: ({ theme }) => ({ borderRadius: RADIUS, ...neutralSelected(theme) }),
        ellipsis: { display: "inline-flex", alignItems: "center", justifyContent: "center" },
      },
    },
    MuiBreadcrumbs: {
      styleOverrides: { root: { fontSize: "0.8125rem" } },
    },
    MuiLinearProgress: {
      styleOverrides: { root: { borderRadius: 4 } },
    },
    MuiSnackbarContent: {
      styleOverrides: { root: { fontSize: "0.8125rem", borderRadius: RADIUS } },
    },
    MuiDivider: {
      styleOverrides: {
        root: ({ theme }) => ({ borderColor: theme.palette.divider }),
      },
    },
    MuiFab: {
      styleOverrides: { root: { boxShadow: "none" } },
    },
    MuiBadge: {
      styleOverrides: { badge: { fontSize: "0.6875rem", minWidth: 16, height: 16, padding: "0 4px" } },
    },
  },
};

// Selected rows and items use a neutral tint, the accent color stays rare.
const neutralSelected = (theme: Theme, color = selectedSurface(theme)) => ({
  "&.Mui-selected, &.Mui-selected:hover, &.Mui-selected.Mui-focusVisible": {
    backgroundColor: color,
  },
});
export const selectedSurface = (theme: Theme) =>
  isDark(theme) ? "#1e1e22" : alpha(theme.palette.common.black, 0.07);
export const hoverSurface = (theme: Theme) =>
  isDark(theme) ? "#141416" : alpha(theme.palette.common.black, 0.04);

// Hover and selected on a raised surface (menu, popover, dialog). In dark mode the normal hover
// has the color of the raised surface itself, so these go one step further up.
export const raisedHoverSurface = (theme: Theme) => (isDark(theme) ? "#1e1e22" : hoverSurface(theme));
export const raisedSelectedSurface = (theme: Theme) => (isDark(theme) ? "#26262b" : selectedSurface(theme));

// The frame around the page (sidebar and top bar).
export const shellSurface = (theme: Theme) => (isDark(theme) ? "#000000" : theme.palette.grey[100]);

export const withFylvoLook = (options: ThemeOptions): ThemeOptions => deepmerge(options, fylvoOptions);
