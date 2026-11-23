import i18next from "i18next";
import { enqueueSnackbar } from "notistack";
import {
  getFileEntityUrl,
  getFileInfo,
  sendCreateUploadSession,
  sendCreateViewerSession,
  sendDeleteUploadSession,
  sendUpdateFile,
  sendUploadChunk,
} from "../../api/api.ts";
import { newBusyState, ProviderGaveUp, retryBusy } from "../../component/FileManager/Connected/providerRetry.ts";
import { FileResponse, Metadata, UploadCredential, Viewer, ViewerAction, ViewerType } from "../../api/explorer.ts";
import { AppError, Code } from "../../api/request.ts";
import { DefaultCloseAction } from "../../component/Common/Snackbar/snackbar.tsx";
import { canUpdate, getActionOpt } from "../../component/FileManager/ContextMenu/useActionDisplayOpt.ts";
import { FileManagerIndex } from "../../component/FileManager/FileManager.tsx";
import SessionManager, { UserSettings } from "../../session";
import { isTrueVal } from "../../session/utils.ts";
import { usableViewers } from "../../component/FileManager/Connected/usableViewers.ts";
import { ConnectedCode, inConnectedStorage } from "../../api/connected.ts";
import { dataUrlToBytes, fileExtension, fileNameNoExt, getFileLinkedUri, sizeToString } from "../../util";
import { base64Encode } from "../../util/base64.ts";
import CrUri, { CrUriPrefix } from "../../util/uri.ts";
import { closeContextMenu, ContextMenuTypes, fileUpdated } from "../fileManagerSlice.ts";
import {
  closeImageEditor,
  setArchiveViewer,
  setCodeViewer,
  setCustomViewer,
  setDrawIOViewer,
  setEpubViewer,
  setExcalidrawViewer,
  setImageEditor,
  setImageViewer,
  setMarkdownViewer,
  setMusicPlayer,
  setPdfViewer,
  setPhotopeaViewer,
  setSearchPopup,
  setSidebar,
  setVideoViewer,
  setViewerSelector,
  setWopiViewer,
} from "../globalStateSlice.ts";
import { Viewers, ViewersByID } from "../siteConfigSlice.ts";
import { AppThunk } from "../store.ts";
import { askSaveAs, askStaleVersionAction } from "./dialog.ts";
import { longRunningTaskWithSnackbar, refreshSingleFileSymbolicLinks } from "./file.ts";
import { refreshFileList, uploadRawFile } from "./filemanager.ts";

export interface ExpandedViewerSetting {
  [key: string]: Viewer[];
}

export const builtInViewers = {
  image: "image",
  photopea: "photopea",
  monaco: "monaco",
  drawio: "drawio",
  markdown: "markdown",
  video: "video",
  pdf: "pdf",
  epub: "epub",
  music: "music",
  excalidraw: "excalidraw",
  archive: "archive",
};

export function openViewers(
  index: number,
  file: FileResponse,
  size?: number,
  preferredVersion?: string,
  ignorePreference?: boolean,
): AppThunk {
  return async (dispatch, getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    const {
      siteConfig: { explorer },
    } = getState();

    const ext = fileExtension(file.name) ?? "";
    const entitySize = size ?? file.size;

    // Try user preference
    const userPreference = SessionManager.get(UserSettings.OpenWithPrefix + ext);
    const usable = (id?: string) => !!id && !!ViewersByID[id] && usableViewers(file, [ViewersByID[id]]).length > 0;
    if (!ignorePreference && usable(userPreference)) {
      dispatch(openViewer(file, ViewersByID[userPreference], entitySize, preferredVersion));
      return;
    }

    const viewerOptions = usableViewers(file, Viewers[ext]);

    if (!ignorePreference && viewerOptions.length == 1) {
      dispatch(openViewer(file, viewerOptions[0], entitySize, preferredVersion));
      return;
    }

    const globalDefaultViewer = explorer?.config?.default_viewer_mapping?.[ext];
    if (!ignorePreference && globalDefaultViewer && usable(globalDefaultViewer)) {
      dispatch(openViewer(file, ViewersByID[globalDefaultViewer], entitySize, preferredVersion));
      return;
    }

    // open viewer selection dialog
    dispatch(
      setViewerSelector({
        open: true,
        file,
        entitySize,
        viewers: viewerOptions,
        version: preferredVersion,
      }),
    );
  };
}

