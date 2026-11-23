import { loadTags } from "./fylvo.ts";
import dayjs from "dayjs";
import i18next from "i18next";
import { closeSnackbar, enqueueSnackbar, SnackbarAction, SnackbarKey } from "notistack";
import {
  getFileDirectLinks,
  getFileEntityUrl,
  getFileInfo,
  getFileList,
  getFileThumb,
  sendCreateFile,
  sendDeleteFiles,
  sendEmptyTrash,
  sendMetadataPatch,
  sendMoveFile,
  sendRenameFile,
  sendRestoreFile,
  sendUnlockFiles,
  setCurrentVersion,
} from "../../api/api.ts";
import {
  ConflictDetail,
  DirectLink,
  FileResponse,
  FileThumbResponse,
  FileType,
  ListResponse,
  Metadata,
  MetadataPatch,
  NewFileTemplate,
  Viewer,
} from "../../api/explorer.ts";
import { AppError, Code, Response } from "../../api/request.ts";
import { DefaultCloseAction, ViewDstAction } from "../../component/Common/Snackbar/snackbar.tsx";
import { canShowInfo, getActionOpt } from "../../component/FileManager/ContextMenu/useActionDisplayOpt.ts";
import { DeleteOption } from "../../component/FileManager/Dialogs/DeleteConfirmation.tsx";
import { PathSelectionVariantOptions } from "../../component/FileManager/Dialogs/PathSelection.tsx";
import { Tag } from "../../component/FileManager/Dialogs/Tags.tsx";
import { DragItem, DropEffect, DropResult } from "../../component/FileManager/Dnd/DndWrappedFile.tsx";
import { FileManagerIndex } from "../../component/FileManager/FileManager.tsx";
import { MinPageSize } from "../../component/FileManager/TopBar/ViewOptionPopover.tsx";
import { loadingDebounceMs } from "../../constants";
import { defaultPath } from "../../hooks/useNavigation.tsx";
import SessionManager, { UserSettings } from "../../session";
import { addRecentUsedColor, addUsedTags } from "../../session/utils.ts";
import { fileExtension, getFileLinkedUri } from "../../util";
import Boolset from "../../util/boolset.ts";
import { canCopyMoveTo } from "../../util/permission.ts";
import CrUri, { Filesystem } from "../../util/uri.ts";
import {
  addSelected,
  appendTreeCache,
  clearSelected,
  closeContextMenu,
  closeRenameFileModal,
  ContextMenuTypes,
  fileUpdated,
  removeSelected,
  removeThumbCache,
  removeTreeCache,
  setContextMenu,
  setFileDeleteModal,
  setFileDeleteModalLoading,
  setFileList,
  setRenameFileModalError,
  setRenameFileModalLoading,
  setSelected,
  setThumbCache,
  SingleManager,
} from "../fileManagerSlice.ts";
import {
  CreateNewDialogType,
  setDirectLinkDialog,
  setExtractArchiveDialog,
  setRemoteDownloadDialog,
  setShareLinkDialog,
  setSidebar,
  updateLockConflicts,
} from "../globalStateSlice.ts";
import { ConfigLoadState, Viewers } from "../siteConfigSlice.ts";
import { AppThunk } from "../store.ts";
import { confirmOperation, deleteConfirmation, renameForm, requestCreateNew, selectPath } from "./dialog.ts";
import { downloadSingleFile } from "./download.ts";
import { navigateReconcile, navigateToPath, refreshFileList, updateUserCapacity } from "./filemanager.ts";
import { queueLoadShareInfo } from "./share.ts";
import { loadSiteConfig } from "./site.ts";
import { openViewer, openViewers } from "./viewer.ts";
import {
  connectedBatchSize,
  connectedIds,
  ConnectedCode,
  folderEnterable,
  inConnectedStorage,
  inGroups,
  limiter,
} from "../../api/connected.ts";
import { rowUnsupported } from "../../component/FileManager/Connected/ConnectedMark.tsx";
import { BusyState, codeOf, ProviderGaveUp, retryBusy } from "../../component/FileManager/Connected/providerRetry.ts";

const contextMenuCloseAnimationDelay = 250;

function get_platform(): string {
  // 2022 way of detecting. Note : this userAgentData feature is available only in secure contexts (HTTPS)
  if (typeof navigator.userAgentData !== "undefined" && navigator.userAgentData != null) {
    return navigator.userAgentData.platform;
  }
  // Deprecated but still works for most of the browser
  if (typeof navigator.platform !== "undefined") {
    return navigator.platform;
  }
  return "unknown";
}

export const isMac = () => {
  return get_platform().toUpperCase().indexOf("MAC") >= 0;
};

export const isMacbook = isMac();

// Fylvo: the server asks the provider for a preview of a connected storage when its image is
// loaded, only a few at a time (it answers 503 when busy). So the web app loads them itself, two
// at a time, a few times 5 seconds apart, before the row shows the address (the browser keeps the
// image). The answer for a version without a preview: 40077.
const connectedThumbs = limiter(2);
// The address of a preview is cheap while the folder's listing is fresh; after that the server
// looks the file up at the provider again, so those requests are limited as well.
const connectedThumbAddresses = limiter(3);
const connectedThumbTries = 3;
const connectedThumbPause = 5000;
const connectedThumbTimeout = 30000;

const imageLoads = (url: string) =>
  new Promise<boolean>((resolve) => {
    const img = new Image();
    const timer = setTimeout(() => {
      // Stopped, so its place is free only once nothing is loading any more.
      img.onload = img.onerror = null;
      img.src = "";
      resolve(false);
    }, connectedThumbTimeout);
    img.onload = () => {
      clearTimeout(timer);
      resolve(true);
    };
    img.onerror = () => {
      clearTimeout(timer);
      resolve(false);
    };
    img.src = url;
  });

// Loads the preview image of a connected file: true when it loaded, false when it did not after a
// few tries, undefined when the file is no longer listed anywhere (a waiting load is dropped).
const loadConnectedImage = async (url: string, listed: () => boolean): Promise<boolean | undefined> => {
  for (let i = 0; i < connectedThumbTries; i++) {
    if (i > 0) {
      await new Promise((resolve) => setTimeout(resolve, connectedThumbPause));
    }
    const loaded = await connectedThumbs((waited) =>
      (waited || i > 0) && !listed() ? Promise.resolve(undefined) : imageLoads(url),
    );
    if (loaded !== false) {
      return loaded;
    }
  }
  return false;
};

