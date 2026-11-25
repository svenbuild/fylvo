import * as React from "react";
import { useLayoutEffect, useRef, useState } from "react";
import { Box, ListItemIcon, ListItemText, Menu, MenuItem, Typography, useMediaQuery, useTheme } from "@mui/material";
import { StyledTab, StyledTabs } from "./StyledComponents.tsx";
import CaretDown from "../Icons/CaretDown.tsx";
import { useTranslation } from "react-i18next";
import { bindMenu, bindTrigger, usePopupState } from "material-ui-popup-state/hooks";

export interface Tab<T> {
  label: React.ReactNode;
  value: T;
  icon?: React.ReactElement;
}

export interface ResponsiveTabsProps<T> {
  tabs: Tab<T>[];
  value: T;
  onChange: (event: React.SyntheticEvent, value: T) => void;
}

const ResponsiveTabs = <T,>({ tabs, value, onChange }: ResponsiveTabsProps<T>) => {
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const [hideTabs, setHideTabs] = useState(false);
  const tabsRef = useRef<HTMLDivElement>(null);
  // Width all tabs need side by side, measured while all of them are shown.
  const neededWidth = useRef(0);
  const allShown = useRef(true);
  allShown.current = !isMobile && !hideTabs;
  const { t, i18n } = useTranslation();
  const moreOptionState = usePopupState({
    variant: "popover",
    popupId: "tabMore",
  });
  const { onClose, ...menuProps } = bindMenu(moreOptionState);
  const list = () => tabsRef.current?.children[0]?.children[0] as HTMLElement | undefined;
  const fit = () => {
    const el = list();
    if (el) {
      setHideTabs(neededWidth.current > el.clientWidth);
    }
  };
  const measure = () => {
    const last = list()?.lastElementChild as HTMLElement | null | undefined;
    if (last) {
      neededWidth.current = last.offsetLeft + last.offsetWidth;
    }
    fit();
  };
  // Fylvo: measure only once React has put all tabs back on screen (an animation frame could still see the folded ones).
  const measurePending = useRef(false);
  useLayoutEffect(() => {
    if (measurePending.current && allShown.current) {
      measurePending.current = false;
      measure();
    }
  }, [hideTabs]);

  useLayoutEffect(() => {
    // On phones the tabs are always folded into the menu, nothing to measure.
    if (isMobile) {
      return;
    }

    // Measuring needs all tabs on screen: show them, then measure.
    let active = true;
    const remeasure = () => {
      if (!active) {
        return;
      }
      if (allShown.current) {
        measure();
      } else {
        measurePending.current = true;
        setHideTabs(false);
      }
    };

    remeasure();
    // Tab widths change once the web font has replaced the fallback font.
    document.fonts?.ready.then(remeasure);
    // The room changes also without a window resize (the sidebar opens or is dragged).
    const observer = new ResizeObserver(fit);
    if (tabsRef.current) {
      observer.observe(tabsRef.current);
    }
    return () => {
      active = false;
      observer.disconnect();
    };
  }, [isMobile, tabs.length, i18n.language]);

  return (
    <Box
      sx={{
        borderBottom: 1,
        borderColor: "divider",
        pb: "2px",
      }}
    >
      <StyledTabs ref={tabsRef} value={value} onChange={onChange}>
        {tabs
          .filter((tab) => (isMobile || hideTabs ? tab.value == value : true))
          .map((tab) => (
            <StyledTab label={tab.label} value={tab.value} icon={tab.icon} />
          ))}
        {(isMobile || hideTabs) && tabs.length > 1 && (
          <>
            <StyledTab
              label={
                <Typography
                  sx={{
                    display: "flex",
                    gap: "4px",
                  }}
                  variant={"inherit"}
                >
                  {t("application:navbar.showMore")}
                  <CaretDown sx={{ fontSize: 15 }} />
                </Typography>
              }
              {...bindTrigger(moreOptionState)}
            />
            <Menu {...menuProps} onClose={onClose}>
              {tabs
                .filter((tab) => tab.value != value)
                .map((option, index) => (
                  <MenuItem
                    dense
                    key={index}
                    onClick={(e) => {
                      onClose();
                      onChange(e, option.value);
                    }}
                  >
                    {option.icon && <ListItemIcon>{option.icon}</ListItemIcon>}
                    <ListItemText>{option.label}</ListItemText>
                  </MenuItem>
                ))}
            </Menu>
          </>
        )}
      </StyledTabs>
    </Box>
  );
};

export default ResponsiveTabs;
