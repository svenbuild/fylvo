import { Alert, Box, ListItemText, Menu, useTheme } from "@mui/material";
import i18n from "i18next";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { closeWopiViewer, setVersionControlDialog, WopiViewerState } from "../../redux/globalStateSlice.ts";
import { useAppDispatch, useAppSelector } from "../../redux/hooks.ts";
import { openShareDialog } from "../../redux/thunks/file.ts";
import { SquareMenuItem } from "../FileManager/ContextMenu/ContextMenu.tsx";
import useActionDisplayOpt, { canUpdate } from "../FileManager/ContextMenu/useActionDisplayOpt.ts";
import { FileManagerIndex } from "../FileManager/FileManager.tsx";
import ViewerDialog, { ViewerLoading } from "./ViewerDialog.tsx";
import { inConnectedStorage } from "../../api/connected.ts";
import { refreshFileList } from "../../redux/thunks/filemanager.ts";

const WopiForm = ({
  replacedSrc,
  viewerState,
  onSubmit,
}: {
  viewerState: WopiViewerState;
  replacedSrc: string;
  onSubmit: () => void;
}) => {
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => {
    formRef.current?.submit();
    onSubmit();
  }, [viewerState]);
  return (
    <form ref={formRef} id="office_form" name="office_form" target="office_frame" action={replacedSrc} method="post">
      <input name="access_token" value={viewerState.session.access_token} type="hidden" />
      <input name="access_token_ttl" value={viewerState.session.expires} type="hidden" />
    </form>
  );
};

const Wopi = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const theme = useTheme();
  const viewerState = useAppSelector((state) => state.globalState.wopiViewer);

  const displayOpt = useActionDisplayOpt(viewerState?.file ? [viewerState?.file] : []);
  const canEdit = canUpdate(displayOpt);
  const [noticeClosed, setNoticeClosed] = useState(false);
  // A new session (also one opened over a running one) shows the notice again.
  useEffect(() => setNoticeClosed(false), [viewerState?.session?.id]);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);

  const formRef = useRef<HTMLFormElement>(null);
  const submitedRef = useRef<boolean>(false);

  const replacedSrc = useMemo(() => {
    if (!viewerState?.src) {
      return "";
    }

    // Fylvo: fill in the placeholders in the query only. Replacing them anywhere in the address
    // could change the host the access token is posted to.
    const placeholders = new Map([
      ["lng", i18n.resolvedLanguage?.toLowerCase() ?? ""],
      ["darkmode", theme.palette.mode === "dark" ? "2" : "1"],
    ]);
    const url = new URL(viewerState.src, window.location.href);
    for (const [key, value] of [...url.searchParams]) {
      const replacement = placeholders.get(value);
      if (replacement !== undefined) {
        url.searchParams.set(key, replacement);
      }
    }

    return url.toString();
  }, [viewerState?.src, theme]);

  // Fylvo: editing in a connected storage: each editor window is a document of its own. The
  // server says whether the session may save (it may open it for viewing only); without that
  // answer it counts as viewing.
  const connected = !!viewerState?.file && inConnectedStorage(viewerState.file.path);
  const connectedEdit = connected && viewerState?.edit === true;

  const handlePostMessage = (e: MessageEvent) => {
    console.log("Received PostMessage from " + e.origin, e.data);
    let msg;
    try {
      msg = JSON.parse(e.data);
    } catch (e) {
      return;
    }

    if (!viewerState?.file) {
      return;
    }

    // Fylvo: a connected storage has no shares and no versions of this server.
    if (inConnectedStorage(viewerState.file.path)) {
      return;
    }

    if (msg.MessageId === "UI_Sharing" || msg.MessageId === "UI_Share") {
      dispatch(openShareDialog(FileManagerIndex.main, viewerState?.file));
    } else if (msg.MessageId == "UI_FileVersions") {
      dispatch(setVersionControlDialog({ open: true, file: viewerState.file }));
    }
  };

  // Fylvo: bound again for a new session, so the editor's requests name its own file.
  useEffect(() => {
    if (!viewerState || !viewerState.open) {
      submitedRef.current = false;
      return;
    }

    window.addEventListener("message", handlePostMessage, false);
    return () => {
      window.removeEventListener("message", handlePostMessage, false);
    };
  }, [viewerState?.open, viewerState?.session?.id]);

  const onClose = useCallback(() => {
    dispatch(closeWopiViewer());
    // Fylvo: on closing with unsaved changes after a conflict, the server puts them next to the
    // file as a copy; the folder shows it.
    if (connected) {
      dispatch(refreshFileList(FileManagerIndex.main));
    }
  }, [dispatch, connected]);

  const openMore = useCallback(
    (e: React.MouseEvent<any>) => {
      setAnchorEl(e.currentTarget);
    },
    [dispatch],
  );

  const handleIframeOnload = useCallback(() => {
    if (submitedRef.current) {
      setLoaded(true);
    }
  }, []);

  return (
    <>
      <ViewerDialog
        file={viewerState?.file}
        loading={loading}
        readOnly={connected ? !connectedEdit : !canEdit}
        fullScreenToggle
        dialogProps={{
          open: !!(viewerState && viewerState.open),
          onClose: onClose,
          fullWidth: true,
          maxWidth: "lg",
        }}
      >
        <Menu
          anchorEl={anchorEl}
          open={Boolean(anchorEl)}
          onClose={() => setAnchorEl(null)}
          slotProps={{
            paper: {
              sx: {
                minWidth: 150,
              },
            },
          }}
        >
          <SquareMenuItem dense>
            <ListItemText>{t("modals.saveAs")}</ListItemText>
          </SquareMenuItem>
        </Menu>
        {viewerState && (
          <WopiForm
            onSubmit={() => {
              submitedRef.current = true;
            }}
            viewerState={viewerState}
            replacedSrc={replacedSrc}
          />
        )}
        {connectedEdit && !noticeClosed && (
          <Alert severity="warning" onClose={() => setNoticeClosed(true)} sx={{ borderRadius: 0 }}>
            {t("application:connected.officeOneWindow")}
          </Alert>
        )}
        {!loaded && <ViewerLoading />}
        <Box
          onLoad={handleIframeOnload}
          component={"iframe"}
          id={"office_frame"}
          name={"office_frame"}
          sandbox={
            "allow-scripts allow-same-origin allow-forms allow-popups allow-top-navigation allow-popups-to-escape-sandbox allow-downloads allow-modals"
          }
          allow="clipboard-read *; clipboard-write *"
          allowFullScreen={true}
          sx={{
            width: "100%",
            height: loaded ? "100%" : 0,
            border: "none",
            minHeight: loaded ? "calc(100vh - 200px)" : 0,
          }}
        />
      </ViewerDialog>
    </>
  );
};

export default Wopi;