// Fylvo: reloads the folder of a connected file whose preview was asked for an older version,
// at most once per folder within half a minute (several rows of one listing, no loop). Only a
// plain listing of that folder is reloaded, not a search (that would walk all folders again);
// false when nothing is reloaded (the caller then remembers the failure for a minute).
const staleRefreshes = new Map<string, number>();
const staleRefreshPause = 30000;
function refreshForNewVersion(index: number, file: FileResponse): AppThunk<boolean> {
  return (dispatch, getState) => {
    const folder = new CrUri(file.path).parent().toString();
    const { path, pure_path, list } = getState().fileManager[index];
    if (!path || !pure_path || new CrUri(path).is_search() || new CrUri(pure_path).toString() != folder) {
      return false;
    }
    // Not when more than the first page is shown: reloading would take the user back to the top.
    const morePages = (list?.files.length ?? 0) > (list?.pagination?.page_size ?? Infinity);
    if (morePages || Date.now() - (staleRefreshes.get(folder) ?? 0) < staleRefreshPause) {
      return false;
    }
    staleRefreshes.set(folder, Date.now());
    // Keeps the selection and an open menu (refreshFileList clears them).
    dispatch(navigateReconcile(index));
    return true;
  };
}

export function loadFileThumb(index: number, file: FileResponse): AppThunk<Promise<string | null>> {
  return (dispatch) => {
    // Fylvo: callers for the same version of a connected file share one load.
    if (!inConnectedStorage(file.path)) {
      return dispatch(loadThumb(index, file));
    }
    const key = `${index}|${file.path}|${file.primary_entity}`;
    let load = connectedThumbLoads.get(key);
    if (!load) {
      load = dispatch(loadThumb(index, file)).finally(() => connectedThumbLoads.delete(key));
      connectedThumbLoads.set(key, load);
    }
    return load;
  };
}

const connectedThumbLoads = new Map<string, Promise<string | null>>();

function loadThumb(index: number, file: FileResponse): AppThunk<Promise<string | null>> {
  return async (dispatch, getState) => {
    const { fileManager } = getState();
    const cache = fileManager[index].tree[file.path]?.thumb;
    // Fylvo: a thumbnail belongs to one version of the file (its address stays the same for a day).
    if (cache && cache.entity == file.primary_entity) {
      if (!cache.expires || dayjs(cache.expires).isAfter(dayjs())) {
        return cache.failed ? null : cache.src;
      }
    }

    const connected = inConnectedStorage(file.path);
    // Fylvo: still wanted: this version is listed somewhere and may have a preview.
    const listed = () =>
      getState().fileManager.some(
        (fm) =>
          fm.list?.files.some(
            (f) =>
              f.path == file.path &&
              f.primary_entity == file.primary_entity &&
              f.metadata?.[Metadata.thumbDisabled] === undefined,
          ),
      );
    // Fylvo: an answer for an older version does not replace the one of the version listed now.
    const outdated = () => {
      const listedNow = getState().fileManager[index].list?.files.find((f) => f.path == file.path);
      return !!listedNow && listedNow.primary_entity != file.primary_entity;
    };
    // Marks a waiting request that was dropped because its version is no longer listed.
    const dropped = new Error("dropped");
    try {
      let thumb: FileThumbResponse;
      // Fylvo: a busy connected storage (40923) is asked again a few times, 5 seconds apart,
      // while the version is still wanted.
      for (let i = 0; ; i++) {
        try {
          const request = () =>
            dispatch(
              getFileThumb(
                getFileLinkedUri(file),
                getState().fileManager[index].list?.context_hint,
                connected ? file.primary_entity : undefined,
              ),
            );
          thumb = connected
            ? await connectedThumbAddresses((waited) => (waited && !listed() ? Promise.reject(dropped) : request()))
            : await request();
          break;
        } catch (e) {
          const busy = connected && e instanceof AppError && e.code == ConnectedCode.Busy;
          if (!busy || i + 1 >= connectedThumbTries) {
            throw e;
          }
          await new Promise((resolve) => setTimeout(resolve, connectedThumbPause));
          if (!listed()) {
            return null;
          }
        }
      }
      if (connected) {
        const loaded = await loadConnectedImage(thumb.url, listed);
        if (loaded === undefined) {
          return null;
        }
        if (!loaded) {
          throw new Error("The preview image did not load");
        }
      }
      if (outdated()) {
        return null;
      }
      dispatch(setThumbCache({ index, value: [file.path, thumb, file.primary_entity] }));
      return thumb.url;
    } catch (e) {
      if (e === dropped || outdated()) {
        return null;
      }
      // Fylvo: the file is another version at the provider by now: the folder is shown again
      // (the new row asks for its own preview), and nothing is remembered for this version.
      if (
        connected &&
        e instanceof AppError &&
        e.code == Code.StaleVersion &&
        dispatch(refreshForNewVersion(index, file))
      ) {
        return null;
      }
      // Fylvo: remembered for this version of the file only, in the thumbnail cache, and for a
      // minute (a busy server may have it later); the file entry itself is not touched (an answer
      // that comes late must not bring back an older one). A connected storage that has no
      // preview for this version (40077) is not asked again for it.
      const none = connected && e instanceof AppError && e.code == ConnectedCode.NoPreview;
      dispatch(
        setThumbCache({
          index,
          value: [
            file.path,
            { url: "", expires: none ? "" : dayjs().add(1, "minute").toISOString() },
            file.primary_entity,
            true,
          ],
        }),
      );
      console.warn("Failed to load thumb", e);
      return null;
    }
  };
}

export function enterFolder(index: number, folder: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    setTimeout(() => {
      dispatch(navigateToPath(index, folder.path, folder));
    }, contextMenuCloseAnimationDelay);
  };
}

export function goToParent(index: number, folder: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    const path = new CrUri(folder.path).parent().toString();
    setTimeout(() => {
      dispatch(navigateToPath(index, path));
    }, contextMenuCloseAnimationDelay);
  };
}

// Fylvo: a document of a connected storage's provider (for example a Google Sheet) opens at
// the provider in a new tab. So does a row that names an address there as an extra way to
// open it (a shortcut to a file).
export function openAtProvider(index: number, file: FileResponse): AppThunk {
  return async (dispatch) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    // The first one that names an address, as the menu decides whether to show the entry.
    const url =
      file.metadata?.[Metadata.connected_external_url] || file.metadata?.[Metadata.connected_provider_url];
    if (url && /^https?:\/\//i.test(url)) {
      window.open(url, "_blank", "noopener,noreferrer");
    }
  };
}

export function openFile(index: number, file: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    if (file.type == FileType.folder) {
      dispatch(navigateToPath(index, file.path, file));
      return;
    } else {
      dispatch(openViewers(index, file));
    }
  };
}

