import i18next from "i18next";
import { enqueueSnackbar } from "notistack";
import { getFileInfo, getFileList } from "../../api/api.ts";
import { FileResponse, Metadata } from "../../api/explorer.ts";
import { DefaultCloseAction } from "../../component/Common/Snackbar/snackbar.tsx";
import { getActionOpt } from "../../component/FileManager/ContextMenu/useActionDisplayOpt.ts";
import CrUri, { CrUriPrefix, Filesystem } from "../../util/uri.ts";
import { ContextMenuTypes } from "../fileManagerSlice.ts";
import { setFileClipboard } from "../globalStateSlice.ts";
import { Viewers } from "../siteConfigSlice.ts";
import { AppThunk } from "../store.ts";
import { deleteFile, fileDoubleClicked, isMacbook, moveFiles, restoreFile, submitRenameFile } from "./file.ts";
import { navigateToPath, refreshFileList } from "./filemanager.ts";
import { takeUndo, withoutRecording } from "./undo.ts";

// Fylvo: the keyboard shortcuts of the file manager (see useFileShortcuts). They use the same
// actions and the same rules as the menus: what the menu does not offer, a key does not do either.

// A function: read at module load, isMacbook may not be set yet (the modules import each other).
export const modKey = () => (isMacbook ? "⌘" : "Ctrl");

function hint(key: string, variant: "warning" | "info" = "warning") {
  enqueueSnackbar({
    message: i18next.t(`application:shortcuts.${key}`, { key: modKey() }),
    variant,
    action: DefaultCloseAction,
  });
}

const selectedFiles = (state: { fileManager: { selected: { [key: string]: FileResponse } }[] }, index: number) =>
  Object.values(state.fileManager[index].selected);

// Ctrl+C and Ctrl+X: remember the selection for Ctrl+V.
export function clipSelection(index: number, cut: boolean): AppThunk {
  return async (dispatch, getState) => {
    const files = selectedFiles(getState(), index);
    if (files.length == 0) {
      return;
    }
    const opt = getActionOpt(files, Viewers);
    if (!(cut ? opt.showMove : opt.showCopy)) {
      hint(cut ? "cannotCut" : "cannotCopy");
      return;
    }
    dispatch(setFileClipboard({ files, cut }));
    hint(cut ? "cut" : "copied", "info");
  };
}

// Ctrl+V: copy or move the remembered files into the open folder, with the actions of
// "Copy to" and "Move to".
export function pasteClipboard(index: number): AppThunk {
  return async (dispatch, getState) => {
    const clipboard = getState().globalState.fileClipboard;
    if (!clipboard) {
      return;
    }
    const fm = getState().fileManager[index];
    const parent = fm.list?.parent;
    const dst = fm.pure_path;
    const canAdd =
      !!dst &&
      !!fm.path &&
      !new CrUri(fm.path).is_search() &&
      getActionOpt([], undefined, ContextMenuTypes.empty, parent, index).showCreateFolder;
    if (!canAdd) {
      hint("cannotPaste");
      return;
    }
    if (!clipboard.cut && clipboard.files.every((f) => new CrUri(f.path).parent().toString() == dst)) {
      hint("sameFolder");
      return;
    }
    if (!clipboard.cut) {
      // A copy refreshes the open folder itself.
      dispatch(moveFiles(index, clipboard.files, dst, true));
      return;
    }
    dispatch(setFileClipboard(undefined));
    await dispatch(moveFiles(index, clipboard.files, dst, false));
    // A move only takes the files out of the folder they came from; here they come in.
    dispatch(refreshFileList(index));
  };
}

// Esc: forget files taken with Ctrl+X (files taken with Ctrl+C stay, they show nothing).
export function cancelCut(): AppThunk {
  return async (dispatch, getState) => {
    if (getState().globalState.fileClipboard?.cut) {
      dispatch(setFileClipboard(undefined));
    }
  };
}

// Delete (or Cmd+Backspace on a Mac): to the trash, with the usual question.
export function deleteSelected(index: number): AppThunk {
  return async (dispatch, getState) => {
    const files = selectedFiles(getState(), index);
    if (files.length == 0) {
      return;
    }
    if (!getActionOpt(files, Viewers).showDelete) {
      hint("cannotDelete");
      return;
    }
    dispatch(deleteFile(index, files));
  };
}

