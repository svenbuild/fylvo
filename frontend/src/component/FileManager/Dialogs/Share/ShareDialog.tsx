import { Box, Checkbox, DialogContent, IconButton, Stack, Tooltip, useTheme } from "@mui/material";
import dayjs from "dayjs";
import React, { useCallback, useLayoutEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { enqueueSnackbar } from "notistack";
import { Share as ShareModel } from "../../../../api/explorer.ts";
import { closeShareLinkDialog } from "../../../../redux/globalStateSlice.ts";
import { useAppDispatch, useAppSelector } from "../../../../redux/hooks.ts";
import { createOrUpdateShareLink } from "../../../../redux/thunks/share.ts";
import { copyToClipboard, sendLink } from "../../../../util";
import { DefaultCloseAction } from "../../../Common/Snackbar/snackbar.tsx";
import { FilledTextField, SmallFormControlLabel } from "../../../Common/StyledComponents.tsx";
import DraggableDialog from "../../../Dialogs/DraggableDialog.tsx";
import CopyOutlined from "../../../Icons/CopyOutlined.tsx";
import Share from "../../../Icons/Share.tsx";
import { FileManagerIndex } from "../../FileManager.tsx";
import ShareSettingContent, {
  downloadOptions,
  expireOptions,
  ShareSetting,
  uploadLimitOptions,
} from "./ShareSetting.tsx";

const initialSetting: ShareSetting = {
  expires_val: expireOptions[2],
  downloads_val: downloadOptions[0],
  upload_limit_val: uploadLimitOptions[1],
};

interface ShareLinkPassword {
  shareLink: string;
  password?: string;
}

const shareToSetting = (share: ShareModel): ShareSetting => {
  const res: ShareSetting = {
    ...initialSetting,
    is_private: share.is_private,
    password: share.password,
    share_view: share.share_view,
    show_readme: share.show_readme,
    member_only: share.member_only,
    members: share.members ?? [],
    downloads: share.remain_downloads != undefined && share.remain_downloads > 0,
    allow_upload: share.allow_upload,
    hide_content: share.hide_content,
    upload_limit: !!share.upload_limit,
  };

  if (res.downloads) {
    res.downloads_val = share.remain_downloads ?? 0;
  }
  if (share.upload_limit) {
    res.upload_limit_val = share.upload_limit;
  }

  if (share.expires != undefined) {
    const secondsTtl = dayjs(share.expires).diff(dayjs(), "second");
    res.expires = secondsTtl > 0;
    if (res.expires) {
      res.expires_val = secondsTtl;
    }
  }

  return res;
};

const ShareDialog = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const theme = useTheme();

  const [loading, setLoading] = useState(false);
  const [setting, setSetting] = useState<ShareSetting>(initialSetting);
  const [shareLink, setShareLink] = useState<string>("");
  const [includePassword, setIncludePassword] = useState(true);
  const shareLinkPassword = useMemo(() => {
    const start = shareLink.lastIndexOf("/s/");
    const shareLinkParts = shareLink.substring(start + 3).split("/");
    const password = shareLinkParts.length == 2 ? shareLinkParts[1] : undefined;
    return {
      shareLink: password ? shareLink.substring(0, shareLink.lastIndexOf("/")) : shareLink,
      password: password,
    } as ShareLinkPassword;
  }, [shareLink]);

  const open = useAppSelector((state) => state.globalState.shareLinkDialogOpen);
  const target = useAppSelector((state) => state.globalState.shareLinkDialogFile);
  const editTarget = useAppSelector((state) => state.globalState.shareLinkDialogShare);

  // A layout effect, so the dialog never shows the settings of the last share for a frame.
  useLayoutEffect(() => {
    if (open) {
      if (editTarget) {
        setSetting(shareToSetting(editTarget));
      } else {
        setSetting(initialSetting);
      }
      setShareLink("");
      setIncludePassword(true);
    }
  }, [open]);

  const onClose = useCallback(() => {
    if (!loading) {
      dispatch(closeShareLinkDialog());
    }
  }, [dispatch, loading]);

  const onAccept = useCallback(
    async (e?: React.MouseEvent<HTMLElement>) => {
      if (e) {
        e.preventDefault();
      }

      if (!target) return;

      if (shareLink) {
        copyToClipboard(shareLink);
        return;
      }

      setLoading(true);
      try {
        const shareLink = await dispatch(
          createOrUpdateShareLink(FileManagerIndex.main, target, setting, editTarget?.id),
        );
        // Fylvo: a member share has no link to hand out, the people see it under "Shared with me".
        if (setting.member_only) {
          enqueueSnackbar({
            message: setting.members?.length
              ? t("application:modals.sharedWithPeople", { count: setting.members.length })
              : t("application:modals.sharedWithNobody"),
            variant: "success",
            action: DefaultCloseAction,
          });
          dispatch(closeShareLinkDialog());
          return;
        }
        setShareLink(shareLink);
      } catch (e) {
      } finally {
        setLoading(false);
      }
    },
    [dispatch, t, target, shareLink, editTarget, setLoading, setting, setShareLink],
  );

  const finalShareLink = useMemo(() => {
    if (includePassword) {
      return shareLink;
    }
    return shareLink.substring(0, shareLink.lastIndexOf("/"));
  }, [includePassword, shareLink]);

  const finalShareLinkPassword = useMemo(() => {
    if (!includePassword) {
      return shareLink.substring(shareLink.lastIndexOf("/") + 1);
    }
    return undefined;
  }, [includePassword, shareLink]);

  return (
    <>
      <DraggableDialog
        title={t(
          setting.member_only
            ? "application:modals.shareWithPeople"
            : `application:modals.${editTarget ? "edit" : "create"}ShareLink`,
        )}
        showActions
        loading={loading}
        showCancel
        hideOk={!!shareLink}
        disabled={!editTarget && setting.member_only && !setting.members?.length}
        onAccept={onAccept}
        dialogProps={{
          open: open ?? false,
          onClose: onClose,
          fullWidth: true,
          maxWidth: "xs",
          // Fylvo: hang the dialog from a fixed top edge. It grows only downward (people added,
          // link created), so its title never moves.
          sx: {
            "& .MuiDialog-container": { alignItems: "flex-start" },
            "& .MuiDialog-paper": { mt: { xs: "12px", sm: "12vh" }, maxHeight: { sm: "calc(100% - 12vh - 32px)" } },
          },
        }}
        cancelText={shareLink ? t("common:close") : undefined}
        secondaryAction={
          shareLink
            ? // @ts-ignore
              navigator.share && (
                <Tooltip title={t("application:modals.sendLink")}>
                  <IconButton onClick={() => sendLink(target?.name ?? "", finalShareLink)}>
                    <Share />
                  </IconButton>
                </Tooltip>
              )
            : undefined
        }
      >
        <DialogContent sx={{ pb: "12px" }}>
          <Box>
            {!shareLink && (
              <ShareSettingContent
                editing={!!editTarget}
                onSettingChange={setSetting}
                setting={setting}
                file={target}
              />
            )}
            {shareLink && (
              <Stack spacing={1}>
                <FilledTextField
                  variant={"filled"}
                  inputProps={{ readonly: true }}
                  label={t("modals.shareLink")}
                  fullWidth
                  value={finalShareLink ?? ""}
                  onFocus={(e) => e.target.select()}
                  slotProps={{
                    input: {
                      endAdornment: (
                        <IconButton
                          onClick={() => copyToClipboard(finalShareLink)}
                          aria-label={t("common:copyToClipboard") + ": " + t("modals.shareLink")}
                          size="small"
                          sx={{
                            mr: -0.5,
                            bgcolor: "primary.main",
                            color: "primary.contrastText",
                            "&:hover": { bgcolor: "primary.dark" },
                          }}
                        >
                          <CopyOutlined fontSize="small" />
                        </IconButton>
                      ),
                    },
                  }}
                />
                {shareLinkPassword.password && (
                  <>
                    <Tooltip enterDelay={100} title={t("application:modals.includePasswordInShareLinkDes")}>
                      <SmallFormControlLabel
                        sx={{
                          mt: "0!important",
                        }}
                        control={
                          <Checkbox
                            disableRipple
                            sx={{
                              pl: 0,
                            }}
                            size="small"
                            checked={includePassword}
                            onChange={() => {
                              setIncludePassword(!includePassword);
                            }}
                          />
                        }
                        label={t("application:modals.includePasswordInShareLink")}
                      />
                    </Tooltip>
                    {/* Below the checkbox, so the dialog only grows downward and the checkbox stays put. */}
                    {!includePassword && (
                      <FilledTextField
                        variant={"filled"}
                        inputProps={{ readonly: true }}
                        label={t("modals.sharePassword")}
                        fullWidth
                        value={finalShareLinkPassword ?? ""}
                        onFocus={(e) => e.target.select()}
                        slotProps={{
                          input: {
                            endAdornment: (
                              <IconButton
                                onClick={() => copyToClipboard(finalShareLinkPassword ?? "")}
                                aria-label={t("common:copyToClipboard") + ": " + t("modals.sharePassword")}
                                size="small"
                                sx={{ marginRight: -1 }}
                              >
                                <CopyOutlined />
                              </IconButton>
                            ),
                          },
                        }}
                      />
                    )}
                  </>
                )}
              </Stack>
            )}
          </Box>
        </DialogContent>
      </DraggableDialog>
    </>
  );
};
export default ShareDialog;