let lastSelectedIndex: number = 0;
export function selectFile(index: number, file: FileResponse, e?: React.MouseEvent<HTMLElement>): AppThunk {
  return async (dispatch, getState) => {
    let ctrlKey: boolean = false,
      metaKey: boolean = false,
      shiftKey: boolean = false;
    if (e) {
      ctrlKey = e.ctrlKey;
      metaKey = e.metaKey;
      shiftKey = e.shiftKey;
      if ([ctrlKey, shiftKey].filter(Boolean).length > 1 || [metaKey, shiftKey].filter(Boolean).length > 1) {
        return;
      }
    }

    const {
      fileManager,
      globalState: { sidebarOpen },
    } = getState();
    const { selected, list } = fileManager[index];

    if (!list) {
      return;
    }

    if (sidebarOpen) {
      const cap = new Boolset(file.capability);
      if (canShowInfo(cap)) {
        dispatch(switchSidebarTarget(file));
      }
    }

    const isRangeSelection = shiftKey && !ctrlKey && !metaKey && Object.keys(selected).length !== 0;
    const isCtrlSelection = (ctrlKey && !isMacbook) || (metaKey && isMacbook);

    const fileIndex = list.files.findIndex((f) => f.id === file.id);
    if (isRangeSelection) {
      const begin = Math.min(lastSelectedIndex, fileIndex);
      const end = Math.max(lastSelectedIndex, fileIndex);
      const newShiftSelected = list.files.slice(begin, end + 1);
      return dispatch(setSelected({ index, value: newShiftSelected }));
    }

    lastSelectedIndex = fileIndex;

    if (isCtrlSelection && index != FileManagerIndex.selector) {
      // Ctrl/Command 单击添加/删除
      const currentSelected = selected[file.path];
      if (currentSelected) {
        return dispatch(removeSelected({ index, value: file.path }));
      }
      return dispatch(addSelected({ index, value: [file] }));
    }
    // 单选
    return dispatch(setSelected({ index, value: [file] }));
  };
}

export function switchSidebarTarget(file?: FileResponse): AppThunk {
  return async (dispatch, getState) => {
    const sidebarOpen = getState().globalState.sidebarOpen;
    if (sidebarOpen) {
      dispatch(setSidebar({ open: true, target: file }));
    }
  };
}

export function fileClicked(index: number, file: FileResponse, e?: React.MouseEvent<HTMLElement>): AppThunk {
  return async (dispatch, _getState) => {
    const ctrlKeyPressed = e && (e.ctrlKey || e.metaKey || e.shiftKey);
    const isTrash = file.metadata && file.metadata[Metadata.restore_uri];
    const folderClickAction = SessionManager.getWithFallback(UserSettings.FolderClickAction);
    if (
      file.type === FileType.folder &&
      !ctrlKeyPressed &&
      !isTrash &&
      folderClickAction === "open" &&
      folderEnterable(file)
    ) {
      dispatch(openFile(index, file));
    } else {
      dispatch(selectFile(index, file, e));
    }

    if (e) {
      e.stopPropagation();
    }
  };
}

export function fileDoubleClicked(index: number, file: FileResponse, e?: React.MouseEvent<HTMLElement>): AppThunk {
  return async (dispatch, _getState) => {
    const actionOpt = getActionOpt([file], Viewers);
    if (actionOpt.showOpenAtProvider) {
      dispatch(openAtProvider(index, file));
    } else if (actionOpt.showOpen || actionOpt.showEnter) {
      dispatch(openFile(index, file));
    } else if (
      file.type == FileType.file &&
      actionOpt.showDownload &&
      // Fylvo: a shortcut of a connected storage opens at the provider, it is not downloaded.
      file.metadata?.[Metadata.connected_link] === undefined
    ) {
      dispatch(downloadSingleFile(file));
    }
  };
}

export function fileIconClicked(index: number, file: FileResponse, e?: React.MouseEvent<HTMLElement>): AppThunk {
  return async (dispatch, _getState) => {
    if (e) {
      e.stopPropagation();
      if (!e.shiftKey) {
        if (isMacbook) {
          e.metaKey = true;
        } else {
          e.ctrlKey = true;
        }
      }
    }
    dispatch(selectFile(index, file, e));
    return false;
  };
}

export function selectionFromDragBox(index: number, fileIndex: string[], ctrlKey: boolean, metaKey: boolean): AppThunk {
  return async (dispatch, getState) => {
    const { fileManager } = getState();
    const list = fileManager[index].list?.files;
    const newSelected: FileResponse[] = [];
    fileIndex.forEach((i) => {
      const iInt = parseInt(i, 10);
      if (list && !isNaN(iInt)) {
        newSelected.push(list[iInt]);
      }
    });
    if ((ctrlKey && !isMacbook) || (metaKey && isMacbook)) {
      return dispatch(addSelected({ index, value: newSelected }));
    }

    return dispatch(setSelected({ index, value: newSelected }));
  };
}

export function openFileContextMenu(
  index: number,
  file: FileResponse,
  selectedOverwrite: boolean,
  e: React.MouseEvent<HTMLElement>,
  type: string = ContextMenuTypes.file,
  useExactMousePos: boolean = true,
): AppThunk {
  return async (dispatch, getState) => {
    e.preventDefault();
    e.stopPropagation();
    if (index == FileManagerIndex.selector) {
      return;
    }

    let targets: { [key: string]: FileResponse } | undefined = undefined;
    const { fileManager } = getState();
    if (selectedOverwrite) {
      targets = {
        [file.path]: file,
      };
    } else if (!fileManager[index].selected[file.path]) {
      // If not selected, set to the only selected file
      dispatch(setSelected({ index, value: [file] }));
    }

    const rect = useExactMousePos ? undefined : e.currentTarget?.getBoundingClientRect();
    const { x, y } = rect
      ? { x: rect.x, y: rect.bottom }
      : {
          x: e.clientX,
          y: e.clientY,
        };
    dispatch(
      setContextMenu({
        index,
        value: {
          open: true,
          pos: { x, y },
          type: type,
          targets: targets,
          fmIndex: index,
        },
      }),
    );
  };
}

export function forceUnlockFiles(tokens: string[]): AppThunk<Promise<void>> {
  return async (dispatch, getState) => {
    try {
      await dispatch(confirmOperation(i18next.t("application:modals.forceUnlockDes")));
    } catch (e) {
      return;
    }

    await dispatch(
      sendUnlockFiles({
        tokens,
      }),
    );

    const original = getState().globalState.lockConflictError;
    if (!original) {
      return;
    }
    const currentConflicts: Response<ConflictDetail[]> = {
      ...original,
    };
    currentConflicts.data = currentConflicts.data?.filter((conflict) => {
      return !conflict.token || !tokens.includes(conflict.token);
    });
    dispatch(updateLockConflicts(currentConflicts));
  };
}