// Enter: open the one selected file or folder, as a double click does.
export function openSelected(index: number): AppThunk {
  return async (dispatch, getState) => {
    const files = selectedFiles(getState(), index);
    if (files.length == 1) {
      dispatch(fileDoubleClicked(index, files[0]));
    }
  };
}

// F2: the rename itself is started by the row (useInlineRename); this only explains why nothing
// happens.
export function renameHint(index: number): AppThunk {
  return async (_dispatch, getState) => {
    const files = selectedFiles(getState(), index);
    if (files.length > 1 || (files.length == 1 && !getActionOpt(files, Viewers).showRename)) {
      hint("cannotRename");
    }
  };
}

// Backspace or Alt+Up: one folder up.
export function goUp(index: number): AppThunk {
  return async (dispatch, getState) => {
    const path = getState().fileManager[index].path;
    if (!path) {
      return;
    }
    const uri = new CrUri(path);
    if (uri.is_search() || uri.elements().length == 0) {
      return;
    }
    dispatch(navigateToPath(index, uri.parent().toString()));
  };
}

// Ctrl+Z: undo the last own rename, move or delete to the trash. When in doubt it does nothing:
// each file must still be the same one (same id) where the action left it.
export function undoLast(index: number): AppThunk {
  return async (dispatch) => {
    const entry = takeUndo();
    if (!entry) {
      hint("nothingToUndo", "info");
      return;
    }
    if (entry.kind == "none") {
      hint("cannotUndo", "info");
      return;
    }
    const files = entry.kind == "rename" ? [entry.file] : entry.kind == "move" ? entry.files.map((f) => f.file) : [];
    for (const file of files) {
      if (!(await dispatch(stillThere(file)))) {
        hint("changedSince");
        return;
      }
    }
    await withoutRecording(async () => {
      switch (entry.kind) {
        case "rename":
          await dispatch(submitRenameFile(index, entry.file, entry.oldName));
          break;
        case "move": {
          const from = [...new Set(entry.files.map((f) => f.from))];
          for (const folder of from) {
            const back = entry.files.filter((f) => f.from == folder).map((f) => f.file);
            await dispatch(moveFiles(index, back, folder, false));
          }
          // The open folder may be the one they come back to.
          dispatch(refreshFileList(index));
          break;
        }
        case "trash":
          await dispatch(restoreFromTrash(index, entry.files));
          break;
      }
    });
  };
}

// Whether the file at file.path is still the one with file.id.
function stillThere(file: FileResponse): AppThunk<Promise<boolean>> {
  return async (dispatch) => {
    try {
      const now = await dispatch(getFileInfo({ uri: file.path }, true));
      return now.id == file.id;
    } catch (e) {
      return false;
    }
  };
}

// The same place, also when one address names the owner and the other does not.
const samePlace = (a: string, b: string) => {
  const x = new CrUri(a);
  const y = new CrUri(b);
  return x.fs() == y.fs() && x.elements().join("/") == y.elements().join("/");
};

// A file keeps its id in the trash. Each deleted file must be found there by its id and the place
// it came from, or nothing is restored.
function restoreFromTrash(index: number, files: FileResponse[]): AppThunk<Promise<void>> {
  return async (dispatch) => {
    let trashed: FileResponse[];
    try {
      const res = await dispatch(
        getFileList({
          uri: CrUriPrefix + Filesystem.trash,
          page: 0,
          page_size: 1000,
          order_by: "updated_at",
          order_direction: "desc",
        }),
      );
      trashed = res.files;
    } catch (e) {
      hint("notInTrash");
      return;
    }
    const found = files.map((f) =>
      trashed.find((t) => {
        const from = t.metadata?.[Metadata.restore_uri];
        return t.id == f.id && !!from && samePlace(from, f.path);
      }),
    );
    if (found.some((f) => !f)) {
      hint("notInTrash");
      return;
    }
    await dispatch(restoreFile(index, found as FileResponse[]));
    dispatch(refreshFileList(index));
  };
}
