import dayjs from "dayjs";
import i18next from "i18next";
import { closeSnackbar, enqueueSnackbar } from "notistack";
import streamSaver from "streamsaver";
import { getFileEntityUrl } from "../../api/api.ts";
import { FileResponse, FileType, Metadata } from "../../api/explorer.ts";
import { GroupPermission } from "../../api/user.ts";
import {
  DefaultCloseAction,
  BatchDownloadProgressAction,
  BatchDownloadSecondaryAction,
  BatchDownloadCompleteAction,
  BatchDownloadIncompleteAction,
} from "../../component/Common/Snackbar/snackbar.tsx";
import SessionManager from "../../session";
import { getFileLinkedUri } from "../../util";
import Boolset from "../../util/boolset.ts";
import { formatLocalTime } from "../../util/datetime.ts";
import {
  getFileSystemDirectoryPaths,
  streamFileToFileSystemDirectory,
  verifyFileSystemRWPermission,
} from "../../util/filesystem.ts";
import "../../util/zip.js";
import { closeContextMenu } from "../fileManagerSlice.ts";
import { DialogSelectOption, setBatchDownloadLog, setBatchDownloadProgress } from "../globalStateSlice.ts";
import { AppThunk } from "../store.ts";
import { confirmOperation, promiseId, selectOption } from "./dialog.ts";
import {
  FileResponseWalked,
  longRunningTaskWithSnackbar,
  refreshSingleFileSymbolicLinks,
  walk,
  walkAll,
  WalkSkipped,
} from "./file.ts";
import { downloadFileName, getConnectedFileUrl, inConnectedStorage } from "../../api/connected.ts";
import {
  askAboveFiles,
  errorMessage,
  fetchContent,
  newBusyState,
  ProviderGaveUp,
  retryBusy,
} from "../../component/FileManager/Connected/providerRetry.ts";

enum MultipleDownloadOption {
  Browser,
  StreamSaver,
  Backend,
}

enum DownloadOverwriteOption {
  Skip,
  Overwrite,
  OverwriteAll,
  SkipAll,
}

export function downloadFiles(index: number, files: FileResponse[]): AppThunk {
  return async (dispatch, _getState) => {
    dispatch(closeContextMenu({ index, value: undefined }));
    if (files.length == 1 && files[0].type == FileType.file) {
      await dispatch(downloadSingleFile(files[0]));
    } else {
      await dispatch(downloadMultipleFiles(files));
    }
  };
}

export function downloadMultipleFiles(files: FileResponse[]): AppThunk {
  return async (dispatch, _getState) => {
    // Prepare download options
    const options: MultipleDownloadOption[] = [MultipleDownloadOption.StreamSaver];
    // @ts-ignore
    if (window.isSecureContext && window.showDirectoryPicker) {
      options.push(MultipleDownloadOption.Browser);
    }

    const groupPermission = new Boolset(SessionManager.currentUser()?.group?.permission);
    if (
      groupPermission.enabled(GroupPermission.archive_download) &&
      (files.length > 1 || !files[0].metadata?.[Metadata.share_redirect]) &&
      // Fylvo: the server builds no archives from a connected storage.
      !files.some((f) => inConnectedStorage(f.path))
    ) {
      options.push(MultipleDownloadOption.Backend);
    }

    let finalOption = options[0];
    if (options.length > 1) {
      try {
        finalOption = (await dispatch(
          selectOption(getDownloadSelectOption(options), "fileManager.selectArchiveMethod"),
        )) as MultipleDownloadOption;
      } catch (e) {
        // User cancel selection
        return;
      }
    }

    if (finalOption == MultipleDownloadOption.Backend) {
      await dispatch(backendBatchDownload(files));
    } else if (finalOption == MultipleDownloadOption.Browser) {
      await dispatch(browserBatchDownload(files));
    } else {
      await dispatch(streamSaverDownload(files));
    }
  };
}

export function backendBatchDownload(files: FileResponse[]): AppThunk {
  return async (dispatch, _getState) => {
    const downloadUrl = await longRunningTaskWithSnackbar(
      dispatch(
        getFileEntityUrl({
          uris: files.map((f) => getFileLinkedUri(f)),
          archive: true,
        }),
      ),
      "application:fileManager.preparingBathDownload",
    );

    window.location.assign(downloadUrl.urls[0].url);
  };
}

export const cancelSignals: {
  [key: string]: AbortController;
} = {};