export function openViewer(
  file: FileResponse,
  viewer: Viewer,
  size: number,
  preferredVersion?: string,
  forceNotOpenInNew?: boolean,
): AppThunk {
  return async (dispatch, getState) => {
    // Fylvo: also for a viewer named in the address (?viewer=).
    if (usableViewers(file, [viewer]).length == 0) {
      enqueueSnackbar({ message: i18next.t("common:errors.40920"), variant: "warning", action: DefaultCloseAction });
      return;
    }
    if (!forceNotOpenInNew && viewer.type != ViewerType.custom && isTrueVal(viewer.props?.openInNew ?? "")) {
      const currentUrl = new URL(window.location.href);
      currentUrl.searchParams.set("viewer", viewer.id ?? "");
      currentUrl.searchParams.set("version", preferredVersion ?? "");
      currentUrl.searchParams.set("open", file.id ?? "");
      currentUrl.searchParams.set("size", size.toString());
      window.open(currentUrl.toString(), "_blank");
      return;
    }

    // Warning for large file
    if (viewer.max_size && size > viewer.max_size) {
      enqueueSnackbar({
        message: i18next.t("fileManager.viewerFileSizeWarning", {
          file_size: sizeToString(size),
          max: sizeToString(viewer.max_size),
          app: i18next.t(viewer.display_name),
        }),
        variant: "warning",
        action: DefaultCloseAction,
      });
    }

    const isSharedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    const originalFileId = file.id;

    if (isSharedFile) {
      file = await dispatch(refreshSingleFileSymbolicLinks(file));
    }

    if (viewer.type == ViewerType.builtin) {
      let primaryEntity = file.primary_entity;
      if (isSharedFile) {
        const fileInfo = await dispatch(getFileInfo({ uri: getFileLinkedUri(file) }));
        primaryEntity = fileInfo.primary_entity;
      }
      switch (viewer.id) {
        case builtInViewers.image: {
          // open image viewer
          const fm = getState().fileManager[FileManagerIndex.main];
          const fileIndex = fm.list?.files?.findIndex((f) => f.id == originalFileId);
          dispatch(setSearchPopup(false));
          dispatch(
            setImageViewer({
              open: true,
              index: fileIndex,
              file,
              exts: viewer.exts,
              version: preferredVersion,
            }),
          );
          break;
        }
        case builtInViewers.photopea:
          dispatch(
            setPhotopeaViewer({
              open: true,
              file,
              version: preferredVersion ?? primaryEntity,
            }),
          );
          break;
        case builtInViewers.monaco:
          dispatch(
            setCodeViewer({
              open: true,
              file,
              version: preferredVersion ?? primaryEntity,
            }),
          );
          break;
        case builtInViewers.drawio:
          dispatch(
            setDrawIOViewer({
              open: true,
              file,
              version: preferredVersion,
              host: viewer.props?.host,
            }),
          );
          break;
        case builtInViewers.markdown:
          dispatch(
            setMarkdownViewer({
              open: true,
              file,
              version: preferredVersion ?? primaryEntity,
            }),
          );
          break;
        case builtInViewers.excalidraw:
          dispatch(
            setExcalidrawViewer({
              open: true,
              file,
              version: preferredVersion ?? primaryEntity,
            }),
          );
          break;
        case builtInViewers.video:
          dispatch(
            setVideoViewer({
              open: true,
              file,
              version: preferredVersion,
            }),
          );
          break;
        case builtInViewers.pdf:
          dispatch(
            setPdfViewer({
              open: true,
              file,
              version: preferredVersion,
            }),
          );
          break;
        case builtInViewers.epub:
          dispatch(
            setEpubViewer({
              open: true,
              file,
              version: preferredVersion,
            }),
          );
          break;
        case builtInViewers.archive:
          dispatch(
            setArchiveViewer({
              open: true,
              file,
              version: preferredVersion,
            }),
          );
          break;
        case builtInViewers.music: {
          // open image viewer
          const fm = getState().fileManager[FileManagerIndex.main];
          let fileIndex = -1;
          let files: FileResponse[] = [];
          if (preferredVersion) {
            fileIndex = 0;
            files = [file];
          } else {
            fm.list?.files?.forEach((f) => {
              if (f.id == originalFileId) {
                fileIndex = files.length;
                f = { ...file };
              }

              if (viewer.exts.indexOf(fileExtension(f.name) ?? "") > -1) {
                files.push(f);
              }
            });
          }
          if (fileIndex >= 0) {
            dispatch(
              setMusicPlayer({
                files: files,
                startIndex: fileIndex,
                version: preferredVersion,
              }),
            );
          }
          break;
        }
      }
    } else if (viewer.type == ViewerType.wopi) {
      return dispatch(openWopiViewer(file, viewer, preferredVersion));
    } else if (viewer.type == ViewerType.custom) {
      return dispatch(openCustomViewer(file, viewer, preferredVersion));
    }
  };
}

