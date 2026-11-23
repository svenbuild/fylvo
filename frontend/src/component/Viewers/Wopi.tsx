import { Alert, Box, Button, ListItemText, Menu, useTheme } from "@mui/material";
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
import { ConnectedCode, getConnectedOfficeState, inConnectedStorage } from "../../api/connected.ts";
import { FileResponse } from "../../api/explorer.ts";
import { AppError, Code } from "../../api/request.ts";
import { refreshFileList } from "../../redux/thunks/filemanager.ts";
import { openViewers, openWopiViewer } from "../../redux/thunks/viewer.ts";
import CrUri from "../../util/uri.ts";
import { AppThunk } from "../../redux/store.ts";

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

// Fylvo: reloads the main list when it shows folder; a later reload (later) only while nothing is
// selected and the folder fits one page, so it does not undo what the user does meanwhile.
const refreshFolderIfOpen =
  (folder: string, later: boolean): AppThunk =>
  (dispatch, getState) => {
    const fm = getState().fileManager[FileManagerIndex.main];
    const pagination = fm.list?.pagination;
    // More than one page: there is a next one, or more rows are shown than one page holds.
    const pages = !!pagination?.next_token || (fm.list?.files.length ?? 0) > (pagination?.page_size ?? Infinity);
    const busy = Object.keys(fm.selected ?? {}).length > 0 || pages;
    if (fm.pure_path && new CrUri(fm.pure_path).toString() == folder && !(later && busy)) {
      dispatch(refreshFileList(FileManagerIndex.main));
    }
  };

// Fylvo: how often an editing window of a connected storage asks the server whether it can
// still save.
const officeStateInterval = 4000;

// Fylvo: answers after which there is nothing more to ask: the server no longer knows the
// window, the account is signed out, or it may not use connected storages.
const officeStateOver: number[] = [
  40016,
  Code.CodeLoginRequired,
  Code.CredentialInvalid,
  Code.PermissionDenied,
  ConnectedCode.Unavailable,
];

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

  // Fylvo: after a conflict (the file changed elsewhere) the editor shows its dialog once and
  // then stops sending what is typed, also when the window is closed. The server has put the
  // refused state next to the file as a copy and knows that this window cannot save any more;
  // the editor does not tell this page. So the page asks the server while the window is open.
  // Once the conflict is known, or the server no longer knows the window, it stops asking.
  // conflict is set then; its copy is missing when the server could not keep one.
  const [conflict, setConflict] = useState<{ copy?: FileResponse } | undefined>(undefined);
  const conflictCopy = conflict?.copy;
  const [openingCopy, setOpeningCopy] = useState(false);
  const sessionId = viewerState?.session?.id;
  const watching = connectedEdit && !!viewerState?.open && !!sessionId;
  useEffect(() => {
    setConflict(undefined);
    setOpeningCopy(false);
    if (!watching || !sessionId) {
      return;
    }
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined = undefined;
    const ask = async () => {
      try {
        const state = await dispatch(getConnectedOfficeState(sessionId));
        if (stopped) {
          return;
        }
        if (state.conflict) {
          setConflict({ copy: state.copy });
          return;
        }
      } catch (e) {
        // An answer that says the window is over ends the asking. Anything else (a request
        // that did not get through or hung, a server in trouble for a moment) is tried again.
        if (stopped || (e instanceof AppError && officeStateOver.includes(e.code))) {
          return;
        }
      }
      timer = setTimeout(ask, officeStateInterval);
    };
    timer = setTimeout(ask, officeStateInterval);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [watching, sessionId]);

  // The editor is locked: nothing more can be typed into a window that does not save, once
  // what it had is safe in a copy. Without a copy it stays usable: what the user wrote is in
  // the editor only, and has to be taken out of it by hand.
  useEffect(() => {
    const frame = document.getElementById("office_frame");
    if (!frame) {
      return;
    }
    frame.toggleAttribute("inert", !!conflictCopy);
    if (conflictCopy) {
      frame.blur();
    }
  }, [conflictCopy, sessionId]);

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
    // Fylvo: on closing an editing window after a conflict, the server puts the unsaved changes
    // next to the file as a copy, once the editor has noticed the window is gone (a few seconds).
    // The folder is shown again then, while it is still open.
    if (connectedEdit && viewerState?.file) {
      const folder = new CrUri(viewerState.file.path).parent().toString();
      for (const delay of [0, 5000, 15000]) {
        setTimeout(() => dispatch(refreshFolderIfOpen(folder, delay > 0)), delay);
      }
    }
  }, [dispatch, connectedEdit, viewerState?.file]);

  // Fylvo: go on in the copy. It takes the place of this window, in the same editor.
  const openConflictCopy = useCallback(async () => {
    if (!conflictCopy || openingCopy) {
      return;
    }
    setOpeningCopy(true);
    dispatch(refreshFolderIfOpen(new CrUri(conflictCopy.path).parent().toString(), false));
    try {
      if (viewerState?.viewer) {
        await dispatch(openWopiViewer(conflictCopy, viewerState.viewer));
      } else {
        await dispatch(openViewers(FileManagerIndex.main, conflictCopy));
      }
    } catch (e) {
      // The copy could not be opened (it is gone, the provider is busy): the notice stays.
    }
    setOpeningCopy(false);
  }, [dispatch, conflictCopy, openingCopy, viewerState?.viewer]);

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
        {connectedEdit && !noticeClosed && !conflict && (
          <Alert severity="warning" onClose={() => setNoticeClosed(true)} sx={{ borderRadius: 0 }}>
            {t("application:connected.officeOneWindow")}
          </Alert>
        )}
        {conflictCopy && (
          <Alert
            severity="error"
            sx={{ borderRadius: 0, alignItems: "center" }}
            action={
              <>
                <Button
                  color="inherit"
                  size="small"
                  autoFocus
                  disabled={openingCopy}
                  onClick={openConflictCopy}
                  sx={{ whiteSpace: "nowrap" }}
                >
                  {t("application:connected.officeOpenCopy")}
                </Button>
                <Button
                  color="inherit"
                  size="small"
                  disabled={openingCopy}
                  onClick={onClose}
                  sx={{ whiteSpace: "nowrap" }}
                >
                  {t("common:close")}
                </Button>
              </>
            }
          >
            {t("application:connected.officeConflict", { name: conflictCopy.name })}
          </Alert>
        )}
        {conflict && !conflictCopy && (
          <Alert severity="error" sx={{ borderRadius: 0 }}>
            {t("application:connected.officeConflictUnkept")}
          </Alert>
        )}
        {!loaded && <ViewerLoading />}
        <Box
          onLoad={handleIframeOnload}
          component={"iframe"}
          // Fylvo: a new session gets a frame of its own, so the document of the one before is
          // gone at once (it may be one that can no longer save).
          key={sessionId ?? "none"}
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
            // Fylvo: a window that no longer saves is shown, but dimmed and out of reach.
            opacity: conflictCopy ? 0.35 : 1,
            pointerEvents: conflictCopy ? "none" : undefined,
          }}
        />
      </ViewerDialog>
    </>
  );
};

export default Wopi;
