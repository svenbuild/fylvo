import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContentText,
  DialogProps,
  DialogTitle,
  IconButton,
  Paper,
  PaperProps,
  Stack,
  styled,
  useMediaQuery,
} from "@mui/material";

import { LoadingButton } from "@mui/lab";
import { useCallback } from "react";
import Draggable from "react-draggable";
import { useTranslation } from "react-i18next";
import Dismiss from "../Icons/Dismiss.tsx";

function PaperComponent(props: PaperProps) {
  return (
    <Draggable handle="#draggable-dialog-title" cancel={'[class*="MuiDialogContent-root"]'}>
      <Paper {...props} />
    </Draggable>
  );
}

const CompactDialog = styled(Dialog)(({ theme }) => ({
  "& .MuiDialog-paper:not(.MuiDialog-paperFullScreen)": {
    boxShadow: "none",
    [theme.breakpoints.down("sm")]: {
      margin: 12,
      width: "calc(100% - 24px)",
      maxHeight: "calc(100% - 24px)",
    },
  },
  "& .MuiDialog-paperWidthSm:not(.MuiDialog-paperFullScreen)": { maxWidth: 480 },
  "& .MuiDialogContent-root .MuiOutlinedInput-root:not(.MuiInputBase-multiline) .MuiOutlinedInput-input": {
    paddingTop: 7,
    paddingBottom: 7,
  },
  "& .MuiDialogContent-root .MuiInputLabel-outlined.MuiInputLabel-sizeSmall:not(.MuiInputLabel-shrink)": {
    transform: "translate(14px, 7px) scale(1)",
  },
  "& .MuiDialogContent-root .MuiAutocomplete-inputRoot": { paddingBlock: 0 },
  "& .MuiDialogContent-root .MuiFormControl-root:has(.MuiInputLabel-filled) .MuiFilledInput-root:not(.MuiInputBase-multiline) .MuiFilledInput-input": {
    paddingTop: 15,
    paddingBottom: 3,
  },
  "& .MuiDialogContent-root .MuiFilledInput-root.Mui-focused": {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: -1,
  },
  "& .MuiButtonBase-root.Mui-focusVisible": {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: -1,
  },
  "@media (max-width: 599.95px), (pointer: coarse)": {
    "& .MuiDialogContent-root .MuiInputBase-root, & .MuiDialogContent-root .MuiListItemButton-root, & .MuiDialogActions-root .MuiButton-root, & .MuiDialogTitle-root .MuiIconButton-root": {
      minHeight: 40,
    },
    "& .MuiDialogTitle-root .MuiIconButton-root": { minWidth: 40 },
    "& .MuiDialogContent-root .MuiInputLabel-outlined.MuiInputLabel-sizeSmall:not(.MuiInputLabel-shrink)": {
      transform: "translate(14px, 11px) scale(1)",
    },
  },
}));

export const StyledDialogActions = styled(DialogActions)<{
  denseAction?: boolean;
}>(({ theme, denseAction }) => ({
  padding: denseAction ? "6px 16px" : "10px 16px 14px",
  borderTop: `1px solid ${theme.palette.divider}`,
  gap: 8,
  flexWrap: "wrap",
  justifyContent: "flex-end",
  "& > .MuiBox-root:first-child": { marginInlineEnd: "auto", minWidth: 0 },
  "& > :empty": { display: "none" },
  "&.MuiDialogActions-spacing > :not(:first-of-type)": { marginLeft: 0 },
}));

export const StyledDialogContentText = styled(DialogContentText)(({ theme }) => ({
  fontSize: theme.typography.body2.fontSize,
  overflowWrap: "anywhere",
}));

export const StyledDialogTitle = styled(DialogTitle)<{ moveable?: boolean }>(({ moveable }) => ({
  display: "flex",
  justifyContent: "space-between",
  alignItems: "center",
  gap: 8,
  cursor: moveable ? "move" : "initial",
}));

export interface DraggableDialogProps {
  dialogProps: DialogProps;
  children?: React.ReactNode;
  secondaryAction?: React.ReactNode;
  showActions?: boolean;
  showCancel?: boolean;
  hideOk?: boolean;
  okText?: string;
  cancelText?: string;
  title?: string | React.ReactNode;
  onAccept?: () => void;
  loading?: boolean;
  disabled?: boolean;
  denseAction?: boolean;
  secondaryFullWidth?: boolean;
}

const DraggableDialog = (props: DraggableDialogProps) => {
  const { t } = useTranslation();
  const isTouch = useMediaQuery("(pointer: coarse)");
  const onClose = useCallback(() => {
    props.dialogProps.onClose && props.dialogProps.onClose({}, "backdropClick");
  }, [props.dialogProps.onClose]);
  return (
    <CompactDialog
      PaperComponent={isTouch ? undefined : PaperComponent}
      {...props.dialogProps}
      onClose={props.loading ? undefined : props.dialogProps.onClose}
    >
      {props.title != undefined && (
        <Box>
          <StyledDialogTitle sx={{ px: "16px", py: "10px" }} moveable id="draggable-dialog-title">
            <Box sx={{ minWidth: 0, overflowWrap: "anywhere" }}>{props.title}</Box>
            <IconButton disabled={props.loading} onClick={onClose}>
              <Dismiss fontSize={"small"} />
            </IconButton>
          </StyledDialogTitle>
        </Box>
      )}
      {props.children}
      {props.showActions && (
        <StyledDialogActions denseAction={props.denseAction}>
          <Box sx={{ flexGrow: props.secondaryFullWidth ? 1 : "unset" }}>{props.secondaryAction}</Box>
          <Stack direction={"row"} spacing={1}>
            {props.showCancel && (
              <Button color="inherit" disabled={props.loading} onClick={onClose}>
                {props.cancelText ?? t("common:cancel")}
              </Button>
            )}
            {!props.hideOk && (
              <LoadingButton
                disabled={props.disabled}
                loading={props.loading}
                variant={"contained"}
                onClick={props.onAccept}
                color="primary"
              >
                <span>{props.okText ?? t("common:ok")}</span>
              </LoadingButton>
            )}
          </Stack>
        </StyledDialogActions>
      )}
    </CompactDialog>
  );
};

export default DraggableDialog;
