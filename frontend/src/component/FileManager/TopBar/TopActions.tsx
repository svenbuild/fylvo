import { Button, ButtonGroup, styled, useMediaQuery, useTheme } from "@mui/material";
import { bindPopover } from "material-ui-popup-state";
import { bindMenu, bindTrigger, usePopupState } from "material-ui-popup-state/hooks";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { useAppSelector } from "../../../redux/hooks.ts";
import { hoverSurface } from "../../../fylvoTheme.ts";
import ArrowSort from "../../Icons/ArrowSort.tsx";
import TableSettingsOutlined from "../../Icons/TableSettings.tsx";
import SortMethodMenu from "./SortMethodMenu.tsx";
import ViewOptionPopover from "./ViewOptionPopover.tsx";

import MoreHorizontal from "../../Icons/MoreHorizontal.tsx";
import { FmIndexContext } from "../FmIndexContext.tsx";
import MoreActionMenu from "./MoreActionMenu.tsx";

export const ActionButton = styled(Button)(({ theme }) => ({
  color: theme.palette.text.secondary,
  minWidth: 30,
  minHeight: 28,
  padding: "3px 7px",
  "&:hover": { backgroundColor: hoverSurface(theme), color: theme.palette.text.primary },
  "&.Mui-focusVisible": {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: -1,
  },
  "@media (max-width: 599.95px), (pointer: coarse)": { minWidth: 40, minHeight: 40 },
}));

export const ActionButtonGroup = styled(ButtonGroup)(({ theme }) => ({
  flexShrink: 0,
  "& .MuiButtonGroup-grouped": {
    borderColor: theme.palette.divider,
    "&:hover": { borderColor: theme.palette.divider },
  },
  "& .MuiButtonGroup-grouped:not(:last-of-type)": {
    borderRightColor: theme.palette.divider,
    "&:hover": { borderRightColor: theme.palette.divider },
  },
}));

const TopActions = () => {
  const { t } = useTranslation();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const fmIndex = useContext(FmIndexContext);
  const sortOptions = useAppSelector((state) => state.fileManager[fmIndex].list?.props.order_by_options);
  const isSingleFileView = useAppSelector((state) => state.fileManager[fmIndex].list?.single_file_view);
  const viewPopupState = usePopupState({
    variant: "popover",
    popupId: "viewOption",
  });
  const sortPopupState = usePopupState({
    variant: "popover",
    popupId: "sortOption",
  });
  const morePopupState = usePopupState({
    variant: "popover",
    popupId: "moreActions",
  });
  return (
    <>
      <ActionButtonGroup variant="outlined">
        <ActionButton
          aria-label={t("application:fileManager.view")}
          disabled={isSingleFileView}
          {...bindTrigger(viewPopupState)}
          startIcon={!isMobile && <TableSettingsOutlined />}
        >
          {isMobile ? <TableSettingsOutlined fontSize={"small"} /> : t("application:fileManager.view")}
        </ActionButton>
        {(!(!sortOptions || isSingleFileView) || !isMobile) && (
          <ActionButton
            aria-label={t("application:fileManager.sortMethod")}
            disabled={!sortOptions || isSingleFileView}
            startIcon={!isMobile && <ArrowSort />}
            {...bindTrigger(sortPopupState)}
          >
            {isMobile ? <ArrowSort fontSize={"small"} /> : t("application:fileManager.sortMethod")}
          </ActionButton>
        )}
        {isMobile && (
          <ActionButton aria-label={t("application:fileManager.moreActions")} {...bindTrigger(morePopupState)}>
            <MoreHorizontal fontSize={"small"} />
          </ActionButton>
        )}
      </ActionButtonGroup>
      {isMobile && <MoreActionMenu {...bindMenu(morePopupState)} />}
      <ViewOptionPopover {...bindPopover(viewPopupState)} />
      <SortMethodMenu {...bindMenu(sortPopupState)} />
    </>
  );
};

export default TopActions;
