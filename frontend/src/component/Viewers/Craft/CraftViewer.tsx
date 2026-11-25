import { Alert, Box, Button, Chip } from "@mui/material";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getFileInfo } from "../../../api/api.ts";
import { getConnectedOfficeState, inConnectedStorage } from "../../../api/connected.ts";
import { closeCraftViewer } from "../../../redux/globalStateSlice.ts";
import { fileUpdated } from "../../../redux/fileManagerSlice.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { saveFile } from "../../../redux/thunks/viewer.ts";
import SessionManager from "../../../session";
import { getFileLinkedUri } from "../../../util";
import CrUri from "../../../util/uri.ts";
import { FileManagerIndex } from "../../FileManager/FileManager.tsx";
import ViewerDialog, { ViewerLoading } from "../ViewerDialog.tsx";
import { loadBlankTemplate } from "../../../util/craft.ts";
import { newLockToken, saveRoute, WopiClient } from "./wopiClient.ts";

// Fylvo: a Craft editor (WordCraft, GridCraft, DeckCraft, PdfCraft). The editor runs in a frame of
// this server and gets the file through its host messages (`<app>:open`), and hands every save back
// (`<app>:save`). This page makes the WOPI calls: it locks the file while it edits it, saves over
// the version it read (so a change made elsewhere meanwhile is never overwritten), and keeps what
// could not be stored until it is stored, copied or thrown away by the user.

// A save this long after the first unsaved change; Ctrl+S saves at once.
const autosaveDelay = 60 * 1000;
// A failed save is tried again after this long.
const retryDelay = 30 * 1000;
// The lock lasts 30 minutes on the server; it is renewed well before.
const lockRefreshDelay = 10 * 60 * 1000;
// How long closing waits for the editor to hand over its last state.
const closeWait = 15 * 1000;
// An editor that is not ready after this long did not start.
const startDeadline = 90 * 1000;

type Status = "saved" | "unsaved" | "saving";

interface Session {
  id: string;
  client: WopiClient;
  app: string;
  // The name the editor knows the file by.
  name: string;
  edit: boolean;
  connected: boolean;
  lock?: string;
  version?: string;
  // Changes the editor has that are not saved (as it reports them).
  dirty: boolean;
  // What the editor handed over and is not stored yet.
  unsaved?: ArrayBuffer;
  // No more saves into the file: it changed elsewhere, or the session is over.
  frozen: boolean;
  saving?: Promise<void>;
  // Called with the next save the editor hands over.
  nextSave?: () => void;
  snapshot?: Promise<void>;
  ready: boolean;
  autosave?: ReturnType<typeof setTimeout>;
  timers: ReturnType<typeof setTimeout>[];
  closed: boolean;
}

