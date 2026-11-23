import { Box, ButtonBase, styled } from "@mui/material";
import * as React from "react";
import { hoverSurface, raisedHoverSurface, raisedSelectedSurface, selectedSurface } from "../../../fylvoTheme.ts";
import { NoWrapTypography } from "../../Common/StyledComponents.tsx";

const StyledButtonBase = styled(ButtonBase)<{
  active?: boolean;
}>(({ theme, active }) => ({
  borderRadius: "8px",
  display: "flex",
  justifyContent: "flex-start",
  alignItems: "center",
  width: "100%",
  height: "var(--fylvo-nav-height, 28px)",
  color: active ? theme.palette.text.primary : theme.palette.text.secondary,
  backgroundColor: active ? `${selectedSurface(theme)}!important` : "initial",
  "&:hover": {
    backgroundColor: hoverSurface(theme),
    color: theme.palette.text.primary,
  },
  // In a dialog (folder picker) the item sits on a raised surface: use the raised steps.
  ".MuiDialog-paper &&:hover, .MuiDialog-paper &&.Mui-focused": { backgroundColor: raisedHoverSurface(theme) },
  ...(active && { ".MuiDialog-paper &&": { backgroundColor: `${raisedSelectedSurface(theme)}!important` } }),
  "&.Mui-focusVisible": {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: -1,
  },
  [theme.breakpoints.down("sm")]: { height: "max(40px, var(--fylvo-nav-height, 28px))" },
  "@media (pointer: coarse)": { height: "max(40px, var(--fylvo-nav-height, 28px))" },
}));

export interface SideNavItemBaseProps {
  active?: boolean;
  [key: string]: any;
}
export const SideNavItemBase = React.forwardRef(
  ({ active, ...rest }: SideNavItemBaseProps, ref: React.Ref<HTMLButtonElement>) => {
    return <StyledButtonBase active={active} {...rest} ref={ref} />;
  },
);

const StyledSideNavItem = styled(SideNavItemBase)<{ level?: number }>(({ level }) => ({
  padding: `0 6px 0 ${24 + (level ?? 0) * 10}px`,
}));

export interface SideNavItemProps extends SideNavItemBaseProps {
  icon?: React.ReactNode;
  label?: string | React.ReactNode;
  level?: number;
  [key: string]: any;
}

const SideNavItem = React.forwardRef(
  ({ icon, label, level, sx, ...rest }: SideNavItemProps, ref: React.Ref<HTMLButtonElement>) => {
    return (
      <StyledSideNavItem
        level={level}
        sx={{
          ...sx,
        }}
        {...rest}
        ref={ref}
      >
        <Box
          sx={{
            width: 16,
            flexShrink: 0,
            mr: 1,
            display: "flex",
            alignItems: "center",
            color: "text.secondary",
          }}
        >
          {icon}
        </Box>
        <NoWrapTypography variant={"body2"}>{label}</NoWrapTypography>
      </StyledSideNavItem>
    );
  },
);

export default SideNavItem;