export function browserBatchDownload(files: FileResponse[]): AppThunk {
  return async (dispatch, getState) => {
    const downloadId = promiseId();
    cancelSignals[downloadId] = new AbortController();

    // Select download folder
    let handle: FileSystemDirectoryHandle;
    if (!window.showDirectoryPicker || !window.isSecureContext) {
      return;
    }
    try {
      // can't use suggestedName for showDirectoryPicker (only available showSaveFilePicker)
      handle = await window.showDirectoryPicker({
        startIn: "downloads",
        mode: "readwrite",
      });
      // we should obtain the readwrite permission for the directory at first
      if (!(await verifyFileSystemRWPermission(handle))) {
        enqueueSnackbar({
          message: i18next.t("application:fileManager.directoryDownloadPermissionError"),
          variant: "error",
        });
        throw new Error(i18next.t("application:fileManager.directoryDownloadPermissionError"));
      }
    } catch (e) {
      return;
    }

    dispatch(
      setBatchDownloadProgress({
        id: downloadId,
        progress: {
          isDownloading: true,
          filesCompleted: 0,
          totalFiles: 0,
          totalBytes: 0,
          totalExpectedBytes: 0,
          currentFile: "",
          speed: 0,
        },
      }),
    );

    const beforeUnloadHandler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnloadHandler);

    const snackbarId = enqueueSnackbar({
      message: i18next.t("fileManager.batchDownloadStarted"),
      variant: "loading",
      persist: true,
      action: BatchDownloadProgressAction(downloadId),
      secondaryAction: BatchDownloadSecondaryAction(downloadId),
      getProgress: () => {
        const progress = getState().globalState.batchDownloadProgress?.[downloadId];
        if (!progress || progress.totalExpectedBytes === 0) return 0;
        return Math.min(100, Math.round((progress.totalBytes / progress.totalExpectedBytes) * 100));
      },
    });

    // Track cancellation locally because walk() catches and swallows AbortError
    // rather than re-throwing it, so the download resolves normally after cancel.
    let cancelled = false;
    const origAbort = cancelSignals[downloadId].abort.bind(cancelSignals[downloadId]);
    cancelSignals[downloadId].abort = () => {
      cancelled = true;
      origAbort();
    };

    try {
      const complete = await dispatch(startBrowserBatchDownloadTo(handle, downloadId, files));
      if (complete === null) {
        cancelled = true;
      }
      dispatch(setBatchDownloadProgress({ id: downloadId, progress: { isDownloading: false } }));
      closeSnackbar(snackbarId);
      if (!cancelled && complete) {
        enqueueSnackbar({
          message: i18next.t("fileManager.downloadComplete"),
          variant: "success",
          autoHideDuration: 5000,
          action: BatchDownloadCompleteAction(downloadId),
        });
      } else if (!cancelled) {
        // Fylvo: something is missing; the log names it.
        enqueueSnackbar({
          message: i18next.t("application:connected.downloadIncomplete"),
          variant: "warning",
          persist: true,
          action: BatchDownloadIncompleteAction(downloadId),
        });
      } else {
        enqueueSnackbar({
          message: i18next.t("modals.directoryDownloadCancelled"),
          variant: "warning",
          autoHideDuration: 3000,
          action: DefaultCloseAction,
        });
      }
    } catch (e) {
      closeSnackbar(snackbarId);
      dispatch(setBatchDownloadProgress({ id: downloadId, progress: { isDownloading: false } }));
      // Fylvo: the provider of a connected storage stayed busy; the log says what came through.
      if (e instanceof ProviderGaveUp) {
        enqueueSnackbar({
          message: i18next.t(e.message),
          variant: "error",
          persist: true,
          action: BatchDownloadIncompleteAction(downloadId),
        });
      } else if (cancelled) {
        enqueueSnackbar({
          message: i18next.t("modals.directoryDownloadCancelled"),
          variant: "warning",
          autoHideDuration: 3000,
          action: DefaultCloseAction,
        });
      }
    } finally {
      window.removeEventListener("beforeunload", beforeUnloadHandler);
    }
  };
}

