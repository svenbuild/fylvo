import { Alert, Box, Button, Chip } from "@mui/material";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getFileInfo } from "../../../api/api.ts";
import { inConnectedStorage } from "../../../api/connected.ts";
import { closeCraftViewer } from "../../../redux/globalStateSlice.ts";
import { fileUpdated } from "../../../redux/fileManagerSlice.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { saveFile } from "../../../redux/thunks/viewer.ts";
import SessionManager from "../../../session";
import { fileExtension, getFileLinkedUri } from "../../../util";
import CrUri from "../../../util/uri.ts";
import { FileManagerIndex } from "../../FileManager/FileManager.tsx";
import ViewerDialog, { ViewerLoading } from "../ViewerDialog.tsx";
import { blankTemplate, newLockToken, saveRoute, WopiClient } from "./wopiClient.ts";

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

type Status = "saved" | "unsaved" | "saving";

interface Session {
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
  timers: ReturnType<typeof setTimeout>[];
  closed: boolean;
}

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
  const [notice, setNotice] = useState<{ key: string; severity: "info" | "warning" | "error" }>();
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
    } catch (e) {
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
      while (s.unsaved && !s.frozen) {
        const bytes = s.unsaved;
        showStatus();
        let answer;
        try {
          answer = await s.client.put(bytes, s.lock, s.connected ? undefined : s.version);
        } catch (e) {
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
          // Changed elsewhere (or the lock went to someone else). A connected storage has kept
          // this state as a copy beside the file already.
          s.frozen = true;
          if (s.connected) {
            s.unsaved = undefined;
          }
          setNotice({ key: s.connected ? "craft.conflictConnected" : "craft.conflict", severity: "error" });
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
  }, [showStatus, refreshRow]);

  // Asks the editor for its current state; it answers with a save message.
  const requestSave = useCallback(async () => {
    const s = sessionRef.current;
    const api = (frameRef.current?.contentWindow as any)?.[s?.app ?? ""];
    if (!s || !api?.execute) {
      return;
    }
    try {
      await api.execute("file.save", {});
    } catch (e) {
      // Nothing to save, or the editor is busy: the next change tries again.
    }
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
      } catch (e) {
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
      client: new WopiClient(viewerState.src, viewerState.session.access_token),
      app,
      name: file.name,
      edit: false,
      connected: inConnectedStorage(file.path),
      dirty: false,
      frozen: false,
      timers: [],
      closed: false,
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
      const template = blankTemplate(fileExtension(s.name) ?? "");
      if (bytes.byteLength == 0 && template) {
        bytes = await (await fetch(template)).arrayBuffer();
      }
      if (s.closed) {
        return;
      }
      setEdit(s.edit);
      pendingOpen.current = { name: s.name, bytes };
      const query = new URLSearchParams({ host: "parent", author: info.UserFriendlyName ?? "" });
      // PdfCraft offers its command API (used for autosave) only when asked.
      setFrameSrc(`/craft/${app}/?${query.toString()}${app == "pdfcraft" ? "&control" : ""}`);
    })().catch(() => {
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
      const answer = await s.client.lock("REFRESH_LOCK", s.lock).catch(() => 0);
      if (answer != 200 && answer != 0 && (await s.client.lock("LOCK", s.lock).catch(() => 0)) == 409) {
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
      if (s.unsaved || (s.dirty && s.edit && !s.frozen)) {
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
          if (s.dirty && s.edit && !s.frozen) {
            s.timers.push(setTimeout(() => sessionRef.current === s && s.dirty && requestSave(), autosaveDelay));
          }
          showStatus();
          break;
        case "save":
          if (data.bytes instanceof ArrayBuffer && typeof data.name == "string") {
            onSave(data.name, data.bytes, !!data.saveAs);
          }
          break;
        case "failed":
          if (!loaded) {
            setNotice({ key: "craft.editorFailed", severity: "error" });
            setLoaded(true);
          }
          break;
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [open, app, onSave, requestSave, showStatus, loaded]);

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
      await requestSave();
      const until = Date.now() + closeWait;
      while (s.dirty && Date.now() < until) {
        await sleep(200);
      }
    }
    await store();
    setClosing(false);
    if (s.unsaved || s.dirty) {
      setAskClose(true);
      return;
    }
    finishClose();
  }, [closing, requestSave, store, finishClose]);

  // Keeps what could not be stored as a copy the user places, then closes.
  const copyAndClose = useCallback(async () => {
    const s = sessionRef.current;
    if (!s) {
      return;
    }
    if (!s.unsaved && s.dirty) {
      await requestSave();
      await sleep(500);
    }
    if (s.unsaved && (await saveCopy(s.name, s.unsaved))) {
      s.unsaved = undefined;
      finishClose();
    }
  }, [requestSave, saveCopy, finishClose]);

  const statusLabel = !edit ? undefined : t(`application:craft.status.${status}`);

  return (
    <ViewerDialog
      file={file}
      readOnly={!edit}
      loading={closing}
      status={statusLabel && <Chip size="small" variant="outlined" sx={{ ml: 1, height: 20 }} label={statusLabel} />}
      fullScreenToggle
      dialogProps={{
        open,
        onClose,
        fullWidth: true,
        maxWidth: "lg",
      }}
    >
      {notice && (
        <Alert
          severity={notice.severity}
          sx={{ borderRadius: 0, alignItems: "center" }}
          onClose={notice.severity == "error" ? undefined : () => setNotice(undefined)}
          action={
            notice.key == "craft.conflict" || notice.key == "craft.sessionEnded" || notice.key == "craft.readOnlySave" ? (
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
          {t(`application:${notice.key}`)}
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
      {!loaded && <ViewerLoading />}
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
            height: loaded ? "100%" : 0,
            minHeight: loaded ? "calc(100vh - 200px)" : 0,
            border: "none",
            display: "block",
          }}
        />
      )}
    </ViewerDialog>
  );
};

export default CraftViewer;
