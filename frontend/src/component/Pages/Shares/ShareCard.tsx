import {
  Box,
  Chip,
  Grid,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuProps,
  Skeleton,
  Tooltip,
  Typography,
  useTheme,
} from "@mui/material";
import { bindMenu, bindTrigger, usePopupState } from "material-ui-popup-state/hooks";
import { useSnackbar } from "notistack";
import { useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useInView } from "react-intersection-observer";
import { sendDeleteShare } from "../../../api/api.ts";
import { FileType, Share } from "../../../api/explorer.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { confirmOperation } from "../../../redux/thunks/dialog.ts";
import { openShareEditByID } from "../../../redux/thunks/share.ts";
import SessionManager from "../../../session";
import { copyToClipboard } from "../../../util/index.ts";
import { DefaultCloseAction } from "../../Common/Snackbar/snackbar.tsx";
import { NoWrapBox } from "../../Common/StyledComponents.tsx";
import TimeBadge from "../../Common/TimeBadge.tsx";
import { DenseDivider, SquareMenuItem } from "../../FileManager/ContextMenu/ContextMenu.tsx";
import FileTypeIcon from "../../FileManager/Explorer/FileTypeIcon.tsx";
import Clipboard from "../../Icons/Clipboard.tsx";
import DeleteOutlined from "../../Icons/DeleteOutlined.tsx";
import Eye from "../../Icons/Eye.tsx";
import MoreHorizontal from "../../Icons/MoreHorizontal.tsx";
import LinkEdit from "../../Icons/LinkEdit.tsx";
import LockClosedOutlined from "../../Icons/LockClosedOutlined.tsx";
import Open from "../../Icons/Open.tsx";
import { ShareMembersBadge } from "../../FileManager/Dialogs/Share/ShareMembers.tsx";
import { SummaryButton } from "../Tasks/TaskCard.tsx";

export interface ShareCardProps {
  share?: Share;
  onLoad?: () => void;
  loading?: boolean;
  onShareDeleted: (id: string) => void;
}

interface ActionMenuProps extends MenuProps {
  share: Share;
  onShareDeleted: (id: string) => void;
}

const ActionMenu = ({ share, onShareDeleted, onClose, ...rest }: ActionMenuProps) => {
  const theme = useTheme();
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();

  const deleteShare = useCallback(() => {
    dispatch(confirmOperation(t("fileManager.deleteShareWarning"))).then(() => {
      dispatch(sendDeleteShare(share.id)).then(() => {
        onClose && onClose({}, "backdropClick");
        enqueueSnackbar({
          message: t("application:share.shareCanceled"),
          variant: "success",
          action: DefaultCloseAction,
        });
        onShareDeleted(share.id);
      });
    });
  }, [t, share.id, onClose, dispatch, enqueueSnackbar]);

  const openEdit = useCallback(() => {
    dispatch(openShareEditByID(share.id, share.password, share.source_type == FileType.file));
    onClose && onClose({}, "backdropClick");
  }, [dispatch, share, onClose]);

  const openLink = useCallback(() => {
    window.open(share.url, "_blank");
    onClose && onClose({}, "backdropClick");
  }, [share, onClose]);

  const copyLink = useCallback(() => {
    copyToClipboard(share.url);
    onClose && onClose({}, "backdropClick");
  }, [share, onClose, t]);

  return (
    <Menu
      onClose={onClose}
      slotProps={{
        paper: {
          sx: {
            minWidth: 150,
          },
        },
      }}
      {...rest}
    >
      <SquareMenuItem dense onClick={openLink}>
        <ListItemIcon>
          <Open fontSize="small" />
        </ListItemIcon>
        <ListItemText>{t(`fileManager.open`)}</ListItemText>
      </SquareMenuItem>
      {!share.member_only && (
        <SquareMenuItem dense onClick={copyLink}>
          <ListItemIcon>
            <Clipboard fontSize="small" />
          </ListItemIcon>
          <ListItemText>{t(`share.copyLinkToClipboard`)}</ListItemText>
        </SquareMenuItem>
      )}
      {!share.expired && (
        <SquareMenuItem dense onClick={openEdit}>
          <ListItemIcon>
            <LinkEdit fontSize="small" />
          </ListItemIcon>
          <ListItemText>{t(`fileManager.${share?.expired ? "editAndReactivate" : "edit"}`)}</ListItemText>
        </SquareMenuItem>
      )}
      <DenseDivider />
      <SquareMenuItem hoverColor={theme.palette.error.light} dense onClick={deleteShare}>
        <ListItemIcon>
          <DeleteOutlined fontSize="small" />
        </ListItemIcon>
        <ListItemText>{t(`fileManager.delete`)}</ListItemText>
      </SquareMenuItem>
    </Menu>
  );
};