export function submitRenameFile(index: number, file: FileResponse, newName: string): AppThunk<Promise<boolean>> {
  return async (dispatch, getState) => {
    let newFile: FileResponse | undefined = undefined;
    try {
      newFile = await dispatch(
        sendRenameFile({
          uri: file.path,
          new_name: newName,
          ids: connectedIds([file]),
        }),
      );
    } catch (e) {
      // Fylvo: in a connected storage the row may have changed at the provider; show its state.
      if (inConnectedStorage(file.path)) {
        dispatch(refreshFileList(index));
      }
      return false;
    }

    if (newFile) {
      dispatch(
        fileUpdated({
          index,
          value: [
            {
              file: newFile,
              oldPath: file.path,
            },
          ],
        }),
      );

      const currentPath = getState().fileManager[index].path;
      if (currentPath) {
        if (
          currentPath.startsWith(file.path + "/") ||
          currentPath.startsWith(file.path + "?") ||
          currentPath == file.path
        ) {
          dispatch(navigateToPath(index, currentPath.replace(file.path, newFile.path)));
        }
      }
    }
    return !!newFile;
  };
}

export function inlineRenameSubmit(
  index: number,
  file: FileResponse,
  newName: string,
): AppThunk<Promise<string | undefined>> {
  return async (dispatch, getState) => {
    const current = getState().fileManager[index].list?.files;
    const existingNames = current?.filter((f) => f.name !== file.name).map((f) => f.name) ?? [];
    const error = validateFileNamePure(newName, existingNames);
    if (error) return error;

    await dispatch(submitRenameFile(index, file, newName));
    return undefined;
  };
}

export function renameFile(index: number, file: FileResponse): AppThunk<Promise<void>> {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    let newName = "";
    try {
      newName = await dispatch(renameForm(index, file));
    } catch (e) {
      // operation canceled
      return;
    }

    dispatch(setRenameFileModalLoading({ index, value: true }));

    try {
      await dispatch(submitRenameFile(index, file, newName));
    } finally {
      dispatch(closeRenameFileModal({ index, value: undefined }));
    }
  };
}

const special = '\\/:*?"<>|';

export function validateFileNamePure(newName: string, existingNames: string[]): string | undefined {
  if (newName.length === 0 || newName.length > 255) {
    return i18next.t("application:modals.newNameLengthError");
  }

  if (newName.split("").some((c) => special.includes(c))) {
    return i18next.t("application:modals.newNameCharacterError");
  }

  if (newName == "." || newName == "..") {
    return i18next.t("application:modals.newNameDotError");
  }

  if (existingNames.includes(newName)) {
    return i18next.t("application:modals.duplicatedObjectName");
  }

  return undefined;
}

export function validateFileName(
  index: number,
  resolve: (value: string | PromiseLike<string>) => void,
  newName: string,
): AppThunk<Promise<void>> {
  return async (dispatch, getState) => {
    const current = getState().fileManager[index].list?.files;
    const existingNames = current?.map((f) => f.name) ?? [];
    const error = validateFileNamePure(newName, existingNames);

    if (error) {
      dispatch(setRenameFileModalError({ index, value: error }));
      return;
    }

    dispatch(setRenameFileModalError({ index, value: undefined }));
    resolve(newName);
  };
}

export function deleteFile(index: number, files: FileResponse[]): AppThunk<Promise<void>> {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));

    // Ask for confirmation and advanced delete options
    let opt: DeleteOption;
    try {
      opt = await dispatch(deleteConfirmation(index, files));
    } catch (e) {
      // operation canceled
      return;
    }

    dispatch(setFileDeleteModalLoading({ index, value: true }));

    let deleted = files;
    // Fylvo: a connected storage takes at most 100 entries per request, each with its row id.
    const connected = files.some((f) => inConnectedStorage(f.path));
    const groups = connected ? inGroups(files, connectedBatchSize) : [files];
    let failed = false;
    for (const [i, group] of groups.entries()) {
      try {
        await dispatch(
          sendDeleteFiles({
            uris: group.map((f) => f.path),
            unlink: opt.unlink,
            skip_soft_delete: opt.skip_soft_delete,
            ids: connectedIds(group),
          }),
        );
      } catch (e) {
        failed = true;
        const partialSuccess = e instanceof AppError && e.aggregatedError;
        if (!partialSuccess) {
          if (!connected) {
            dispatch(setFileDeleteModal({ index, value: [false, files, undefined, true] }));
            return;
          }
          // This group and the ones after it were not deleted.
          const notSent = groups.slice(i).flat();
          deleted = deleted.filter((f) => !notSent.includes(f));
          break;
        }

        // remove failed files from list
        if (e instanceof AppError) {
          const err = e.aggregatedError;
          deleted = deleted.filter((f) => !group.includes(f) || !err || !err[f.path]);
        }
      }
    }

    dispatch(setFileDeleteModal({ index, value: [false, files, undefined, true] }));

    await dispatch(processFileListDiff(FileManagerIndex.main, deleted));
    await dispatch(processFileListDiff(FileManagerIndex.selector, deleted, false));
    // Fylvo: after a refusal in a connected storage the list shows the state at the provider.
    if (connected && failed) {
      dispatch(refreshFileList(index));
    }

    dispatch(updateUserCapacity(index));
  };
}

function processFileListDiff(index: number, deleted: FileResponse[], refreshIfNeeded: boolean = true): AppThunk {
  return async (dispatch, getState) => {
    const fm = getState().fileManager[index];
    let potentialParents: string[] = [];
    dispatch(removeTreeCache({ index, value: deleted.map((f) => f.path) }));
    if (!fm.path) {
      return;
    }
    let newNavigatePath = fm.path;

    // Find all parent paths of current
    const currentUri = new CrUri(fm.path);
    const elements = currentUri.elements();
    const base = currentUri.base();
    potentialParents.push(base);

    elements.forEach((_p, index) => {
      potentialParents.push(new CrUri(base).join(...elements.slice(0, index + 1)).toString());
    });

    deleted.forEach((f) => {
      // Filter out potential parents
      const index = potentialParents.findIndex((p) => f.path == p);
      if (index > 0) {
        potentialParents.splice(index);
      }
    });
    if (potentialParents.length > 0) {
      newNavigatePath = potentialParents[potentialParents.length - 1];
    }

    const newNavigateUri = new CrUri(newNavigatePath);
    const isCursorPagination = fm.list?.pagination.is_cursor;
    if (isCursorPagination) {
      // remove deleted files from selected
      const newSelected = { ...fm.selected };
      Object.keys(newSelected).forEach((p) => {
        if (deleted.find((f) => f.path === p)) {
          delete newSelected[p];
        }
      });
      dispatch(setSelected({ index, value: Object.values(newSelected) }));

      // For cursor pagination, Remove file from file list
      const newList = fm.list?.files.filter((f) => !deleted.find((d) => d.path === f.path));
      if (newList) {
        dispatch(setFileList({ index, value: newList }));
      }
    } else {
      // For offset pagination, refresh file list
      if (refreshIfNeeded && newNavigateUri.pure_uri().toString() == currentUri.pure_uri().toString()) {
        dispatch(refreshFileList(index));
      }
    }

    // Retreat to parent if current path is deleted
    if (
      refreshIfNeeded &&
      newNavigateUri.pure_uri().toString() != currentUri.pure_uri().toString() &&
      newNavigatePath
    ) {
      dispatch(navigateToPath(index, newNavigatePath));
    }
  };
}

