import i18next from "i18next";
import { closeSnackbar, enqueueSnackbar, SnackbarKey } from "notistack";
import { getFileInfo, getFileList, getShareInfo, sendCreateShare, sendUpdateShare } from "../../api/api.ts";
import { FileResponse, FileType, Share, ShareCreateService } from "../../api/explorer.ts";
import { DefaultCloseAction, OpenReadMeAction } from "../../component/Common/Snackbar/snackbar.tsx";
import { ShareSetting } from "../../component/FileManager/Dialogs/Share/ShareSetting.tsx";
import { getPaginationState } from "../../component/FileManager/Pagination/PaginationFooter.tsx";
import CrUri from "../../util/uri.ts";
import { fileUpdated } from "../fileManagerSlice.ts";
import {
  addShareInfo,
  closeShareReadme,
  setManageShareDialog,
  setShareLinkDialog,
  setShareReadmeOpen,
} from "../globalStateSlice.ts";
import { AppThunk } from "../store.ts";
import { longRunningTaskWithSnackbar } from "./file.ts";

// Fylvo: only a whole number above zero is a value; anything else means "no limit".
const positive = (value: number) => (Number.isInteger(value) && value > 0 ? value : undefined);

export function createOrUpdateShareLink(
  index: number,
  file: FileResponse,
  setting: ShareSetting,
  existed?: string,
): AppThunk<Promise<string>> {
  return async (dispatch, getState) => {
    const req: ShareCreateService = {
      uri: file.path,
      share_view: setting.share_view,
      show_readme: setting.show_readme,
      expire: setting.expires ? positive(setting.expires_val) : undefined,
      // Fylvo: a member share has no password and no download limit.
      ...(setting.member_only
        ? { member_only: true, members: setting.members ?? [] }
        : {
            is_private: setting.is_private,
            password: setting.is_private ? setting.password : undefined,
            downloads: setting.downloads ? positive(setting.downloads_val) : undefined,
          }),
      // Fylvo: uploads exist only for links of folders. allow_upload is always sent so an edit can turn it off.
      ...(!setting.member_only &&
        file.type == FileType.folder && {
          allow_upload: !!setting.allow_upload,
          ...(setting.allow_upload && {
            hide_content: !!setting.hide_content,
            upload_limit: setting.upload_limit ? positive(setting.upload_limit_val) : undefined,
          }),
        }),
    };

    const res = await dispatch(existed ? sendUpdateShare(req, existed) : sendCreateShare(req));
    dispatch(
      fileUpdated({
        index,
        value: [
          {
            file: { ...file, shared: true },
            oldPath: file.path,
          },
        ],
      }),
    );

    if (existed) {
      const {
        globalState: { manageShareDialogOpen, manageShareDialogFile },
      } = getState();
      if (manageShareDialogOpen && manageShareDialogFile?.path === file.path) {
        dispatch(
          setManageShareDialog({
            open: true,
            file: {
              ...manageShareDialogFile,
              extended_info: undefined,
            },
          }),
        );
      }
    }

    return res;
  };
}

interface shareInfoQueueItem {
  resolve: (value: Share | PromiseLike<Share>) => void;
  reject: (reason?: any) => void;
}

const shareInfoLoadQueue: {
  [key: string]: shareInfoQueueItem[];
} = {};

export function queueLoadShareInfo(uri: CrUri, countViews: boolean = false): AppThunk<Promise<Share>> {
  return async (dispatch, getState) => {
    const id = `${uri.id()}/${uri.password()}/${countViews}`;
    const cached = getState().globalState.shareInfo[id];
    if (cached) {
      return cached;
    }
    if (!shareInfoLoadQueue[id]) {
      shareInfoLoadQueue[id] = [];
    }

    const p = new Promise<Share>((resolve, reject) => {
      shareInfoLoadQueue[id].push({ resolve, reject });
    });

    if (shareInfoLoadQueue[id].length === 1) {
      dispatch(getShareInfo(uri.id(), uri.password(), countViews))
        .then((res) => {
          shareInfoLoadQueue[id].forEach((item) => {
            item.resolve(res);
          });
          dispatch(addShareInfo({ id, info: res }));
        })
        .catch((e) => {
          shareInfoLoadQueue[id].forEach((item) => {
            item.reject(e);
          });
        })
        .finally(() => {
          delete shareInfoLoadQueue[id];
        });
    }

    return p;
  };
}

export function openShareEditByID(shareId: string, password?: string, singleFile?: boolean): AppThunk {
  return async (dispatch) => {
    try {
      const { share, file } = await longRunningTaskWithSnackbar(
        dispatch(getFileAndShareById(shareId, password, singleFile)),
        "application:uploader.processing",
      );
      dispatch(
        setShareLinkDialog({
          open: true,
          file: file,
          share: share,
        }),
      );
    } catch (e) {
      console.log(e);
      return;
    }
  };
}

// Priority from high to low
const supportedReadMeFiles = ["README.md", "README.txt"];

export function detectReadMe(index: number, isTablet: boolean): AppThunk<Promise<void>> {
  return async (dispatch, getState) => {
    const { files: list, pagination } = getState().fileManager[index]?.list ?? {};
    if (list) {
      // Find readme file from highest to lowest priority
      for (const readmeFile of supportedReadMeFiles) {
        const found = list.find((file) => file.name === readmeFile);
        if (found) {
          dispatch(tryOpenReadMe(found, isTablet));
          return;
        }
      }
    }

    // Not found in current file list, try to get file directly
    const path = getState().fileManager[index]?.pure_path;
    const hasMorePages = getPaginationState(pagination).moreItems;
    if (path && hasMorePages) {
      const uri = new CrUri(path);
      for (const readmeFile of supportedReadMeFiles) {
        try {
          const file = await dispatch(getFileInfo({ uri: uri.join(readmeFile).toString() }, true));
          if (file) {
            dispatch(tryOpenReadMe(file, isTablet));
            return;
          }
        } catch (e) {}
      }
    }
    dispatch(closeShareReadme());
  };
}

let snackbarId: SnackbarKey | undefined = undefined;

function tryOpenReadMe(file: FileResponse, askForConfirmation?: boolean): AppThunk<Promise<void>> {
  return async (dispatch) => {
    if (askForConfirmation) {
      dispatch(setShareReadmeOpen({ open: false, target: file }));
      if (snackbarId) {
        closeSnackbar(snackbarId);
      }
      snackbarId = enqueueSnackbar({
        message: "README.md",
        variant: "file",
        file,
        action: OpenReadMeAction(file),
      });
    } else {
      dispatch(setShareReadmeOpen({ open: true, target: file }));
    }
  };
}

function getFileAndShareById(
  shareId: string,
  password?: string,
  singleFile?: boolean,
): AppThunk<
  Promise<{
    share: Share;
    file: FileResponse;
  }>
> {
  return async (dispatch) => {
    let share: Share | undefined;
    try {
      share = await dispatch(getShareInfo(shareId, password, false, true));
    } catch (e) {
      enqueueSnackbar({
        message: i18next.t("application:share.shareNotExist"),
        variant: "error",
        action: DefaultCloseAction,
      });
      throw e;
    }

    let file: FileResponse | undefined = undefined;
    if (singleFile) {
      const root = new CrUri(share.source_uri ?? "");
      file = await dispatch(getFileInfo({ uri: root.join(share.name ?? "").toString() }));
    } else {
      file = await dispatch(getFileInfo({ uri: share.source_uri ?? "" }));
    }
    return { share, file };
  };
}