export function openCustomViewer(file: FileResponse, viewer: Viewer, preferredVersion?: string): AppThunk {
  return async (dispatch, getState) => {
    const entityUrl = await longRunningTaskWithSnackbar(
      dispatch(
        getFileEntityUrl({
          uris: [getFileLinkedUri(file)],
          entity: preferredVersion,
          use_primary_site_url: true,
        }),
      ),
      "fileManager.preparingOpenFile",
    );

    const currentUser = SessionManager.currentUser();

    // Mirrors the fallback in useGeneratedTheme (App.tsx): explicit user
    // preference wins, otherwise fall back to the OS/browser preference.
    const darkModePreference = getState().globalState.darkMode;
    const isDarkMode =
      darkModePreference === undefined
        ? window.matchMedia?.("(prefers-color-scheme: dark)").matches
        : darkModePreference;

    const vars: { [key: string]: string } = {
      src: encodeURIComponent(entityUrl.urls[0].url),
      src_raw: entityUrl.urls[0].url,
      src_raw_base64: base64Encode(entityUrl.urls[0].url),
      name: encodeURIComponent(file.name),
      version: preferredVersion ? preferredVersion : "",
      id: file.id,
      user_id: currentUser?.id ?? "",
      user_display_name: encodeURIComponent(currentUser?.nickname ?? ""),
      theme: isDarkMode ? "dark" : "light",
      dark: isDarkMode ? "1" : "0",
    };

    // replace variables in viewer.url
    let url = viewer.url;
    if (!url) {
      console.error("Viewer URL not set");
      return;
    }
    for (const key in vars) {
      url = url.replace(`{$${key}}`, vars[key]);
    }

    // if url matches custom scheme pattern like nplayer://xxx, use window.location.assign
    if (/^(?!https?:\/\/)[a-zA-Z0-9]+:\/\//.test(url)) {
      window.location.assign(url);
      return;
    }

    if (isTrueVal(viewer.props?.openInNew ?? "")) {
      window.window.open(url);
      return;
    }

    // open viewer
    dispatch(setCustomViewer({ open: true, url, file, version: preferredVersion }));
  };
}

export function openWopiViewer(file: FileResponse, viewer: Viewer, preferredVersion?: string): AppThunk {
  return async (dispatch, _getState) => {
    const displayOpt = getActionOpt([file], Viewers, ContextMenuTypes.file);
    const action = !preferredVersion && canUpdate(displayOpt) ? ViewerAction.edit : ViewerAction.view;

    const viewerSession = await longRunningTaskWithSnackbar(
      dispatch(
        sendCreateViewerSession({
          uri: getFileLinkedUri(file),
          viewer_id: viewer.id,
          preferred_action: action,
          version: preferredVersion,
        }),
      ),
      "fileManager.preparingOpenFile",
    );

    if (!viewerSession.wopi_src) {
      return;
    }
    dispatch(
      setWopiViewer({
        open: true,
        src: viewerSession.wopi_src,
        session: viewerSession.session,
        file: file,
        version: preferredVersion,
      }),
    );
  };
}

export function onImageViewerIndexChange(file: FileResponse): AppThunk {
  return async (dispatch, getState) => {
    const {
      globalState: { sidebarOpen },
    } = getState();
    if (sidebarOpen) {
      dispatch(
        setSidebar({
          open: true,
          target: file,
        }),
      );
    }
  };
}

export function switchToImageEditor(file: FileResponse, version?: string): AppThunk {
  return async (dispatch, getState) => {
    const {
      globalState: { imageViewer },
    } = getState();
    if (!imageViewer) {
      return;
    }

    const isSharedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    if (isSharedFile) {
      const fileInfo = await dispatch(getFileInfo({ uri: getFileLinkedUri(file) }));
      version = fileInfo.primary_entity;
    }

    dispatch(
      setImageViewer({
        ...imageViewer,
        open: false,
      }),
    );

    dispatch(
      setImageEditor({
        open: true,
        file,
        version: version ?? file.primary_entity,
      }),
    );
  };
}