// The page command API of an editor (window.<app>).
type CraftApi = { execute?: (command: string, params: object) => Promise<unknown> };

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const CraftViewer = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const viewerState = useAppSelector((state) => state.globalState.craftViewer);
  const frameRef = useRef<HTMLIFrameElement>(null);
  const sessionRef = useRef<Session>();
  const [frameSrc, setFrameSrc] = useState<string>();
  const [loaded, setLoaded] = useState(false);
  const [edit, setEdit] = useState(false);
  const [status, setStatus] = useState<Status>("saved");
  const [notice, setNotice] = useState<{
    key: string;
    severity: "info" | "warning" | "error";
    params?: Record<string, string>;
  }>();
  const [askClose, setAskClose] = useState(false);
  const [closing, setClosing] = useState(false);
  const pendingOpen = useRef<{ name: string; bytes: ArrayBuffer }>();

  const file = viewerState?.file;
  const app = viewerState?.viewer?.props?.craft ?? "";
  const open = !!viewerState?.open;
  const sessionId = viewerState?.session?.id;

  const showStatus = useCallback(() => {
    const s = sessionRef.current;
    if (!s) {
      return;
    }
    setStatus(s.saving ? "saving" : s.unsaved || (s.dirty && s.edit) ? "unsaved" : "saved");
  }, []);

  // The row in the list shows the new version and size.
  const refreshRow = useCallback(async () => {
    if (!file) {
      return;
    }
    try {
      const uri = getFileLinkedUri(file);
      const updated = await dispatch(getFileInfo({ uri }, true));
      dispatch(fileUpdated({ index: FileManagerIndex.main, value: [{ oldPath: uri, file: updated }] }));
    } catch {
      // The list shows the new state on its next reload.
    }
  }, [dispatch, file]);

  // Stores what the editor handed over last. One save at a time; one that comes meanwhile is
  // taken by the running one when it is done.
  const store = useCallback((): Promise<void> => {
    const s = sessionRef.current;
    if (!s || s.saving) {
      return s?.saving ?? Promise.resolve();
    }
    s.saving = (async () => {
      while (s.unsaved && s.edit && !s.frozen) {
        const bytes = s.unsaved;
        showStatus();
        let answer;
        try {
          answer = await s.client.put(bytes, s.lock, s.connected ? undefined : s.version);
        } catch {
          answer = { status: 0 };
        }
        if (answer.status == 200) {
          if (s.unsaved === bytes) {
            s.unsaved = undefined;
          }
          s.version = answer.version ?? s.version;
          setNotice((n) => (n?.key == "craft.saveFailed" ? undefined : n));
          refreshRow();
          continue;
        }
        if (answer.status == 409) {
          // Changed elsewhere (or the lock went to someone else). A connected storage keeps this
          // state as a copy beside the file; only a copy the server confirms counts.
          s.frozen = true;
          const state = s.connected ? await dispatch(getConnectedOfficeState(s.id)).catch(() => undefined) : undefined;
          if (state?.copy && s.unsaved === bytes) {
            s.unsaved = undefined;
          }
          setNotice(
            state?.copy
              ? { key: "craft.conflictConnected", severity: "error", params: { name: state.copy.name } }
              : { key: "craft.conflict", severity: "error" },
          );
        } else if (answer.status == 401 || answer.status == 403 || answer.status == 404) {
          s.frozen = true;
          setNotice({ key: "craft.sessionEnded", severity: "error" });
        } else {
          setNotice({ key: "craft.saveFailed", severity: "warning" });
          s.timers.push(setTimeout(() => store(), retryDelay));
        }
        break;
      }
    })().finally(() => {
      s.saving = undefined;
      showStatus();
    });
    showStatus();
    return s.saving;
  }, [dispatch, showStatus, refreshRow]);

  // Asks the editor for its current state; it answers with a save message.
  const requestSave = useCallback(async () => {
    const s = sessionRef.current;
    const api = (frameRef.current?.contentWindow as unknown as Record<string, CraftApi | undefined> | null)?.[
      s?.app ?? ""
    ];
    if (!s || !api?.execute) {
      return;
    }
    try {
      await api.execute("file.save", {});
    } catch {
      // Nothing to save, or the editor is busy: the next change tries again.
    }
  }, []);

  // The editor's current state, handed over as a save (to wait for before closing or copying).
  // One at a time, never longer than closeWait.
  const snapshot = useCallback((): Promise<void> => {
    const s = sessionRef.current;
    if (!s) {
      return Promise.resolve();
    }
    if (!s.snapshot) {
      const handed = new Promise<void>((resolve) => (s.nextSave = resolve));
      s.snapshot = Promise.race([handed, sleep(closeWait)]).then(() => {
        s.nextSave = undefined;
        s.snapshot = undefined;
      });
      requestSave();
    }
    return s.snapshot;
  }, [requestSave]);

  // The editor did not start: the file is given back, nothing is saved from this window.
  const failStart = useCallback((s: Session) => {
    s.frozen = true;
    if (s.lock) {
      s.client.lock("UNLOCK", s.lock).catch(() => undefined);
      s.lock = undefined;
    }
    setEdit(false);
    // A late start opens nothing: the frame and the document are dropped.
    pendingOpen.current = undefined;
    setFrameSrc(undefined);
    setNotice({ key: "craft.editorFailed", severity: "error" });
    setLoaded(true);
  }, []);

  // A copy the user places (Save As, an export, a file that could not go into its place). Who
  // is not signed in gets it as a download.
  const saveCopy = useCallback(
    async (name: string, bytes: ArrayBuffer) => {
      if (!file) {
        return false;
      }
      if (!SessionManager.currentLoginOrNull()) {
        const link = document.createElement("a");
        link.href = URL.createObjectURL(new Blob([bytes]));
        link.download = name;
        link.click();
        setTimeout(() => URL.revokeObjectURL(link.href), 60 * 1000);
        return true;
      }
      try {
        const target = new CrUri(file.path).parent().join(name).toString();
        return !!(await dispatch(saveFile(target, bytes, undefined, true)));
      } catch {
        return false;
      }
    },
    [dispatch, file],
  );

  const onSave = useCallback(
    async (name: string, bytes: ArrayBuffer, saveAs: boolean) => {
      const s = sessionRef.current;
      if (!s) {
        return;
      }
      if (saveRoute(s.name, name, saveAs) == "copy") {
        await saveCopy(name, bytes);
        return;
      }
      s.unsaved = bytes;
      s.nextSave?.();
      if (!s.edit || s.frozen) {
        showStatus();
        if (!s.edit) {
          setNotice({ key: "craft.readOnlySave", severity: "warning" });
        }
        return;
      }
      await store();
    },
    [saveCopy, store, showStatus],
  );

  // The session: lock, read, hand the file to the editor.
  useEffect(() => {
    if (!open || !viewerState || !file || !app) {
      return;
    }
    const s: Session = {
      id: viewerState.session.id,
      client: new WopiClient(viewerState.src, viewerState.session.access_token),
      app,
      name: file.name,
      edit: false,
      connected: inConnectedStorage(file.path),
      dirty: false,
      frozen: false,
      timers: [],
      closed: false,
      ready: false,
    };
    sessionRef.current = s;
    setFrameSrc(undefined);
    setLoaded(false);
    setNotice(undefined);
    setAskClose(false);
    setClosing(false);
    setStatus("saved");

    (async () => {
      let info = await s.client.info();
      s.name = info.BaseFileName || file.name;
      s.edit = info.UserCanWrite && !viewerState.version;
      if (s.edit && info.SupportsLocks) {
        const lock = newLockToken();
        const answer = await s.client.lock("LOCK", lock);
        if (answer == 200 && s.closed) {
          // Closed meanwhile: the lock is given back at once.
          s.client.lock("UNLOCK", lock).catch(() => undefined);
          return;
        }
        if (answer == 200) {
          s.lock = lock;
          // The version as it is under the lock: saves go over this one.
          info = await s.client.info();
        } else {
          s.edit = false;
          setNotice({ key: answer == 409 ? "craft.lockedByOther" : "craft.lockFailed", severity: "info" });
        }
      }
      s.version = info.Version;
      let bytes = await s.client.contents();
      if (bytes.byteLength == 0) {
        bytes = (await loadBlankTemplate(s.name)) ?? bytes;
      }
      if (s.closed) {
        return;
      }
      setEdit(s.edit);
      pendingOpen.current = { name: s.name, bytes };
      s.timers.push(setTimeout(() => !s.ready && !s.closed && failStart(s), startDeadline));
      const query = new URLSearchParams({ host: "parent", author: info.UserFriendlyName ?? "" });
      // PdfCraft offers its command API (used for autosave) only when asked.
      setFrameSrc(`/craft/${app}/?${query.toString()}${app == "pdfcraft" ? "&control" : ""}`);
    })().catch(() => {
      if (s.lock) {
        s.client.lock("UNLOCK", s.lock).catch(() => undefined);
        s.lock = undefined;
      }
      if (!s.closed) {
        s.frozen = true;
        setNotice({ key: "craft.openFailed", severity: "error" });
        setLoaded(true);
      }
    });

    // The lock is renewed while the editor is open, and at once when the page comes back (after a
    // laptop slept, for example). One that was lost is taken again if the file is free; saves are
    // checked against the version in any case.
    const refreshLock = async () => {
      if (!s.lock || s.closed) {
        return;
      }
      const lock = s.lock;
      const answer = await s.client.lock("REFRESH_LOCK", lock).catch(() => 0);
      if (answer == 200 || answer == 0) {
        return;
      }
      const again = await s.client.lock("LOCK", lock).catch(() => 0);
      if ((s.closed || s.frozen) && again == 200) {
        s.client.lock("UNLOCK", lock).catch(() => undefined);
      } else if (again == 409) {
        setNotice({ key: "craft.lockLost", severity: "warning" });
      }
    };
    const lockTimer = setInterval(refreshLock, lockRefreshDelay);
    const onVisible = () => document.visibilityState == "visible" && refreshLock();
    const onLeave = () => {
      if (s.lock) {
        s.client.lock("UNLOCK", s.lock, true).catch(() => undefined);
      }
    };
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      if (s.unsaved || s.dirty) {
        e.preventDefault();
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("pagehide", onLeave);
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => {
      s.closed = true;
      clearInterval(lockTimer);
      s.timers.forEach(clearTimeout);
      clearTimeout(s.autosave);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("pagehide", onLeave);
      window.removeEventListener("beforeunload", onBeforeUnload);
      onLeave();
    };
  }, [open, sessionId]);

  // The editor's messages: only from its own frame on this origin.
  useEffect(() => {
    if (!open || !app) {
      return;
    }
    const onMessage = (e: MessageEvent) => {
      const s = sessionRef.current;
      const data = e.data;
      if (!s || e.origin != window.location.origin || e.source != frameRef.current?.contentWindow) {
        return;
      }
      if (!data || typeof data.type != "string" || !data.type.startsWith(app + ":")) {
        return;
      }
      switch (data.type.slice(app.length + 1)) {
        case "ready": {
          const pending = pendingOpen.current;
          pendingOpen.current = undefined;
          s.ready = true;
          if (pending) {
            frameRef.current?.contentWindow?.postMessage(
              { type: `${app}:open`, name: pending.name, bytes: pending.bytes },
              window.location.origin,
              [pending.bytes],
            );
          }
          setLoaded(true);
          break;
        }
        case "dirty":
          s.dirty = !!data.dirty;
          if (s.dirty && s.edit && !s.frozen && !s.autosave) {
            s.autosave = setTimeout(() => {
              s.autosave = undefined;
              if (sessionRef.current === s && s.dirty) {
                requestSave();
              }
            }, autosaveDelay);
          }
          showStatus();
          break;
        case "save":
          if (data.bytes instanceof ArrayBuffer && typeof data.name == "string") {
            onSave(data.name, data.bytes, !!data.saveAs);
          }
          break;
        case "failed":
          if (!s.ready) {
            failStart(s);
          }
          break;
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [open, app, onSave, requestSave, showStatus, failStart]);

  const finishClose = useCallback(() => {
    dispatch(closeCraftViewer());
  }, [dispatch]);

  // Closing saves first. What cannot be stored is not thrown away without asking.
  const onClose = useCallback(async () => {
    const s = sessionRef.current;
    if (!s || closing) {
      return;
    }
    setClosing(true);
    if (s.dirty && s.edit && !s.frozen) {
      await snapshot();
    }
    await store();
    setClosing(false);
    if (s.unsaved || s.dirty) {
      setAskClose(true);
      return;
    }
    finishClose();
  }, [closing, snapshot, store, finishClose]);

  // Keeps what could not be stored as a copy the user places, then closes.
  const copyAndClose = useCallback(async () => {
    const s = sessionRef.current;
    if (!s || closing) {
      return;
    }
    setClosing(true);
    if (s.dirty) {
      await snapshot();
    }
    const bytes = s.unsaved;
    if (!bytes && !s.dirty) {
      // Nothing that is not stored (a connected storage has kept the copy already).
      setClosing(false);
      finishClose();
      return;
    }
    // Under a name of its own: the copy never goes over the file.
    const dot = s.name.lastIndexOf(".") > 0 ? s.name.lastIndexOf(".") : s.name.length;
    const copyName = `${s.name.slice(0, dot)} (${t("application:craft.copySuffix")})${s.name.slice(dot)}`;
    const copied = !!bytes && (await saveCopy(copyName, bytes));
    setClosing(false);
    if (!copied) {
      setNotice({ key: "craft.copyFailed", severity: "warning" });
      return;
    }
    if (s.unsaved === bytes) {
      s.unsaved = undefined;
    }
    if (!s.unsaved && !s.dirty) {
      finishClose();
    }
  }, [closing, snapshot, saveCopy, finishClose, t]);

  // Read-only and the save state belong to an editor that is shown, not to one still starting.
  const shown = loaded && !!frameSrc;

  return (
    <ViewerDialog
      file={file}
      readOnly={shown && !edit}
      loading={closing}
      status={
        shown &&
        edit && (
          <Chip
            size="small"
            variant="outlined"
            sx={{ ml: 1, height: 20 }}
            label={t(`application:craft.status.${status}`)}
          />
        )
      }
      fullScreenToggle
      dialogProps={{
        open,
        onClose,
        fullWidth: true,
        maxWidth: "lg",
        // The editor fills a window of fixed height: a notice takes room from it instead of
        // growing the window. A window with only an error stays small.
        PaperProps: {
          sx: {
            ...((frameSrc || !loaded) && { "&:not(.MuiDialog-paperFullScreen)": { height: "calc(100% - 64px)" } }),
            overflow: "hidden",
          },
        },
      }}
    >
      {notice && (
        <Alert
          severity={notice.severity}
          sx={{ borderRadius: 0, alignItems: "center" }}
          onClose={notice.severity == "error" ? undefined : () => setNotice(undefined)}
          action={
            askClose ? undefined : notice.key == "craft.conflict" ||
              notice.key == "craft.conflictConnected" ||
              notice.key == "craft.copyFailed" ||
              notice.key == "craft.sessionEnded" ||
              notice.key == "craft.readOnlySave" ? (
              <Button color="inherit" size="small" onClick={copyAndClose} sx={{ whiteSpace: "nowrap" }}>
                {t("application:craft.saveCopy")}
              </Button>
            ) : notice.key == "craft.saveFailed" ? (
              <Button color="inherit" size="small" onClick={() => store()} sx={{ whiteSpace: "nowrap" }}>
                {t("application:craft.retry")}
              </Button>
            ) : undefined
          }
        >
          {t(`application:${notice.key}`, notice.params)}
        </Alert>
      )}
      {askClose && (
        <Alert
          severity="warning"
          sx={{ borderRadius: 0, alignItems: "center" }}
          action={
            <>
              <Button color="inherit" size="small" onClick={copyAndClose} sx={{ whiteSpace: "nowrap" }}>
                {t("application:craft.saveCopy")}
              </Button>
              <Button color="inherit" size="small" onClick={finishClose} sx={{ whiteSpace: "nowrap" }}>
                {t("application:craft.discard")}
              </Button>
              <Button color="inherit" size="small" onClick={() => setAskClose(false)} sx={{ whiteSpace: "nowrap" }}>
                {t("common:cancel")}
              </Button>
            </>
          }
        >
          {t("application:craft.unsavedOnClose")}
        </Alert>
      )}
      {!loaded && <ViewerLoading minHeight="0" />}
      {frameSrc && (
        <Box
          component="iframe"
          ref={frameRef}
          key={sessionId}
          src={frameSrc}
          title={viewerState?.viewer?.display_name}
          allow="clipboard-read; clipboard-write; fullscreen"
          sx={{
            width: "100%",
            height: loaded ? undefined : 0,
            flex: loaded ? 1 : "none",
            minHeight: 0,
            border: "none",
            display: "block",
          }}
        />
      )}
    </ViewerDialog>
  );
};

export default CraftViewer;
