import { Box, Chip, Dialog, DialogProps, Divider, IconButton, styled, useMediaQuery, useTheme } from "@mui/material";
import { hoverSurface, shellSurface } from "../../fylvoTheme.ts";
import React, { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse } from "../../api/explorer.ts";
import FacebookCircularProgress from "../Common/CircularProgress.tsx";
import { NoWrapTypography } from "../Common/StyledComponents.tsx";
import { StyledDialogTitle } from "../Dialogs/DraggableDialog.tsx";
import FileIcon from "../FileManager/Explorer/FileIcon.tsx";
import Dismiss from "../Icons/Dismiss.tsx";
import FullScreenMaximize from "../Icons/FullScreenMaximize.tsx";
import FullScreenMinimize from "../Icons/FullScreenMinimize.tsx";

const ViewerFrame = styled(Dialog)({
  "& .MuiDialog-paper": { boxShadow: "none" },
  "& .MuiDialog-paperFullScreen": { border: 0, borderRadius: 0 },
});

export interface ViewerDialogProps {
  file?: FileResponse;
  readOnly?: boolean;
  // Fylvo: shown after the name, for example whether the changes are saved.
  status?: React.ReactNode;
  fullScreen?: boolean;
  actions?: React.ReactNode;
  fullScreenToggle?: boolean;
  dialogProps: DialogProps;
  loading?: boolean;
  children?: React.ReactNode;
  toggleFullScreen?: () => void;
}

export const ViewerLoading = ({ minHeight = "calc(100vh - 200px)" }: { minHeight?: string }) => (
  <Box
    sx={{
      width: "100%",
      height: "100%",
      border: "none",
      minHeight,
      display: "flex",
      justifyContent: "center",
      alignItems: "center",
    }}
  >
    <FacebookCircularProgress />
  </Box>
);

const ViewerDialog = (props: ViewerDialogProps) => {
  const { t } = useTranslation();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const [fullScreen, setFullScreen] = useState(props.fullScreen || isMobile);
  const onClose = useCallback(() => {
    props.dialogProps.onClose && props.dialogProps.onClose({}, "closeButtonClick" as any);
  }, [props.dialogProps.onClose]);
  return (
    <ViewerFrame
      fullScreen={fullScreen}
      {...props.dialogProps}
      onClose={props.loading ? undefined : props.dialogProps.onClose}
    >
      <Box>
        <StyledDialogTitle
          sx={{
            minHeight: 42,
            px: "10px",
            py: "2px",
            bgcolor: shellSurface,
            "& > :first-child": { minWidth: 0 },
            "& .MuiIconButton-root:hover": { bgcolor: hoverSurface },
            "& .MuiButtonBase-root.Mui-focusVisible": {
              outline: (theme) => `1px solid ${theme.palette.primary.main}`,
              outlineOffset: -1,
            },
            "@media (max-width: 599.95px), (pointer: coarse)": {
              px: "6px",
              gap: "4px",
              "& .MuiIconButton-root": { minWidth: 40, minHeight: 40 },
              "& .MuiButton-root": { minHeight: 40 },
            },
          }}
          id="draggable-dialog-title"
        >
          {props.actions && props.actions}
          <Box sx={{ display: "flex", alignItems: "center", minWidth: 0 }}>
            <FileIcon variant={"default"} file={props.file} sx={{ p: 0, mr: 1, flexShrink: 0 }} fontSize={"small"} />
            <NoWrapTypography variant={"subtitle2"}>{props.file?.name}</NoWrapTypography>
            {props.readOnly && <Chip size="small" sx={{ ml: 1, height: 20 }} label={t("fileManager.readOnly")} />}
            {props.status}
          </Box>
          <Box sx={{ display: "flex", flexShrink: 0 }}>
            {props.fullScreenToggle && (
              <IconButton
                onClick={() => {
                  props.toggleFullScreen ? props.toggleFullScreen() : setFullScreen((s) => !s);
                }}
              >
                {fullScreen ? <FullScreenMinimize fontSize={"small"} /> : <FullScreenMaximize fontSize={"small"} />}
              </IconButton>
            )}
            <IconButton disabled={props.loading} onClick={onClose}>
              <Dismiss fontSize={"small"} />
            </IconButton>
          </Box>
        </StyledDialogTitle>
        <Divider />
      </Box>
      {props.children}
    </ViewerFrame>
  );
};

export default ViewerDialog;