function startBrowserBatchDownloadTo(
  handle: FileSystemDirectoryHandle,
  downloadId: string,
  files: FileResponse[],
): AppThunk<Promise<boolean | null>> {
  // Resolves true when everything was downloaded, null when the user declined to start.
  return async (dispatch, _getState): Promise<boolean | null> => {
    let log = "";
    let failed = 0;
    let skipAll = false;
    let overwriteAll = false;
    let filesCompleted = 0;
    let totalBytes = 0;
    let totalFiles = 0;
    let totalExpectedBytes = 0;

    // Speed tracking
    let lastBytes = 0;
    let lastTime = Date.now();
    const speedSamples: number[] = [];

    let lastDispatchTime = 0;
    let currentFile = "";

    const appendLog = (msg: string) => {
      log = log + msg + "\n";
      dispatch(setBatchDownloadLog({ id: downloadId, logs: log }));
    };

    const dispatchProgress = () => {
      const speed = speedSamples.length > 0 ? speedSamples.reduce((a, b) => a + b, 0) / speedSamples.length : 0;

      dispatch(
        setBatchDownloadProgress({
          id: downloadId,
          progress: {
            filesCompleted,
            totalFiles,
            totalBytes,
            totalExpectedBytes,
            currentFile,
            speed,
            isDownloading: true,
          },
        }),
      );
      lastDispatchTime = Date.now();
    };

    const updateProgress = (force?: boolean) => {
      // Calculate speed (rolling 5-sample average, sampled every 500ms)
      const now = Date.now();
      const elapsed = (now - lastTime) / 1000;
      if (elapsed >= 0.5) {
        const recentBytes = totalBytes - lastBytes;
        const instantSpeed = recentBytes / elapsed;
        speedSamples.push(instantSpeed);
        if (speedSamples.length > 5) speedSamples.shift();
        lastBytes = totalBytes;
        lastTime = now;
      }

      // Throttle dispatches to at most every 500ms unless forced (file complete, etc.)
      if (force || now - lastDispatchTime >= 500) {
        dispatchProgress();
      }
    };

    // Fylvo: busy answers of a connected storage's provider, counted over the whole download.
    // Other storages keep their own handling: one failing file never stops the rest.
    const connected = files.some((f) => inConnectedStorage(f.path));
    const busy = connected ? newBusyState(cancelSignals[downloadId].signal) : undefined;
    const fetchFile = (url: string) => {
      const signal = cancelSignals[downloadId].signal;
      return busy ? retryBusy(busy, () => fetchContent(url, signal)) : fetchContent(url, signal);
    };
    const skipped: WalkSkipped = { names: [], omitted: 0, unreadable: [] };
    // Fylvo: the provider stayed busy or the storage needs a reconnect: the log says so, and
    // names what was left out up to here.
    const logStop = (e: unknown) => {
      if (e instanceof ProviderGaveUp) {
        appendLog(i18next.t(e.message));
        skippedNotes(skipped).forEach(appendLog);
      }
    };

    // Walk the tree once: count files and bytes, and keep the rows of each folder for the
    // download (Fylvo: so a connected storage is listed only once, and what was counted and
    // asked about is what gets downloaded).
    const folders: { children: FileResponse[]; relativePath: string }[] = [];
    try {
      await dispatch(
        walk(
          files,
          async (children, relativePath) => {
            folders.push({ children, relativePath });
            const childFiles = children.filter((f) => f.type == FileType.file);
            totalFiles += childFiles.length;
            totalExpectedBytes += childFiles.reduce((sum, f) => sum + (f.size || 0), 0);
          },
          [],
          "",
          skipped,
          busy,
        ),
      );
    } catch (e) {
      logStop(e);
      throw e;
    }
    // The walk returns quietly when cancelled.
    if (cancelSignals[downloadId].signal.aborted) {
      appendLog(i18next.t("modals.directoryDownloadCancelled"));
      return null;
    }

    // Fylvo: many files from a connected storage take a while; ask first.
    if (connected && totalFiles >= askAboveFiles) {
      try {
        await dispatch(confirmOperation(i18next.t("application:connected.manyFiles", { count: totalFiles })));
      } catch (e) {
        appendLog(i18next.t("modals.directoryDownloadCancelled"));
        return null;
      }
    }

    updateProgress(true);

    // get the files in the directory to compare with queue files
    const fsPaths = await getFileSystemDirectoryPaths(handle, "");

    const downloadFolder = async (children: FileResponse[], relativePath: string) => {
      const childFiles = children.filter((f) => f.type == FileType.file);
      // Names in this folder, so an exported document does not take the name of another file.
      const takenNames = new Set(children.map((f) => f.name.toLowerCase()));
      try {
        // Fylvo: in a connected storage each address is asked for right before its file:
        // it is valid for about an hour and only for the version of that moment.
        const oneByOne = childFiles.some((f) => inConnectedStorage(f.path));
        const entityUrls = oneByOne
          ? undefined
          : await dispatch(
              getFileEntityUrl({
                uris: childFiles.map((f) => getFileLinkedUri(f)),
                download: true,
                skip_error: true,
              }),
            );

        for (let i = 0; i < childFiles.length; i++) {
          // A cancel during the destination scan or a question ends here, before any further one.
          if (cancelSignals[downloadId].signal.aborted) {
            throw new DOMException("Aborted", "AbortError");
          }
          const name =
            (relativePath == "" ? "" : relativePath + "/") +
            downloadFileName(childFiles[i], childFiles[i].name, takenNames);
          if (fsPaths.has(name)) {
            if (skipAll) {
              appendLog(
                i18next.t("modals.directoryDownloadSkipNotifiction", {
                  name,
                }),
              );
              filesCompleted++;
              updateProgress(true);
              continue;
            }

            if (overwriteAll) {
              appendLog(
                i18next.t("modals.directoryDownloadReplaceNotifiction", {
                  name,
                }),
              );
            } else {
              // No overwrite options, ask for one
              let overwriteOption = DownloadOverwriteOption.Skip;
              try {
                overwriteOption = (await dispatch(
                  selectOption(getDownloadOverwriteOption(name), "fileManager.selectDirectoryDuplicationMethod"),
                )) as DownloadOverwriteOption;
              } catch (e) {
                // User cancel, use skip option
                overwriteOption = DownloadOverwriteOption.Skip;
              }

              if (overwriteOption == DownloadOverwriteOption.Skip) {
                appendLog(
                  i18next.t("modals.directoryDownloadSkipNotifiction", {
                    name,
                  }),
                );
                filesCompleted++;
                updateProgress(true);
                continue;
              } else if (overwriteOption == DownloadOverwriteOption.SkipAll) {
                appendLog(
                  i18next.t("modals.directoryDownloadSkipNotifiction", {
                    name,
                  }),
                );
                skipAll = true;
                filesCompleted++;
                updateProgress(true);
                continue;
              } else if (overwriteOption == DownloadOverwriteOption.OverwriteAll) {
                appendLog(
                  i18next.t("modals.directoryDownloadReplaceNotifiction", {
                    name,
                  }),
                );
                overwriteAll = true;
              } else {
                appendLog(
                  i18next.t("modals.directoryDownloadReplaceNotifiction", {
                    name,
                  }),
                );
              }
            }
          }

          let url = entityUrls?.urls[i]?.url;
          let urlError = "failed to get download url";
          if (oneByOne) {
            try {
              url = await retryBusy(busy!, () => dispatch(getConnectedFileUrl(getFileLinkedUri(childFiles[i]))));
            } catch (e) {
              if (e instanceof ProviderGaveUp || (e instanceof Error && e.name == "AbortError")) {
                throw e;
              }
              urlError = errorMessage(e);
            }
          }
          // Fylvo: an address the server could not give comes back empty.
          if (!url) {
            appendLog(
              i18next.t("modals.directoryDownloadErrorNotification", {
                name: childFiles[i].name,
                msg: urlError,
              }),
            );
            failed++;
            filesCompleted++;
            updateProgress(true);
            continue;
          }
          appendLog(i18next.t("modals.directoryDownloadStarted", { name }));
          currentFile = name;

          updateProgress(true);
          try {
            // Fylvo: an error answer must not become the file; a busy provider is waited for.
            const res = await fetchFile(url!);

            // Stream the response directly to disk to avoid buffering
            // the entire file in memory
            const reader = res.body?.getReader();
            if (!reader) {
              throw new Error("Response body is not readable");
            }

            try {
              await streamFileToFileSystemDirectory(handle, reader, name, (bytes) => {
                totalBytes += bytes;
                updateProgress();
              });
            } catch (e) {
              // Fylvo: a failed local write ends the transfer too, so it does not keep running.
              reader.cancel().catch(() => {});
              throw e;
            }
            filesCompleted++;
            updateProgress(true);
            appendLog(i18next.t("modals.directoryDownloadFinished", { name }));
          } catch (e) {
            // User cancel download
            if (e instanceof Error && e.name == "AbortError") {
              appendLog(i18next.t("modals.directoryDownloadCancelled"));
              throw e;
            }
            if (e instanceof ProviderGaveUp) {
              throw e;
            }
            failed++;
            filesCompleted++;
            updateProgress(true);
            appendLog(
              i18next.t("modals.directoryDownloadErrorNotification", {
                name: name,
                msg: (e as Error).message,
              }),
            );
          }
        }
      } catch (e) {
        if (e instanceof Error && e.name == "AbortError") {
          throw e;
        }
        if (e instanceof ProviderGaveUp) {
          throw e;
        }
        failed += childFiles.length;
        filesCompleted += childFiles.length;
        updateProgress(true);
        appendLog(
          i18next.t("modals.directoryDownloadError", {
            msg: (e as Error).message,
          }),
        );
      }
    };
    try {
      for (const folder of folders) {
        await downloadFolder(folder.children, folder.relativePath);
      }
    } catch (e) {
      logStop(e);
      throw e;
    }

    // Fylvo: say what was left out, so the download does not look complete.
    const notes = skippedNotes(skipped);
    notes.forEach(appendLog);
    if (failed === 0 && notes.length == 0) {
      appendLog(i18next.t("fileManager.directoryDownloadFinished"));
    } else if (failed > 0) {
      appendLog(i18next.t("fileManager.directoryDownloadFinishedWithError", { failed }));
    }
    return failed === 0 && notes.length == 0;
  };
}

