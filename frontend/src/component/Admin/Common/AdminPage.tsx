import { Popover, styled } from "@mui/material";
import type { BoxProps, Theme } from "@mui/material";
import { hoverSurface } from "../../../fylvoTheme.ts";
import PageContainer from "../../Pages/PageContainer.tsx";

const touch = "@media (max-width: 599.95px), (pointer: coarse)";

export const adminControlSx = (theme: Theme) => ({
  "& .MuiOutlinedInput-root:not(.MuiInputBase-multiline)": { minHeight: 32 },
  "& .MuiOutlinedInput-input:not(.MuiInputBase-inputMultiline):not(.MuiAutocomplete-input)": {
    paddingBlock: 6,
  },
  "& .MuiFormHelperText-root": { marginTop: 3, lineHeight: 1.45 },
  "& .MuiButtonBase-root.Mui-focusVisible": {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: -2,
  },
  [touch]: {
    "& .MuiButton-root, & .MuiOutlinedInput-root:not(.MuiInputBase-multiline)": { minHeight: 40 },
  },
});

const AdminPage = styled((props: BoxProps) => <PageContainer {...props} compact contentWidth={1440} />)(({ theme }) => ({
  ...adminControlSx(theme),
  minWidth: 0,
  "& > .MuiContainer-root": {
    marginInlineStart: 0,
  },
  "& > .MuiContainer-root > .MuiStack-root": {
    flexWrap: "wrap",
    rowGap: theme.spacing(1),
    marginBottom: theme.spacing(1),
  },
  "& > .MuiContainer-root > .MuiTableContainer-root": {
    marginTop: theme.spacing(1),
  },
  "& .MuiTableCell-root": {
    padding: "3px 8px",
    lineHeight: 1.35,
  },
  "& .MuiTableCell-stickyHeader": { backgroundColor: theme.palette.background.paper },
  "& .MuiTableCell-paddingCheckbox": {
    paddingBlock: 0,
    paddingInline: 2,
    "& .MuiCheckbox-root": { padding: 5 },
  },
  "& .MuiTableRow-root": { height: 33 },
  "& .MuiTableCell-root .MuiIconButton-root": {
    padding: 4,
    color: theme.palette.text.secondary,
    "&:hover": { backgroundColor: hoverSurface(theme), color: theme.palette.text.primary },
  },
  "& .MuiTableRow-root:last-of-type > .MuiTableCell-body": { borderBottom: 0 },
  // Tighter than on the user pages, so the eleven settings tabs fit a 1536 px wide window.
  "& .MuiTabs-root .MuiTabs-flexContainer": { gap: theme.spacing(2) },
  [touch]: {
    ...adminControlSx(theme)[touch],
    "& .MuiTableRow-root": { height: 40 },
    "& .MuiTableCell-root .MuiIconButton-root": { minWidth: 40, minHeight: 40 },
    "& .MuiTableCell-paddingCheckbox .MuiCheckbox-root": { minHeight: 40 },
  },
}));

export const AdminFilterPopover = styled(Popover)(({ theme }) => ({
  ...adminControlSx(theme),
  "& .MuiPopover-paper": {
    width: 280,
    padding: theme.spacing(1.5),
  },
  "& .MuiPopover-paper > .MuiStack-root > :not(style) ~ :not(style)": { marginTop: theme.spacing(1.25) },
}));

export default AdminPage;
