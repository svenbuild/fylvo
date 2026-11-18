import React, { useCallback, useEffect } from "react";
import { Box, DialogContent, styled, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import { hoverSurface } from "../../fylvoTheme.ts";
import { useAppDispatch, useAppSelector } from "../../redux/hooks.ts";
import DraggableDialog from "../Dialogs/DraggableDialog.tsx";
import { setUploadFromClipboardDialog } from "../../redux/globalStateSlice.ts";
import Clipboard from "../Icons/Clipboard.tsx";

export interface PasteUploadDialogProps {
  onFilePasted: (files: File[]) => void;
}

export interface PasteTargetProps extends PasteUploadDialogProps {
  onClose: () => void;
}

const PasteTargetContainer = styled(Box)(({ theme }) => ({
  borderRadius: theme.shape.borderRadius,
  border: `1px solid ${theme.palette.divider}`,
  backgroundColor: hoverSurface(theme),
  display: "flex",
  justifyContent: "center",
  alignItems: "center",
  gap: 8,
  padding: theme.spacing(2),
  color: theme.palette.text.secondary,
}));

const PasteTarget = ({ onFilePasted, onClose }: PasteTargetProps) => {
  const { t } = useTranslation();
  const pasteHandler = (e: ClipboardEvent) => {
    e.preventDefault();
    if (!e.clipboardData?.files.length) {
      return;
    }

    onFilePasted(Array.from(e.clipboardData.files));
    onClose();
  };

  useEffect(() => {
    // listen to onpaste event for window
    window.addEventListener("paste", pasteHandler);
    return () => {
      window.removeEventListener("paste", pasteHandler);
    };
  }, []);

  const disableDefault = useCallback((e: React.MouseEvent | React.KeyboardEvent | React.ClipboardEvent) => {
    e.preventDefault();
    return false;
  }, []);
  return (
    <PasteTargetContainer>
      <Clipboard fontSize="small" />
      <Typography variant="body2">{t("uploader.pasteFilesHere")}</Typography>
    </PasteTargetContainer>
  );
};

export default function PasteUploadDialog({ onFilePasted }: PasteUploadDialogProps) {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const open = useAppSelector((state) => state.globalState.uploadFromClipboardDialogOpen);

  const onClose = useCallback(() => {
    dispatch(setUploadFromClipboardDialog(false));
  }, []);

  return (
    <DraggableDialog
      title={t("uploader.uploadFromClipboard")}
      dialogProps={{
        fullWidth: true,
        maxWidth: "xs",
        open: !!open,
        onClose,
      }}
    >
      <DialogContent>
        <PasteTarget onClose={onClose} onFilePasted={onFilePasted} />
      </DialogContent>
    </DraggableDialog>
  );
}