// Fylvo: at most ten names, then how many more.
const shortList = (names: string[]) =>
  names.length <= 10
    ? names.join(", ")
    : names.slice(0, 10).join(", ") + " " + i18next.t("application:connected.andMore", { count: names.length - 10 });

// Fylvo: lines that name what a walk left out (see WalkSkipped).
const skippedNotes = (skipped: WalkSkipped): string[] => {
  const notes: string[] = [];
  if (skipped.unreadable.length > 0) {
    notes.push(
      i18next.t("application:connected.downloadUnreadable", {
        count: skipped.unreadable.length,
        names: shortList(skipped.unreadable),
      }),
    );
  }
  if (skipped.names.length > 0) {
    notes.push(
      i18next.t("application:connected.downloadSkipped", {
        count: skipped.names.length,
        names: shortList(skipped.names),
      }),
    );
  }
  if (skipped.omitted > 0) {
    notes.push(i18next.t("application:connected.downloadOmitted", { count: skipped.omitted }));
  }
  return notes;
};

export function streamSaverDownload(files: FileResponse[]): AppThunk {
  return async (dispatch, getState) => {
    const skipped: WalkSkipped = { names: [], omitted: 0, unreadable: [] };
    // Fylvo: busy answers of a connected storage's provider, counted over the whole download.
    const connected = files.some((f) => inConnectedStorage(f.path));
    // Ends waiting and retrying when the archive is no longer written (for example a cancel).
    const stopped = new AbortController();
    const busy = connected ? newBusyState(stopped.signal) : undefined;
    let stopReason = "";
    // The file whose content is being written into the archive.
    let activeName = "";
    // Files and folders: an exported name must not take the path of a folder either.
    let allWalked: FileResponseWalked[];
    try {
      allWalked = await longRunningTaskWithSnackbar(
        dispatch(walkAll(files, skipped, busy)),
        "application:fileManager.preparingBathDownload",
      );
    } catch (e) {
      if (e instanceof ProviderGaveUp) {
        enqueueSnackbar({
          message: [i18next.t(e.message), ...skippedNotes(skipped)].join(" "),
          variant: "error",
          persist: true,
          action: DefaultCloseAction,
        });
        return;
      }
      throw e;
    }
    const allFiles = allWalked.filter((f) => f.type == FileType.file);
    // Fylvo: many files from a connected storage take a while; ask first.
    if (connected && allFiles.length >= askAboveFiles) {
      try {
        await dispatch(confirmOperation(i18next.t("application:connected.manyFiles", { count: allFiles.length })));
      } catch (e) {
        return;
      }
    }

    const fileStream = streamSaver.createWriteStream(formatLocalTime(dayjs()) + ".zip");
    const {
      siteConfig: {
        explorer: {
          config: { max_batch_size },
        },
      },
    } = getState();
    // Fylvo: in a connected storage one address at a time, right before its file.
    const maxBatch = connected ? 1 : Math.min(10, max_batch_size ?? 1);

    let current = 0;
    // Paths in the archive, so an exported document does not take the path of another file.
    const takenNames = new Set(allWalked.map((f) => f.relativePath.toLowerCase()));
    const pending: { file: FileResponseWalked; url: string }[] = [];
    const failedNames: string[] = [];

    const readableZipStream = new (window as any).ZIP({
      start(_ctrl: any) {
        // ctrl.close()
      },
      async pull(ctrl: any) {
        // Fylvo: one entry per call, fetched when the archive is ready for it: open responses
        // wait while earlier entries are written, and a provider allows only a few at once.
        // A file that fails is left out and named at the end.
        activeName = "";
        while (true) {
          if (stopped.signal.aborted) {
            ctrl.close();
            return;
          }
          if (pending.length == 0) {
            const batch = allFiles.slice(current, current + maxBatch);
            current += batch.length;
            if (batch.length == 0) {
              ctrl.close();
              return;
            }
            try {
              if (connected) {
                const url = await retryBusy(busy!, () => dispatch(getConnectedFileUrl(getFileLinkedUri(batch[0]))));
                if (url) {
                  pending.push({ file: batch[0], url });
                } else {
                  failedNames.push(batch[0].relativePath);
                }
              } else {
                const entityUrls = await dispatch(
                  getFileEntityUrl({
                    uris: batch.map((f) => getFileLinkedUri(f)),
                    skip_error: true,
                    download: true,
                  }),
                );
                batch.forEach((file, i) => {
                  const url = entityUrls.urls[i]?.url;
                  if (url) {
                    pending.push({ file, url });
                  } else {
                    failedNames.push(file.relativePath);
                  }
                });
              }
            } catch (e) {
              if (e instanceof Error && e.name == "AbortError") {
                ctrl.close();
                return;
              }
              console.warn("Failed to get entity urls", e);
              if (e instanceof ProviderGaveUp) {
                // The provider stayed busy: the archive ends here, the rest is named as missing.
                stopReason = e.message;
                failedNames.push(...allFiles.slice(current - batch.length).map((f) => f.relativePath));
                ctrl.close();
                return;
              }
              failedNames.push(...batch.map((f) => f.relativePath));
            }
            continue;
          }

          const next = pending.shift()!;
          try {
            // An error answer must not become a file in the archive; a busy provider is waited for.
            const fetchOne = () => fetchContent(next.url, stopped.signal);
            const res = busy ? await retryBusy(busy, fetchOne) : await fetchOne();
            const stream = () => res.body;
            try {
              ctrl.enqueue({ name: downloadFileName(next.file, next.file.relativePath, takenNames), stream });
            } catch (e) {
              res.body?.cancel().catch(() => {});
              throw e;
            }
            activeName = next.file.relativePath;
            return;
          } catch (e) {
            if (e instanceof Error && e.name == "AbortError") {
              ctrl.close();
              return;
            }
            console.warn("Failed to download", next.file.relativePath, e);
            failedNames.push(next.file.relativePath);
            if (e instanceof ProviderGaveUp) {
              stopReason = e.message;
              failedNames.push(...pending.map((p) => p.file.relativePath));
              failedNames.push(...allFiles.slice(current).map((f) => f.relativePath));
              ctrl.close();
              return;
            }
          }
        }
      },
    });

    if (window.WritableStream && readableZipStream.pipeTo) {
      try {
        await longRunningTaskWithSnackbar(readableZipStream.pipeTo(fileStream), "fileManager.batchDownloadStarted");
      } catch (e) {
        console.log(e);
        // Ends the transfer that is running and every wait (Fylvo).
        stopped.abort();
        // A cancel by the user says nothing more.
        if (e instanceof Error && e.name == "AbortError") {
          return;
        }
        // Fylvo: a transfer broken off midway leaves an archive that cannot be opened; name the
        // file it broke on and what was left out before.
        enqueueSnackbar({
          message: [
            i18next.t("application:connected.archiveBroken"),
            activeName ? i18next.t("application:connected.archiveBrokenAt", { name: activeName }) : "",
            ...skippedNotes(skipped),
            failedNames.length > 0
              ? i18next.t("application:connected.downloadFailed", {
                  count: failedNames.length,
                  names: shortList(failedNames),
                })
              : "",
          ]
            .filter(Boolean)
            .join(" "),
          variant: "error",
          persist: true,
          action: DefaultCloseAction,
        });
        return;
      }
      // Fylvo: name what is missing from the archive.
      const notes = skippedNotes(skipped);
      if (stopReason) {
        notes.unshift(i18next.t(stopReason));
      }
      if (failedNames.length > 0) {
        notes.push(
          i18next.t("application:connected.downloadFailed", {
            count: failedNames.length,
            names: shortList(failedNames),
          }),
        );
      }
      if (notes.length > 0) {
        enqueueSnackbar({ message: notes.join(" "), variant: "warning", persist: true, action: DefaultCloseAction });
      }
    }
  };
}