export function processDnd(index: number, src: DragItem, dst: DropResult): AppThunk {
  return async (dispatch, getState) => {
    if (!dst.uri) return;

    const fm = getState().fileManager[index];
    const srcFiles = [
      src.target,
      ...(src.includeSelected ? Object.values(fm.selected).filter((f) => f.path != src.target.path) : []),
    ];

    const isCopy = dst.dropEffect == DropEffect.copy;
    return dispatch(moveFiles(index, srcFiles, dst.uri, isCopy));
  };
}

// ignore if exist src file that is equal to dst file, or the parent of dst file
// TODO: validate bypass by share
function checkMovePrecondition(srcFiles: FileResponse[], dst: string) {
  return srcFiles.some((src) => {
    if (src.path === dst || getFileLinkedUri(src) == dst) return true;
    if (dst.startsWith(src.path + "/")) {
      enqueueSnackbar({
        message: i18next.t("application:modals.cannotMoveCopyToChild"),
        variant: "warning",
        action: DefaultCloseAction,
      });
      return true;
    }
    const srcUri = new CrUri(src.path);
    if (srcUri.parent().toString() == dst) {
      return true;
    }
  });
}

// Fylvo: the row of a folder the file manager has listed (the open folder or one in its tree),
// for the id of a move's destination.
const listedFolder = (fm: SingleManager, path: string): FileResponse | undefined => {
  const parent = fm.list?.parent;
  if (parent && (parent.path == path || fm.pure_path == path)) {
    return parent;
  }
  return fm.tree[path]?.file;
};

export function moveFiles(index: number, src: FileResponse[], dst: string, isCopy: boolean): AppThunk {
  return async (dispatch, getState) => {
    const dstUri = new CrUri(dst);
    if (
      !src.find((f) => f.metadata && f.metadata[Metadata.restore_uri]) &&
      !src.some((f) => inConnectedStorage(f.path)) &&
      dstUri.fs() == Filesystem.trash &&
      dstUri.elements().length == 0
    ) {
      return dispatch(deleteFile(index, src));
    }

    if (checkMovePrecondition(src, dst)) {
      return;
    }

    if (!canCopyMoveTo(src, dst, isCopy)) {
      enqueueSnackbar({
        message: i18next.t("application:modals.cannotPerformAction"),
        variant: "warning",
        action: DefaultCloseAction,
      });
      return;
    }

    let success = true;
    // Fylvo: a connected storage takes at most 100 entries per request, each with its row id,
    // and the destination's id when the list knows it.
    const connected = inConnectedStorage(dst);
    const groups = connected ? inGroups(src, connectedBatchSize) : [src];
    const dstFile = connected ? listedFolder(getState().fileManager[index], dst) : undefined;
    try {
      await longRunningTaskWithSnackbar(
        (async () => {
          // A partial failure of one group (shown by the request) does not stop the next ones.
          let partial: unknown;
          for (const group of groups) {
            try {
              await dispatch(
                sendMoveFile({ uris: group.map((f) => f.path), dst, copy: isCopy, ids: connectedIds(group, dstFile) }),
              );
            } catch (e) {
              if (!(connected && e instanceof AppError && e.aggregatedError)) {
                throw e;
              }
              partial ??= e;
            }
          }
          if (partial) {
            throw partial;
          }
        })(),
        isCopy ? "application:modals.processingCopying" : "application:modals.processingMoving",
      );
    } catch (e) {
      success = false;
    }

    if (isCopy) {
      if (dst == getState().fileManager[index].path) dispatch(refreshFileList(index));
    } else if (success) {
      dispatch(processFileListDiff(index, src));
    } else if (connected) {
      // Fylvo: part of a move may have happened; the list shows the state at the provider.
      dispatch(refreshFileList(index));
    }

    if (success) {
      enqueueSnackbar({
        message: i18next.t(isCopy ? "application:modals.copySuccess" : "application:modals.moveSuccess", {
          num: src.length,
        }),
        variant: "success",
        action: ViewDstAction(dst),
      });
    }
  };
}

export function restoreFile(index: number, src: FileResponse[]): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));

    let success = true;
    try {
      await longRunningTaskWithSnackbar(
        dispatch(sendRestoreFile({ uris: src.map((f) => f.path) })),
        "application:modals.processingRestoring",
      );
    } catch (e) {
      success = false;
    }

    if (success) {
      dispatch(processFileListDiff(index, src));
    }

    if (success) {
      enqueueSnackbar({
        message: i18next.t("application:modals.fileRestored", {
          num: src.length,
        }),
        variant: "success",
      });
    }
  };
}

export function emptyTrash(index: number): AppThunk {
  return async (dispatch, _getState) => {
    try {
      await dispatch(confirmOperation(i18next.t("application:fileManager.emptyTrashConfirm")));
    } catch (e) {
      // user canceled
      return;
    }

    let success = true;
    try {
      await longRunningTaskWithSnackbar(dispatch(sendEmptyTrash()), "application:modals.processingDeletion");
    } catch (e) {
      success = false;
    }

    if (success) {
      await dispatch(refreshFileList(index));
      dispatch(updateUserCapacity(index));
      enqueueSnackbar({
        message: i18next.t("application:fileManager.trashEmptied"),
        variant: "success",
      });
    }
  };
}

export async function longRunningTaskWithSnackbar<T>(
  task: Promise<T>,
  textKey: string,
  action?: SnackbarAction,
  getProgress?: () => number,
): Promise<T> {
  let loadingId: SnackbarKey | undefined = undefined;
  const delayedLoading = setTimeout(() => {
    loadingId = enqueueSnackbar({
      message: i18next.t(textKey),
      variant: "loading",
      persist: true,
      action: action,
      getProgress: getProgress,
    });
  }, loadingDebounceMs);
  let result: T;
  try {
    result = await task;
    clearTimeout(delayedLoading);
    if (loadingId) {
      closeSnackbar(loadingId);
    }
  } catch (e) {
    throw e;
  } finally {
    clearTimeout(delayedLoading);
    if (loadingId) {
      closeSnackbar(loadingId);
    }
  }

  return result;
}