export function switchToImageViewer(): AppThunk {
  return async (dispatch, getState) => {
    const {
      globalState: { imageViewer },
    } = getState();
    dispatch(closeImageEditor());
    if (imageViewer) {
      dispatch(setImageViewer({ ...imageViewer, open: true }));
    }
  };
}

export function saveImage(
  name: string,
  data: string,
  file: FileResponse,
  version?: string,
): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch, getState) => {
    if (!version) {
      version = file.primary_entity;
    }

    const isSharedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    let originFileUri = new CrUri(getFileLinkedUri(file));
    if (name != file.name) {
      if (isSharedFile) {
        // For symbolic link, we need to save to the same folder as the link
        originFileUri = new CrUri(file.path);
      }

      originFileUri = originFileUri.parent().join(name);
    }

    // Fylvo: a copy under a new name in a connected storage replaces nothing.
    const uri = originFileUri.toString();
    const savedImageFile = await dispatch(
      saveFile(uri, await dataUrlToBytes(data), version, false, false, name != file.name),
    );
    if (savedImageFile) {
      const {
        globalState: { imageViewer },
      } = getState();
      if (imageViewer) {
        dispatch(
          setImageViewer({
            ...imageViewer,
            file: name != file.name ? file : savedImageFile,
          }),
        );
      }
    }
    return savedImageFile;
  };
}

// Fylvo: a new file in a connected storage from an editor, through an upload session: it only
// creates, so a taken name is refused (40004, at OneDrive also 40905 for another case), never
// replaced, also when the name is taken while the parts go. Parts go one after another, busy
// answers wait (as for downloads); an unknown outcome reloads the list and keeps the editor's
// content. Errors are told here; undefined when nothing was saved.
function connectedSaveNew(uri: string, data: any): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch) => {
    const blob = data instanceof Blob ? data : new Blob([data]);
    let session: UploadCredential | undefined;
    try {
      session = await dispatch(sendCreateUploadSession({ uri, size: blob.size, policy_id: "" }));
      const id = session.session_id;
      const partSize = session.chunk_size > 0 ? session.chunk_size : Math.max(blob.size, 1);
      const parts = Math.max(1, Math.ceil(blob.size / partSize));
      const busy = newBusyState();
      let file: unknown;
      for (let i = 0; i < parts; i++) {
        const part = blob.slice(i * partSize, (i + 1) * partSize);
        file = await retryBusy(busy, () => dispatch(sendUploadChunk(id, part, i)));
      }
      return file as FileResponse;
    } catch (e) {
      const code = e instanceof AppError ? e.code : undefined;
      if (code == ConnectedCode.OutcomeUnknown) {
        dispatch(refreshFileList(FileManagerIndex.main));
      } else if (session) {
        // Nothing stays open at the server.
        dispatch(sendDeleteUploadSession({ id: session.session_id, uri })).catch(() => undefined);
      }
      const taken = code == ConnectedCode.ObjectExists || code == connectedCaseTaken;
      enqueueSnackbar({
        message: taken
          ? i18next.t("application:connected.nameTaken")
          : e instanceof ProviderGaveUp
            ? i18next.t(e.message)
            : e instanceof Error
              ? e.message
              : String(e),
        variant: "warning",
        action: DefaultCloseAction,
      });
      return undefined;
    }
  };
}

// A name that differs only in case from one that is there (OneDrive).
const connectedCaseTaken = 40905;