export function downloadSingleFile(file: FileResponse, preferredEntity?: string): AppThunk {
  return async (dispatch, _getState) => {
    const isSharedFile = file.metadata?.[Metadata.share_redirect] ?? false;
    if (isSharedFile) {
      file = await dispatch(refreshSingleFileSymbolicLinks(file));
    }

    const urlRes = await longRunningTaskWithSnackbar(
      dispatch(
        getFileEntityUrl({
          uris: [getFileLinkedUri(file)],
          entity: preferredEntity,
          download: true,
        }),
      ),
      "application:fileManager.preparingDownload",
    );

    const streamSaverName = urlRes.urls[0].stream_saver_display_name;
    if (streamSaverName) {
      // remove streamSaverParam from query
      const fileStream = streamSaver.createWriteStream(streamSaverName);
      const res = await fetch(urlRes.urls[0].url);
      const readableStream = res.body;
      if (!readableStream) {
        return;
      }

      // more optimized
      if (window.WritableStream && readableStream.pipeTo) {
        const downloadingSnackbar = enqueueSnackbar({
          message: i18next.t("fileManager.downloadingFile", {
            name: streamSaverName,
          }),
          variant: "loading",
          persist: true,
        });
        return readableStream.pipeTo(fileStream).finally(() => closeSnackbar(downloadingSnackbar));
      }
    } else if (inConnectedStorage(file.path)) {
      // Fylvo: the content of a connected storage can still fail at the provider after the
      // address was given. As a download link, a failure stays a failed download and does
      // not replace the page.
      const link = document.createElement("a");
      link.href = urlRes.urls[0].url;
      link.download = "";
      link.rel = "noopener";
      document.body.appendChild(link);
      link.click();
      link.remove();
    } else {
      window.location.assign(urlRes.urls[0].url);
    }
  };
}