export function patchFileMetadata(index: number, files: FileResponse[], patches: MetadataPatch[]): AppThunk {
  return async (dispatch, _getState) => {
    await dispatch(sendMetadataPatch({ uris: files.map((f) => f.path), patches }));

    dispatch(
      fileUpdated({
        index,
        value: files.map((f) => {
          const newMetadata = { ...f.metadata };
          patches.forEach((patch) => {
            if (patch.remove) {
              delete newMetadata[patch.key];
            } else {
              newMetadata[patch.key] = patch.value ?? "";
            }
          });
          return {
            file: { ...f, metadata: newMetadata },
            oldPath: f.path,
            includeMetadata: true,
          };
        }),
      }),
    );
  };
}

export function patchCustomProp(
  index: number,
  file: FileResponse,
  propId: string,
  value: string,
  remove?: boolean,
): AppThunk<Promise<void>> {
  return async (dispatch, getState) => {
    const patches: MetadataPatch[] = [
      {
        key: `props:${propId}`,
        value,
        remove,
      },
    ];
    const selected = getState().fileManager[index]?.selected ?? {};
    const targets: { [path: string]: FileResponse } = { [file.path]: file };
    for (const path in selected) {
      targets[path] = selected[path];
    }
    await dispatch(patchFileMetadata(index, Object.values(targets), patches));
  };
}

export function applyIconColor(
  index: number,
  files: FileResponse[],
  color?: string,
  clearSelectedFiles?: boolean,
): AppThunk {
  return async (dispatch, _getState) => {
    const patches: MetadataPatch[] = [
      {
        key: Metadata.icon_color,
        value: color,
        remove: !color,
      },
    ];
    try {
      await dispatch(patchFileMetadata(index, files, patches));
    } catch (e) {
      return;
    }

    if (clearSelectedFiles) {
      dispatch(clearSelected({ index, value: undefined }));
    }

    addRecentUsedColor(color, UserSettings.UsedCustomizedIconColors);
  };
}

export function patchTags(index: number, files: FileResponse[], tags: Tag[]): AppThunk {
  return async (dispatch, _getState) => {
    const patches: MetadataPatch[] = tags.map((tag) => ({
      key: Metadata.tag_prefix + tag.key,
      value: tag.color,
    }));

    // find tags that are removed
    const allTags: { [key: string]: boolean } = {};
    const removedTags: { [key: string]: boolean } = {};
    tags.forEach((tag) => {
      allTags[tag.key] = true;
    });
    files.forEach((file) => {
      if (file.metadata) {
        Object.keys(file.metadata).forEach((key: string) => {
          if (key.startsWith(Metadata.tag_prefix)) {
            // trim prefix for key
            const tagKey = key.slice(Metadata.tag_prefix.length);
            // remove tag if not in allTags
            if (!allTags[tagKey]) {
              removedTags[tagKey] = true;
            }
          }
        });
      }
    });

    Object.keys(removedTags).forEach((key) => {
      patches.push({
        key: Metadata.tag_prefix + key,
        remove: true,
      });
    });

    try {
      await dispatch(patchFileMetadata(index, files, patches));
    } catch (e) {
      return;
    }

    addUsedTags(tags);
    // Fylvo: the tag list in the sidebar follows.
    dispatch(loadTags());
  };
}

export function applyIcon(index: number, files: FileResponse[], icon?: string): AppThunk {
  return async (dispatch, _getState) => {
    const patches: MetadataPatch[] = [
      {
        key: Metadata.emoji,
        value: icon,
        remove: !icon,
      },
    ];
    try {
      await dispatch(patchFileMetadata(index, files, patches));
    } catch (e) {
      return;
    }
  };
}

export function openShareDialog(index: number, src: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    dispatch(setShareLinkDialog({ open: true, file: src }));
  };
}

export function dialogBasedMoveCopy(index: number, files: FileResponse[], isCopy: boolean): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    const dst = await dispatch(
      selectPath(isCopy ? PathSelectionVariantOptions.copy : PathSelectionVariantOptions.move),
    );
    dispatch(moveFiles(index, files, dst, !!isCopy));
  };
}

export function createShareShortcut(index: number): AppThunk {
  return async (dispatch, getState) => {
    const fm = getState().fileManager[index];
    const base = fm?.path_root;
    const isSingleFile = fm?.list?.single_file_view;
    const files = fm?.list?.files;
    if (!base || !files) {
      return;
    }

    let shortcutPath = base;
    if (isSingleFile && files && files.length > 0) {
      shortcutPath = files[0].path;
    }

    const dst = await dispatch(selectPath(PathSelectionVariantOptions.shortcut, defaultPath));
    const dstCrUri = new CrUri(dst);
    const shareCrUri = new CrUri(shortcutPath);
    const shareInfo = await dispatch(queueLoadShareInfo(shareCrUri));
    if (!shareInfo.name || !shareInfo.owner) {
      return;
    }

    dstCrUri.join(shareInfo.name);

    await dispatch(
      sendCreateFile({
        type: shareInfo.source_type == FileType.file ? "file" : "folder",
        uri: dstCrUri.toString(),
        metadata: {
          [Metadata.share_redirect]: shortcutPath,
          [Metadata.share_owner]: shareInfo.owner.id,
        },
        err_on_conflict: true,
      }),
    );

    enqueueSnackbar({
      message: i18next.t("application:modals.shortcutCreated"),
      variant: "success",
      action: ViewDstAction(dst),
    });
  };
}

export function goToSharedLink(index: number, file: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    const shareUri = file.metadata?.[Metadata.share_redirect];
    if (shareUri) {
      // Add a delay for animation to close the context menu
      const shareUriParsed = new CrUri(shareUri);
      shareUriParsed.setPath("");
      setTimeout(() => {
        dispatch(navigateToPath(index, shareUriParsed.toString()));
      }, contextMenuCloseAnimationDelay);
    }
  };
}
export function openSidebar(index: number, file: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    dispatch(setSidebar({ open: true, target: file }));
  };
}

export function setFileVersion(index: number, file: FileResponse, version: string): AppThunk<Promise<void>> {
  return async (dispatch, getState) => {
    await dispatch(
      setCurrentVersion({
        uri: file.path,
        version: version,
      }),
    );

    // Size and date now come from the other version: take them from the server. The path stays
    // the one the views know the file by; fields the info call leaves out (capability) stay.
    let fresh: FileResponse;
    try {
      fresh = await dispatch(getFileInfo({ uri: file.path }, true));
    } catch (e) {
      console.warn("Failed to reload file after changing its version", e);
      // Only the version changes; size and date stay as the list has them now.
      const current = getState().fileManager[index].list?.files.find((f) => f.path === file.path) ?? file;
      dispatch(fileUpdated({ index, value: [{ file: { ...current, primary_entity: version }, oldPath: file.path }] }));
      return;
    }

    // Another file took this path in the meantime: leave the views alone.
    if (fresh.id !== file.id) {
      return;
    }

    const updated = { ...file, ...fresh, path: file.path };
    dispatch(fileUpdated({ index, value: [{ file: updated, oldPath: file.path }] }));

    const { sidebarOpen, sidebarTarget } = getState().globalState;
    if (
      sidebarOpen &&
      typeof sidebarTarget === "object" &&
      sidebarTarget?.id === file.id &&
      sidebarTarget.path === file.path
    ) {
      dispatch(setSidebar({ open: true, target: updated }));
    }
  };
}

