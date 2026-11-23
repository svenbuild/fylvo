import { FileResponse } from "../../../api/explorer.ts";
import { fileExtension } from "../../../util";
import i18next from "i18next";
import SessionManager from "../../../session";
import { saveDrawIO } from "../../../redux/thunks/viewer.ts";
import { AppDispatch } from "../../../redux/store.ts";
import { DrawIOViewerState } from "../../../redux/globalStateSlice.ts";
import { getFileInfo } from "../../../api/api.ts";

const defaultHost = "https://embed.diagrams.net";

// Fylvo: the origin the editor's messages must come from.
// The host may be relative to this site; undefined when it is no address at all.
export const drawIOOrigin = (host?: string) => {
  try {
    return new URL(host || defaultHost, window.location.href).origin;
  } catch (_) {
    return undefined;
  }
};

export const generateIframeSrc = (
  host: string | undefined,
  file: FileResponse,
  readOnly: boolean,
  darkMode: boolean,
) => {
  const ext = fileExtension(file.name);
  const query = new URLSearchParams({
    embed: "1",
    embedRT: "1",
    configure: "1",
    libraries: "1",
    spin: "1",
    proto: "json",
    keepmodified: "1",
    p: "nxtcld",
    lang: i18next.t("fileManager.drawioLng"),
    dark: darkMode ? "1" : "0",
  });

  if (ext == "dwb") {
    query.set("ui", "sketch");
  }

  if (readOnly) {
    query.set("chrome", "0");
  }

  return (host || defaultHost) + "?" + query.toString();
};

export const handleRemoteInvoke = async (
  w: Window,
  msg: any,
  dispatch: AppDispatch,
  viewerState: DrawIOViewerState,
  writeable?: boolean,
  instanceId?: string,
) => {
  switch (msg.funtionName) {
    case "getCurrentUser":
      const currentUser = SessionManager.currentUser();
      sendResponse(w, msg, [
        currentUser
          ? {
              displayName: currentUser?.user.nickname,
              uid: currentUser?.user.id,
            }
          : null,
      ]);
      break;
    case "saveFile":
      if (!writeable) {
        sendResponse(w, msg, null, "read only");
        break;
      }
      try {
        const res = await dispatch(saveDrawIO(msg.functionArgs[2], viewerState.file, false));
        if (res) {
          sendResponse(w, msg, [
            {
              etag: res.primary_entity,
              size: res.size,
            },
          ]);
        } else {
          // Fylvo: a refused save is answered as well, so the editor does not wait for it.
          sendResponse(w, msg, null, "not saved");
        }
      } catch (e) {
        sendResponse(w, msg, null, `${e}`);
      }
      break;
    case "getFileInfo":
      try {
        // Fylvo: the file the editor holds now (also after a save under a new name).
        const current = dispatch((_d, getState) => getState().globalState.drawIOViewer?.file) ?? viewerState.file;
        const res = await dispatch(getFileInfo({ uri: current.path }));
        if (res) {
          sendResponse(w, msg, [
            {
              id: res.id,
              size: res.size,
              writeable,
              name: res.name,
              etag: res.primary_entity,
              versionEnabled: true,
              ver: 2,
              instanceId,
            },
          ]);
        }
      } catch (e) {
        sendResponse(w, msg, null, `${e}`);
      }
      break;
  }
};

const sendResponse = (w: Window, msg: any, respose: any, error?: string) => {
  var respMsg: {
    action: string;
    msgMarkers: any;
    error?: { errResp?: string };
    resp?: any;
  } = { action: "remoteInvokeResponse", msgMarkers: msg.msgMarkers };
  if (error) {
    respMsg.error = { errResp: error };
  } else if (respose != null) {
    respMsg.resp = respose;
  }

  console.log("Send remote invoke response PostMessage", respMsg);
  w.postMessage(JSON.stringify(respMsg), "*");
};