export function saveFile(
  uri: string,
  data: any,
  version?: string,
  saveAsNew?: boolean,
  ignoreSnackbar?: boolean,
  // Fylvo: saved under a name the user chose (as saveAsNew, without asking for it here); in a
  // connected storage that name must be free.
  asNew?: boolean,
): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch, _getState): Promise<FileResponse | undefined> => {
    let savedFile: FileResponse | undefined;
    if (saveAsNew) {
      try {
        const fileName = new CrUri(uri).elements().pop();
        if (fileName) {
          const saveAsDst = await dispatch(askSaveAs(fileName));
          const dst = new CrUri(saveAsDst.uri).join(saveAsDst.name);
          uri = dst.toString();
        }
      } catch (e) {
        return;
      }
    }
    // Fylvo: under a chosen name in a connected storage the file goes through an upload
    // session, which only creates: a taken name is refused there, never replaced.
    if (inConnectedStorage(uri) && (saveAsNew || asNew)) {
      savedFile = await dispatch(connectedSaveNew(uri, data));
    } else {
      // Fylvo: a connected storage keeps no empty replacement of a file; say so instead of
      // sending it (the server would refuse it).
      const size = data instanceof Blob ? data.size : typeof data == "string" ? data.length : data?.byteLength;
      if (inConnectedStorage(uri) && size === 0) {
        enqueueSnackbar({
          message: i18next.t("application:connected.emptySave"),
          variant: "warning",
          action: DefaultCloseAction,
        });
        return;
      }
      try {
        savedFile = await dispatch(
          sendUpdateFile(
            {
              uri: uri,
              previous: version,
              // Fylvo: saving a file of a connected storage never brings back a deleted one
              // (also when "overwrite" sends no version).
              previous_required: inConnectedStorage(uri) ? true : undefined,
            },
            data,
          ),
        );
      } catch (e) {
        if (e instanceof AppError && e.code == Code.StaleVersion) {
          // Handle version conflict
          try {
            const opt = await dispatch(askStaleVersionAction(uri));
            if (opt.overwrite) {
              return await dispatch(saveFile(uri, data));
            } else if (opt.saveAs) {
              return await dispatch(saveFile(opt.saveAs, data, version, false, false, true));
            }
          } catch (e) {
            // Cancel save action
            throw e;
          }
        }
        // Fylvo: the save may have happened at the provider; the list shows what is there.
        if (e instanceof AppError && e.code == ConnectedCode.OutcomeUnknown && inConnectedStorage(uri)) {
          dispatch(refreshFileList(FileManagerIndex.main));
        }
      }
    }

    if (!savedFile) {
      return;
    }

    if (!ignoreSnackbar) {
      enqueueSnackbar({
        message: i18next.t("fileManager.fileSaved"),
        variant: "success",
        action: DefaultCloseAction,
      });
    }

    dispatch(
      fileUpdated({
        index: FileManagerIndex.main,
        value: [
          {
            oldPath: uri,
            file: savedFile,
          },
        ],
      }),
    );

    return savedFile;
  };
}

export function savePhotopea(data: ArrayBuffer, file: FileResponse, version?: string, saveAsNew?: boolean): AppThunk {
  return async (dispatch, getState) => {
    if (!version) {
      version = file.primary_entity;
    }
    const savedFile = await dispatch(saveFile(getFileLinkedUri(file), data, version, saveAsNew));

    if (savedFile) {
      const {
        globalState: { photopeaViewer },
      } = getState();
      // Fylvo: only while the editor still holds the file this save started from.
      if (photopeaViewer && photopeaViewer.file.path == file.path) {
        dispatch(
          setPhotopeaViewer({
            ...photopeaViewer,
            file: savedFile,
            version: savedFile.primary_entity,
          }),
        );
      }
    }
  };
}

export function saveCode(
  data: string,
  file: FileResponse,
  version?: string,
  saveAsNew?: boolean,
): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch, getState) => {
    const isLinkedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    if (!version && !isLinkedFile) {
      version = file.primary_entity;
    }
    const savedFile = await dispatch(saveFile(getFileLinkedUri(file), data, version, saveAsNew));

    if (savedFile) {
      const {
        globalState: { codeViewer },
      } = getState();
      // Fylvo: only while the editor still holds the file this save started from.
      if (codeViewer && codeViewer.file.path == file.path) {
        dispatch(
          setCodeViewer({
            ...codeViewer,
            file: savedFile,
            version: savedFile.primary_entity,
          }),
        );
      }
    }
    return savedFile;
  };
}

export function saveDrawIO(
  data: string,
  file: FileResponse,
  saveAsNew?: boolean,
): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch, getState): Promise<FileResponse | undefined> => {
    // Fylvo: the file the editor holds now: its messages may still name the file as it was
    // opened, before saves and saves under a new name. In a connected storage only over the
    // version saved last.
    const target = getState().globalState.drawIOViewer?.file ?? file;
    const version = inConnectedStorage(target.path) ? target.primary_entity : undefined;
    const savedFile = await dispatch(saveFile(getFileLinkedUri(target), data, version, saveAsNew, true));

    if (savedFile) {
      const {
        globalState: { drawIOViewer },
      } = getState();
      // Fylvo: only while the editor still holds the file this save started from.
      if (drawIOViewer && drawIOViewer.file.path == target.path) {
        dispatch(
          setDrawIOViewer({
            ...drawIOViewer,
            file: savedFile,
          }),
        );
      }
    }

    return savedFile;
  };
}