export function getEntityContent(file: FileResponse, version?: string): AppThunk<Promise<ArrayBuffer>> {
  return async (dispatch, _getState) => {
    const urls = await dispatch(
      getFileEntityUrl({
        uris: [getFileLinkedUri(file)],
        entity: version,
      }),
    );
    try {
      const data = await fetch(urls.urls[0].url);
      if (!data.ok) {
        throw new Error(`Failed to load file, response code ${data.status}`);
      }

      return data.arrayBuffer();
    } catch (e) {
      enqueueSnackbar({
        message: i18next.t("application:fileManager.failedToLoadFile", {
          msg: (e as Error).message,
        }),
        variant: "error",
        action: DefaultCloseAction,
      });
      return new ArrayBuffer(0);
    }
  };
}

export function createNew(index: number, type: string, viewer?: Viewer, template?: NewFileTemplate): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    let newFile: FileResponse | undefined = undefined;
    let newFileName = "";
    if (template) {
      newFileName = i18next.t("fileManager.newFileName", { ext: template.ext });
    }
    try {
      newFile = await dispatch(
        requestCreateNew(
          index,
          type,
          type == CreateNewDialogType.folder ? i18next.t("application:fileManager.newlyCreatedFolder") : newFileName,
        ),
      );
    } catch (e) {
      return;
    }

    if (viewer) {
      dispatch(openViewer(newFile, viewer, newFile.size));
    }
  };
}

export function submitCreateNew(index: number, name: string, type: number): AppThunk<Promise<FileResponse>> {
  return async (dispatch, getState): Promise<FileResponse> => {
    const fm = getState().fileManager[index];
    const uri = fm.pure_path;
    if (!uri) {
      return Promise.reject("No path to create file");
    }

    const crUri = new CrUri(uri);

    const newFile = await dispatch(
      sendCreateFile({
        type: type == FileType.file ? "file" : "folder",
        uri: crUri.join(name).toString(),
        err_on_conflict: true,
      }),
    );

    // if name does not contain "/", append cache
    if (name.includes("/")) {
      dispatch(refreshFileList(index));
      return newFile;
    }

    const newList = fm.list?.files ? [...fm.list.files, newFile] : [newFile];
    if (newList) {
      dispatch(setFileList({ index: FileManagerIndex.main, value: newList }));
      dispatch(setFileList({ index: FileManagerIndex.selector, value: newList }));
    }
    dispatch(appendTreeCache({ index: FileManagerIndex.main, value: [[newFile], uri] }));
    dispatch(appendTreeCache({ index: FileManagerIndex.selector, value: [[newFile], uri] }));
    dispatch(setSelected({ index, value: [newFile] }));
    return newFile;
  };
}

// Fylvo: what a walk left out, so a download can say so instead of looking complete.
export interface WalkSkipped {
  // Paths of rows not taken: shortcuts and folders that cannot be entered in a connected
  // storage, and rows whose name exists more than once there.
  names: string[];
  // Entries the server could not list at all (their names cannot be names here).
  omitted: number;
  // Paths of folders whose listing failed (for example a busy provider).
  unreadable: string[];
  // Paths of entries that only open at the provider and have no file (for example a form).
  providerOnly: string[];
}

export function walk(
  files: FileResponse[],
  callback: (f: FileResponse[], relativePath: string) => Promise<any>,
  walkedSymbolicLinks: string[] = [],
  relativePath: string = "",
  skipped?: WalkSkipped,
  busy?: BusyState,
): AppThunk {
  return async (dispatch, _getState) => {
    const pathOf = (f: FileResponse) => (relativePath == "" ? "" : relativePath + "/") + f.name;
    // Fylvo: a row of a connected storage that cannot be used is left out, as is an entry of
    // the provider that has no file to download (it only opens there).
    const providerOnly = (f: FileResponse) =>
      f.type == FileType.file &&
      f.metadata?.[Metadata.connected_external_url] !== undefined &&
      !f.metadata?.[Metadata.connected_export];
    files.filter(rowUnsupported).forEach((f) => skipped?.names.push(pathOf(f)));
    files.filter((f) => !rowUnsupported(f) && providerOnly(f)).forEach((f) => skipped?.providerOnly.push(pathOf(f)));
    files = files.filter((f) => !rowUnsupported(f) && !providerOnly(f));
    await callback(files, relativePath);

    for (const f of files) {
      if (f.type != FileType.folder) {
        continue;
      }
      // Fylvo: in a connected storage, shortcuts (they can lead back to a folder above) and
      // folders that cannot be entered are not walked into.
      if (!folderEnterable(f) || f.metadata?.[Metadata.connected_link] !== undefined) {
        skipped?.names.push(pathOf(f));
        continue;
      }
      {
        var uri = getFileLinkedUri(f);
        if (f.metadata && f.metadata[Metadata.share_redirect]) {
          if (!walkedSymbolicLinks.includes(uri)) {
            walkedSymbolicLinks.push(uri);
          } else {
            // Ignore symbolic link loop
            continue;
          }
        }
        try {
          const allChildren: FileResponse[] = [];
          let nextToken: string | undefined = undefined;
          let page: number | undefined = undefined;
          let restarted = false;
          let omittedHere = 0;
          while (true) {
            const list = () =>
              dispatch(
                getFileList({
                  next_page_token: nextToken,
                  page,
                  uri,
                  page_size: 1000,
                }),
              );
            // Fylvo: a busy provider of a connected storage is waited for (see providerRetry);
            // other storages keep their own handling.
            let children: ListResponse;
            try {
              children = busy ? await retryBusy(busy, list) : await list();
            } catch (e) {
              // Fylvo: the server no longer keeps the listing of a connected folder (40926):
              // that folder starts again from its first page, once.
              if (!restarted && (nextToken || page !== undefined) && codeOf(e) == ConnectedCode.ListingGone) {
                restarted = true;
                allChildren.length = 0;
                nextToken = undefined;
                page = undefined;
                if (skipped) {
                  skipped.omitted -= omittedHere;
                }
                omittedHere = 0;
                continue;
              }
              throw e;
            }
            allChildren.push(...children.files);
            if (skipped && !nextToken && page === undefined) {
              omittedHere = children.connected?.omitted ?? 0;
              skipped.omitted += omittedHere;
            }
            // Fylvo: a cursor listing (connected storages) goes on only by its token, also
            // when it names a total.
            if (children.pagination.is_cursor) {
              nextToken = children.pagination.next_token;
              if (!nextToken) {
                break;
              }
              continue;
            }
            if (children.pagination.total_items) {
              page = (page ?? 0) + 1;
            } else if (children.pagination.next_token) {
              nextToken = children.pagination.next_token;
            }

            const pageSize = children.pagination?.page_size;
            const totalPages = Math.ceil(
              (children.pagination.total_items ?? 1) / (pageSize && pageSize > 0 ? pageSize : MinPageSize),
            );
            const usePagination = totalPages > 1;
            const loadMore = nextToken || (usePagination && (page ?? 0) < totalPages);

            if (!loadMore) {
              break;
            }
          }
          const path = (relativePath == "" ? "" : relativePath + "/") + f.name;
          await dispatch(walk(allChildren, callback, walkedSymbolicLinks, path, skipped, busy));
        } catch (e) {
          console.warn("Failed to load children", e);
          if (e instanceof Error && e.name == "AbortError") {
            return;
          }
          if (e instanceof ProviderGaveUp) {
            throw e;
          }
          skipped?.unreadable.push(pathOf(f));
        }
      }
    }
  };
}