const ShareCard = ({ share, onShareDeleted, onLoad, loading }: ShareCardProps) => {
  const { t } = useTranslation();
  const { ref, inView } = useInView({
    rootMargin: "200px 0px",
    triggerOnce: true,
    skip: !loading || !onLoad,
  });

  const popupState = usePopupState({
    popupId: "shareAction",
    variant: "popover",
  });

  useEffect(() => {
    if (!inView) {
      return;
    }

    if (onLoad) {
      onLoad();
    }
  }, [inView]);

  const user = SessionManager.currentLoginOrNull();

  return (
    <>
      {share && <ActionMenu onShareDeleted={onShareDeleted} share={share} {...bindMenu(popupState)} />}
      <Grid item xs={12} ref={ref}>
        <SummaryButton
          disabled={loading}
          expanded={false}
          onContextMenu={(e) => {
            e.preventDefault();
            if (share?.owner?.id === user?.user.id) {
              popupState.open(e);
            }
          }}
          sx={{ px: 1.5, py: 0, width: "100%", border: 1, borderColor: "divider", textAlign: "left" }}
          {...(share?.owner?.id != user?.user.id
            ? {
                onClick: () => {
                  window.open(share?.url ?? "#", "_blank");
                },
              }
            : bindTrigger(popupState))}
        >
          <Box
            sx={{
              display: "grid",
              gridTemplateColumns: "18px minmax(0, 1fr) auto auto",
              alignItems: "center",
              gap: 1,
              width: "100%",
              minWidth: 0,
            }}
          >
            {share ? (
              <FileTypeIcon
                fontSize="small"
                name={share.name ?? ""}
                fileType={share.source_type ?? FileType.file}
              />
            ) : (
              <Skeleton variant="rounded" width={16} height={16} />
            )}
            <NoWrapBox sx={{ display: "flex", gap: 0.75, alignItems: "center" }}>
              {share?.password_protected && <LockClosedOutlined fontSize="small" color="action" />}
              <Tooltip title={share?.name ?? ""}>
                <Typography variant="body2" fontWeight={500} noWrap>
                  {loading ? <Skeleton width={110} /> : share?.name}
                </Typography>
              </Tooltip>
              {share?.expired && (
                <Chip size="small" label={t("application:share.expired")} sx={{ height: 18, flexShrink: 0 }} />
              )}
            </NoWrapBox>
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                justifyContent: "flex-end",
                flexWrap: "wrap",
                gap: { xs: 0.5, sm: 2 },
                maxWidth: { xs: 104, sm: "none" },
                color: "text.secondary",
                whiteSpace: "nowrap",
              }}
            >
              {share?.member_only && <ShareMembersBadge share={share} />}
              {share?.created_at ? (
                <>
                  <TimeBadge variant="caption" datetime={share.created_at} />
                  <Typography variant="caption" sx={{ display: "inline-flex", alignItems: "center", gap: 0.5 }}>
                    <Eye sx={{ fontSize: 14 }} />
                    {share.visited ?? 0}
                  </Typography>
                </>
              ) : (
                <Skeleton width={60} />
              )}
            </Box>
            {share && (
              share.owner?.id === user?.user.id
                ? <MoreHorizontal fontSize="small" color="action" />
                : <Open fontSize="small" color="action" />
            )}
          </Box>
        </SummaryButton>
      </Grid>
    </>
  );
};

export default ShareCard;