export function saveExcalidraw(
  data: string,
  file: FileResponse,
  version?: string,
  saveAsNew?: boolean,
): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch, getState) => {
    const isLinkedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    if (!version && !isLinkedFile) {
      version = file.primary_entity;
    }
    const savedFile = await dispatch(saveFile(getFileLinkedUri(file), data, version, saveAsNew));

    if (savedFile) {
      const {
        globalState: { excalidrawViewer },
      } = getState();
      // Fylvo: only while the editor still holds the file this save started from.
      if (excalidrawViewer && excalidrawViewer.file.path == file.path) {
        dispatch(
          setExcalidrawViewer({
            ...excalidrawViewer,
            file: savedFile,
            version: savedFile.primary_entity,
          }),
        );
      }
    }
    return savedFile;
  };
}

export function saveMarkdown(
  data: string,
  file: FileResponse,
  version?: string,
  saveAsNew?: boolean,
): AppThunk<Promise<FileResponse | undefined>> {
  return async (dispatch, getState) => {
    const isLinkedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    if (!version && !isLinkedFile) {
      version = file.primary_entity;
    }
    const savedFile = await dispatch(saveFile(getFileLinkedUri(file), data, version, saveAsNew));

    if (savedFile) {
      const {
        globalState: { markdownViewer },
      } = getState();
      // Fylvo: only while the editor still holds the file this save started from.
      if (markdownViewer && markdownViewer.file.path == file.path) {
        dispatch(
          setMarkdownViewer({
            ...markdownViewer,
            file: savedFile,
            version: savedFile.primary_entity,
          }),
        );
      }
    }
    return savedFile;
  };
}

const subtitleSuffix = ["ass", "srt", "vrr"];
export function findSubtitleOptions(): AppThunk<FileResponse[]> {
  return (_dispatch, getState): FileResponse[] => {
    const {
      globalState: { videoViewer },
      fileManager,
    } = getState();
    if (!videoViewer || !videoViewer.file) {
      return [];
    }

    const fm = fileManager[FileManagerIndex.main];

    const fileNameMatch = fileNameNoExt(videoViewer.file.name) + ".";
    const options = fm.list?.files
      .filter((f) => {
        return subtitleSuffix.indexOf(fileExtension(f.name) ?? "") !== -1;
      })
      .sort((a, b) => {
        return a.name.startsWith(fileNameMatch) && !b.name.startsWith(fileNameMatch) ? -1 : 0;
      });

    return options ?? [];
  };
}

const BROKEN_IMG_URI =
  "data:image/svg+xml;charset=utf-8," +
  encodeURIComponent(/* xml */ `
    <svg id="imgLoadError" xmlns="http://www.w3.org/2000/svg" width="100" height="100">
      <rect x="0" y="0" width="100" height="100" fill="none" stroke="red" stroke-width="4" stroke-dasharray="4" />
      <text x="50" y="55" text-anchor="middle" font-size="20" fill="red">⚠️</text>
    </svg>
`);

export function markdownImagePreviewHandler(imageSource: string, mdFileUri: string): AppThunk<Promise<string>> {
  return async (dispatch, getState) => {
    // For URl, return the image source
    if (imageSource.startsWith("http://") || imageSource.startsWith("https://")) {
      return imageSource;
    }

    let uri = new CrUri(mdFileUri)?.parent();
    if (imageSource.startsWith(CrUriPrefix)) {
      uri = new CrUri(imageSource);
    } else if (uri) {
      uri = uri.join_raw(imageSource);
    } else {
      return imageSource;
    }

    try {
      const file = await dispatch(getFileInfo({ uri: uri.toString() }, true));
      const fileUrl = await dispatch(getFileEntityUrl({ uris: [getFileLinkedUri(file)] }));
      return fileUrl.urls[0].url;
    } catch (e) {
      return BROKEN_IMG_URI;
    }
  };
}

export function markdownImageAutocompleteSuggestions(): AppThunk<string[] | null> {
  return (_dispatch, getState) => {
    const files = getState().fileManager[FileManagerIndex.main]?.list?.files;
    if (!files) {
      return null;
    }

    const suggestions = files.filter((f) => {
      const ext = fileExtension(f.name);
      return ViewersByID[builtInViewers.image]?.exts.indexOf(ext ?? "") !== -1;
    });

    return suggestions.map((f) => f.name);
  };
}

export function uploadMarkdownImage(file: File): AppThunk<Promise<string>> {
  return async (dispatch, getState) => {
    const task = await dispatch(uploadRawFile(file));
    return task.name;
  };
}