export interface FileResponseWalked extends FileResponse {
  relativePath: string;
}

export function walkAll(
  files: FileResponse[],
  skipped?: WalkSkipped,
  busy?: BusyState,
): AppThunk<Promise<FileResponseWalked[]>> {
  return async (dispatch, _getState): Promise<FileResponseWalked[]> => {
    const allFiles: FileResponseWalked[] = [];
    await dispatch(
      walk(
        files,
        async (f, relativePath) =>
          allFiles.push(
            ...f.map(
              (file): FileResponseWalked => ({
                ...file,
                relativePath: (relativePath == "" ? "" : relativePath + "/") + file.name,
              }),
            ),
          ),
        [],
        "",
        skipped,
        busy,
      ),
    );
    return allFiles;
  };
}

export function extractArchive(index: number, file: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    dispatch(setExtractArchiveDialog({ open: true, file }));
  };
}

export function newRemoteDownload(index: number, file?: FileResponse): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    dispatch(setRemoteDownloadDialog({ open: true, file }));
  };
}

export function batchGetDirectLinks(index: number, files: FileResponse[]): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    const res = await longRunningTaskWithSnackbar(
      dispatch(startBatchGetDirectLinks(files)),
      "modals.generatingSourceLinks",
    );
    dispatch(setDirectLinkDialog({ open: true, res }));
  };
}

export function resetThumbnails(files: FileResponse[]): AppThunk {
  return async (dispatch, getState) => {
    const thumbConfigLoaded = getState().siteConfig.thumb.loaded;
    if (thumbConfigLoaded != ConfigLoadState.Loaded) {
      await dispatch(loadSiteConfig("thumb"));
    }

    const thumbExts = getState().siteConfig.thumb.config.thumb_exts ?? [];
    const targetFiles = files
      .filter((f) => f.type == FileType.file)
      .filter(
        (f) => f.metadata?.[Metadata.thumbDisabled] !== undefined && thumbExts.includes(fileExtension(f.name) ?? ""),
      );

    if (targetFiles.length === 0) {
      enqueueSnackbar({
        message: i18next.t("application:fileManager.noFileCanResetThumbnail"),
        preventDuplicate: true,
        variant: "warning",
        action: DefaultCloseAction,
      });
      return;
    }

    try {
      // Re-enable thumbnails by removing the disable mark, and update local metadata/cache.
      await dispatch(
        patchFileMetadata(FileManagerIndex.main, targetFiles, [
          {
            key: Metadata.thumbDisabled,
            remove: true,
          },
        ]),
      );

      dispatch(
        removeThumbCache({
          index: FileManagerIndex.main,
          value: targetFiles.map((file) => file.path),
        }),
      );
      // refresh file list
      dispatch(refreshFileList(FileManagerIndex.main));

      // 成功信息
      enqueueSnackbar({
        message: i18next.t("application:fileManager.resetThumbnailRequested"),
        variant: "success",
        action: DefaultCloseAction,
      });
    } catch (_e) {
      // Error snackbar is handled in send()
    }
  };
}

// Single file symbolic links might be invalid if original file is renamed by its owner,
// we need to refresh the symbolic links by getting the latest file list
export function refreshSingleFileSymbolicLinks(file: FileResponse): AppThunk<Promise<FileResponse>> {
  return async (dispatch, _getState) => {
    if (file.type != FileType.file || !file?.metadata?.[Metadata.share_redirect]) {
      return file;
    }
    const currentUrl = new CrUri(getFileLinkedUri(file));
    const latestList = await dispatch(
      getFileList(
        {
          uri: currentUrl.setPath("").toString(),
          page_size: 50,
        },
        false,
      ),
    );
    if (latestList.files.length != 1) {
      return file;
    }
    const latestFile = latestList.files[0];
    if (!latestFile) {
      return file;
    }

    if (latestFile.path != file?.metadata?.[Metadata.share_redirect]) {
      // File renamed, update file share_redirect
      dispatch(
        patchFileMetadata(FileManagerIndex.main, [file], [{ key: Metadata.share_redirect, value: latestFile.path }]),
      );
    }
    return latestFile;
  };
}

function startBatchGetDirectLinks(files: FileResponse[]): AppThunk<Promise<DirectLink[]>> {
  return async (dispatch, _getState): Promise<DirectLink[]> => {
    const allFiles: FileResponse[] = [];
    const currentUser = SessionManager.currentUserGroup();
    const batchLimit = currentUser?.direct_link_batch_size ?? 0;
    await dispatch(
      walk(files, async (children, relativePath) => {
        const childFiles = children.filter((f) => f.type == FileType.file);
        allFiles.push(...childFiles);
        if (allFiles.length > batchLimit) {
          enqueueSnackbar({
            message: i18next.t("modals.sourceBatchSizeExceeded", {
              limit: batchLimit,
            }),
            preventDuplicate: true,
            variant: "warning",
            action: DefaultCloseAction,
          });
          throw new Error("AbortError");
        }
      }),
    );

    // Check if there are any files to generate direct links for
    if (allFiles.length === 0) {
      enqueueSnackbar({
        message: i18next.t("modals.noFileCanGenerateSourceLink"),
        preventDuplicate: true,
        variant: "warning",
        action: DefaultCloseAction,
      });
      throw new Error("AbortError");
    }

    return await dispatch(
      getFileDirectLinks({
        uris: allFiles.map((f) => getFileLinkedUri(f)),
      }),
    );
  };
}