const getDownloadSelectOption = (options: MultipleDownloadOption[]): DialogSelectOption[] => {
  return options.map((option): DialogSelectOption => {
    switch (option) {
      case MultipleDownloadOption.Backend:
        return {
          value: MultipleDownloadOption.Backend,
          name: i18next.t("fileManager.serverBatchDownload"),
          description: i18next.t("fileManager.serverBatchDownloadDescription"),
        };
      case MultipleDownloadOption.Browser:
        return {
          value: MultipleDownloadOption.Browser,
          name: i18next.t("fileManager.browserDownload"),
          description: i18next.t("fileManager.browserDownloadDescription"),
        };
      default:
        return {
          value: MultipleDownloadOption.StreamSaver,
          name: i18next.t("fileManager.browserBatchDownload"),
          description: i18next.t("fileManager.browserBatchDownloadDescription"),
        };
    }
  });
};

const getDownloadOverwriteOption = (name: string): DialogSelectOption[] => {
  return [
    {
      name: i18next.t("fileManager.directoryDownloadReplace"),
      description: i18next.t("fileManager.directoryDownloadReplaceDescription", { name }),
      value: DownloadOverwriteOption.Overwrite,
    },
    {
      name: i18next.t("fileManager.directoryDownloadSkip"),
      description: i18next.t("fileManager.directoryDownloadSkipDescription", {
        name,
      }),
      value: DownloadOverwriteOption.Skip,
    },
    {
      name: i18next.t("fileManager.directoryDownloadReplaceAll"),
      description: i18next.t("fileManager.directoryDownloadReplaceAllDescription", { name }),
      value: DownloadOverwriteOption.OverwriteAll,
    },
    {
      name: i18next.t("fileManager.directoryDownloadSkipAll"),
      description: i18next.t("fileManager.directoryDownloadSkipAllDescription", { name }),
      value: DownloadOverwriteOption.SkipAll,
    },
  ];
};
