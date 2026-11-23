import { IconButton, Popover, ToggleButton, ToggleButtonGroup, Tooltip } from "@mui/material";
import DarkTheme from "../../Icons/DarkTheme.tsx";
import { useTranslation } from "react-i18next";
import { useMemo, useState } from "react";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import Sunny from "../../Icons/Sunny.tsx";
import Moon from "../../Icons/Moon.tsx";
import SunWithTime from "../../Icons/SunWithTime.tsx";
import { hoverSurface, selectedSurface } from "../../../fylvoTheme.ts";
import { AppearanceMode } from "../../../api/fylvo.ts";
import { saveAppearanceMode } from "../../../redux/thunks/fylvo.ts";

interface SwitchPopoverProps {
  open?: boolean;
  anchorEl?: HTMLElement | null;
  onClose?: () => void;
}

export const SwitchPopover = ({ open, anchorEl, onClose }: SwitchPopoverProps) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();

  const darkMode = useAppSelector((state) => state.globalState.darkMode);
  const currentMode = useMemo(() => {
    if (darkMode === undefined) {
      return "system";
    }
    return darkMode ? "dark" : "light";
  }, [darkMode]);
  const handleChange = (_event: React.MouseEvent<HTMLElement>, newMode: AppearanceMode | null) => {
    if (!newMode) {
      return;
    }
    // Fylvo: the choice is stored with the account, so every device follows.
    dispatch(saveAppearanceMode(newMode));
    onClose && onClose();
  };

  const inner = (
    <ToggleButtonGroup
      color={onClose ? "standard" : "primary"}
      value={currentMode}
      exclusive
      onChange={handleChange}
      size={onClose ? undefined : "small"}
      aria-label="Platform"
      sx={
        onClose
          ? (theme) => ({
              p: "4px",
              gap: "2px",
              "& .MuiToggleButtonGroup-grouped": {
                margin: 0,
                minHeight: 30,
                border: "1px solid transparent",
                borderRadius: "6px",
                textTransform: "none",
                color: theme.palette.text.secondary,
                "&:hover": { backgroundColor: hoverSurface(theme), color: theme.palette.text.primary },
                "&.Mui-selected, &.Mui-selected:hover": {
                  backgroundColor: selectedSurface(theme),
                  color: theme.palette.text.primary,
                },
                "&.Mui-focusVisible": { borderColor: theme.palette.primary.main },
                "@media (pointer: coarse)": { minHeight: 40 },
              },
            })
          : undefined
      }
    >
      <ToggleButton value="light">
        <Sunny fontSize="small" sx={{ mr: 1 }} />
        {t("navbar.toLightMode")}
      </ToggleButton>
      <ToggleButton value="system">
        <SunWithTime fontSize="small" sx={{ mr: 1 }} />
        {t("setting.syncWithSystem")}
      </ToggleButton>
      <ToggleButton value="dark">
        <Moon fontSize="small" sx={{ mr: 1 }} />
        {t("navbar.toDarkMode")}
      </ToggleButton>
    </ToggleButtonGroup>
  );

  return onClose ? (
    <Popover
      open={!!open}
      anchorEl={anchorEl}
      onClose={onClose}
      anchorOrigin={{
        vertical: "bottom",
        horizontal: "right",
      }}
      transformOrigin={{ vertical: "top", horizontal: "right" }}
      slotProps={{ paper: { sx: { mt: "4px" } } }}
    >
      {inner}
    </Popover>
  ) : (
    inner
  );
};

const DarkThemeSwitcher = () => {
  const { t } = useTranslation();
  const [anchorEl, setAnchorEl] = useState<HTMLButtonElement | null>(null);

  const handleClick = (event: React.MouseEvent<HTMLButtonElement>) => {
    setAnchorEl(event.currentTarget);
  };

  return (
    <>
      <Tooltip title={t("navbar.darkModeSwitch")}>
        <IconButton onClick={handleClick}>
          <DarkTheme />
        </IconButton>
      </Tooltip>
      <SwitchPopover open={Boolean(anchorEl)} anchorEl={anchorEl} onClose={() => setAnchorEl(null)} />
    </>
  );
};

export default DarkThemeSwitcher;
