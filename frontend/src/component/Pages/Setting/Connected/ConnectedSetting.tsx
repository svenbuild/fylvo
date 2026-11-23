import { LoadingButton } from "@mui/lab";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  DialogContent,
  IconButton,
  Link,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Stack,
  Tooltip,
  Typography,
} from "@mui/material";
import { useSnackbar } from "notistack";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link as RouterLink } from "react-router-dom";
import { useQueryState } from "nuqs";
import {
  ConnectedCode,
  ConnectedFlowErrors,
  ConnectedListResponse,
  ConnectedMaxNameBytes,
  ConnectedMaxPerUser,
  ConnectedProviderNames,
  ConnectedStatus,
  ConnectedStorage,
  getConnectedStorages,
  sendConnectedAuthorize,
  sendConnectedDisconnect,
  sendConnectedFinish,
  sendConnectedRename,
} from "../../../../api/connected.ts";
import { AppError } from "../../../../api/request.ts";
import { GroupPermission } from "../../../../api/user.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { confirmOperation } from "../../../../redux/thunks/dialog.ts";
import SessionManager from "../../../../session";
import { GroupBS } from "../../../../session/utils.ts";
import FacebookCircularProgress from "../../../Common/CircularProgress.tsx";
import { DefaultCloseAction } from "../../../Common/Snackbar/snackbar.tsx";
import {
  DenseFilledTextField,
  NoWrapTypography,
  SecondaryLoadingButton,
  SquareChip,
} from "../../../Common/StyledComponents.tsx";
import DraggableDialog, { StyledDialogContentText } from "../../../Dialogs/DraggableDialog.tsx";
import Add from "../../../Icons/Add.tsx";
import ArrowSync from "../../../Icons/ArrowSync.tsx";
import CloudOutlined from "../../../Icons/CloudOutlined.tsx";
import Dismiss from "../../../Icons/Dismiss.tsx";
import Edit from "../../../Icons/Edit.tsx";
import SettingForm from "../SettingForm.tsx";

// The settings tab of connected storages: list, connect, rename, reconnect, disconnect.
// The browser comes back here from the provider with ?finish=<code> or ?error=<value>.

export const ConnectedSettingTab = "connected";

const providerName = (list: ConnectedListResponse | undefined, type: string) =>
  list?.providers.find((p) => p.type == type)?.name ?? ConnectedProviderNames[type] ?? type;

const nameBytes = (name: string) => new TextEncoder().encode(name).length;

// What the settings page needs to know to show the tab: hidden on a server where nobody
// can connect anything and the account has nothing connected.
export const useConnectedTabVisible = (openTab: string | null) => {
  const dispatch = useAppDispatch();
  const [visible, setVisible] = useState(openTab == ConnectedSettingTab);
  useEffect(() => {
    dispatch(getConnectedStorages())
      .then((res) => {
        const usable = res.available && res.allowed && res.providers.some((p) => p.configured);
        setVisible((v) => v || usable || res.storages.length > 0);
      })
      .catch(() => {});
  }, []);
  return visible || openTab == ConnectedSettingTab;
};

// Values of data.reason the server sends with errors of authorize and finish.
const knownReasons = ["expired", "account", "other_account", "exists", "too_many", "setup", "not_configured", "not_allowed"];

// Text for an error of authorize or finish: by data.reason, else by code, else general.
const useErrorText = () => {
  const { t } = useTranslation();
  return useCallback(
    (e: unknown) => {
      if (e instanceof AppError) {
        const reason = e.response?.data?.reason;
        if (typeof reason == "string" && knownReasons.includes(reason)) {
          return t(`connected.reason.${reason}`);
        }
        if (e.code == 40004) {
          return t("connected.reason.exists");
        }
        if (e.code == ConnectedCode.Unavailable) {
          return t("connected.reason.not_allowed");
        }
      }
      return t("connected.finishFailed");
    },
    [t],
  );
};

// Opens the consent page of the provider. The provider sends the browser back to the
// first site URL of the installation, and the sign-in is bound to a cookie of this
// address: started from another address it could not be completed. Returns that address
// instead of leaving the page.
const openProvider = (url: string): string | undefined => {
  try {
    const back = new URL(url).searchParams.get("redirect_uri");
    const origin = back ? new URL(back).origin : undefined;
    if (origin && origin != window.location.origin) {
      return origin;
    }
  } catch (e) {
    // Not a URL we can read; leave it to the provider.
  }
  window.location.assign(url);
  return undefined;
};

// Calls reset when the browser shows this page again from its cache (back button after
// leaving for the provider), so buttons do not stay in their loading state.
const useResetOnBack = (reset: () => void) => {
  useEffect(() => {
    const onShow = (e: PageTransitionEvent) => e.persisted && reset();
    window.addEventListener("pageshow", onShow);
    return () => window.removeEventListener("pageshow", onShow);
  }, []);
};

interface FlowResult {
  severity: "success" | "error";
  message: string;
  // Address of the installation to start connecting from.
  origin?: string;
}

const RenameDialog = ({
  storage,
  onClose,
  onRenamed,
}: {
  storage?: ConnectedStorage;
  onClose: () => void;
  onRenamed: (s: ConnectedStorage) => void;
}) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [name, setName] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (storage) {
      setName(storage.name);
    }
  }, [storage]);

  const tooLong = nameBytes(name.trim()) > ConnectedMaxNameBytes;
  const invalid = !name.trim() || tooLong;

  const submit = () => {
    if (!storage || invalid || loading) {
      return;
    }
    setLoading(true);
    dispatch(sendConnectedRename(storage.id, name.trim()))
      .then((res) => {
        onRenamed(res);
        onClose();
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  return (
    <DraggableDialog
      title={t("connected.rename")}
      showActions
      showCancel
      loading={loading}
      disabled={invalid}
      onAccept={submit}
      dialogProps={{ open: !!storage, onClose, fullWidth: true, maxWidth: "xs" }}
    >
      <DialogContent>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <DenseFilledTextField
            autoFocus
            fullWidth
            label={t("connected.name")}
            value={name}
            error={tooLong}
            helperText={tooLong ? t("connected.nameTooLong") : undefined}
            onChange={(e) => setName(e.target.value)}
          />
        </form>
      </DialogContent>
    </DraggableDialog>
  );
};

const ConnectDialog = ({
  open,
  list,
  onClose,
  onFailed,
}: {
  open: boolean;
  list?: ConnectedListResponse;
  onClose: () => void;
  onFailed: (result: FlowResult) => void;
}) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const errorText = useErrorText();
  const [loading, setLoading] = useState<string | undefined>(undefined);
  const providers = list?.providers.filter((p) => p.configured) ?? [];
  useResetOnBack(() => setLoading(undefined));

  const connect = (provider: string) => {
    setLoading(provider);
    dispatch(sendConnectedAuthorize(provider))
      .then((res) => {
        // On success the page is left; the loading state stays until the browser is gone.
        const origin = openProvider(res.url);
        if (origin) {
          setLoading(undefined);
          onClose();
          onFailed({ severity: "error", message: t("connected.otherOrigin"), origin });
        }
      })
      .catch((e) => {
        setLoading(undefined);
        onClose();
        onFailed({ severity: "error", message: errorText(e) });
      });
  };

  return (
    <DraggableDialog
      title={t("connected.connect")}
      loading={!!loading}
      dialogProps={{ open, onClose, fullWidth: true, maxWidth: "xs" }}
    >
      <DialogContent>
        <StyledDialogContentText sx={{ mb: 1.5 }}>{t("connected.connectDes")}</StyledDialogContentText>
        <List disablePadding>
          {providers.map((p) => (
            <ListItemButton
              key={p.type}
              disabled={!!loading}
              onClick={() => connect(p.type)}
              sx={{ border: 1, borderColor: "divider", borderRadius: 1, mt: 1 }}
            >
              <ListItemIcon sx={{ minWidth: 34 }}>
                <CloudOutlined fontSize="small" />
              </ListItemIcon>
              <ListItemText primary={p.name} slotProps={{ primary: { variant: "subtitle2" } }} />
              {loading == p.type && <CircularProgress size={18} />}
            </ListItemButton>
          ))}
        </List>
      </DialogContent>
    </DraggableDialog>
  );
};

const StorageRow = ({
  storage,
  list,
  allowed,
  onRename,
  onDisconnected,
  onFailed,
}: {
  storage: ConnectedStorage;
  list: ConnectedListResponse;
  allowed: boolean;
  onRename: (s: ConnectedStorage) => void;
  onDisconnected: (id: string) => void;
  onFailed: (result: FlowResult) => void;
}) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const errorText = useErrorText();
  const [reconnecting, setReconnecting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const needsReconnect = storage.status == ConnectedStatus.reconnect;
  const configured = list.providers.some((p) => p.type == storage.provider && p.configured);
  useResetOnBack(() => setReconnecting(false));

  const reconnect = () => {
    setReconnecting(true);
    dispatch(sendConnectedAuthorize(storage.provider, storage.id))
      .then((res) => {
        const origin = openProvider(res.url);
        if (origin) {
          setReconnecting(false);
          onFailed({ severity: "error", message: t("connected.otherOrigin"), origin });
        }
      })
      .catch((e) => {
        setReconnecting(false);
        onFailed({ severity: "error", message: errorText(e) });
      });
  };

  const disconnect = () => {
    dispatch(confirmOperation(t("connected.disconnectConfirm", { name: storage.name })))
      .then(() => {
        setDisconnecting(true);
        return dispatch(sendConnectedDisconnect(storage.id))
          .then(() => onDisconnected(storage.id))
          .finally(() => setDisconnecting(false));
      })
      .catch(() => {});
  };

  return (
    <Box
      sx={{
        display: "flex",
        flexWrap: "wrap",
        alignItems: "center",
        columnGap: 1,
        rowGap: 0.5,
        border: 1,
        borderColor: "divider",
        borderRadius: 1,
        mt: 1,
        px: 2,
        py: 1,
      }}
    >
      <CloudOutlined fontSize="small" sx={{ color: needsReconnect ? "warning.main" : "text.secondary", mr: 0.5 }} />
      <Box sx={{ flex: "1 1 180px", minWidth: 0 }}>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1, minWidth: 0 }}>
          <NoWrapTypography variant="subtitle2">{storage.name}</NoWrapTypography>
          {needsReconnect && (
            <SquareChip
              size="small"
              variant="outlined"
              color="warning"
              label={t("connected.statusReconnect")}
              sx={{ height: "initial", fontSize: (theme) => theme.typography.caption.fontSize, flexShrink: 0 }}
            />
          )}
        </Box>
        <NoWrapTypography variant="caption" color="text.secondary" sx={{ display: "block" }}>
          {[providerName(list, storage.provider), storage.account].filter(Boolean).join(" · ")}
        </NoWrapTypography>
        {needsReconnect && (
          <Typography variant="caption" color="text.secondary" component="div">
            {t("connected.statusReconnectDes")}
          </Typography>
        )}
        {!configured && (
          <Typography variant="caption" color="text.secondary" component="div">
            {t("connected.providerMissing")}
          </Typography>
        )}
      </Box>
      <Box sx={{ display: "flex", alignItems: "center", gap: 0.5, ml: "auto" }}>
        {needsReconnect && allowed && configured && (
          <LoadingButton
            size="small"
            variant="outlined"
            color="warning"
            loading={reconnecting}
            onClick={reconnect}
            startIcon={<ArrowSync />}
          >
            <span>{t("connected.reconnect")}</span>
          </LoadingButton>
        )}
        {allowed && (
          <Tooltip title={t("connected.rename")}>
            <IconButton onClick={() => onRename(storage)} aria-label={t("connected.rename")}>
              <Edit fontSize="small" />
            </IconButton>
          </Tooltip>
        )}
        <Tooltip title={t("connected.disconnect")}>
          <span>
            <IconButton onClick={disconnect} disabled={disconnecting} aria-label={t("connected.disconnect")}>
              <Dismiss fontSize="small" />
            </IconButton>
          </span>
        </Tooltip>
      </Box>
    </Box>
  );
};

const ConnectedSetting = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();
  const errorText = useErrorText();
  const [finish, setFinish] = useQueryState("finish");
  const [flowError, setFlowError] = useQueryState("error");
  const [list, setList] = useState<ConnectedListResponse | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [finishing, setFinishing] = useState(false);
  const [result, setResult] = useState<FlowResult | undefined>(undefined);
  const [connectOpen, setConnectOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState<ConnectedStorage | undefined>(undefined);
  const handled = useRef(false);

  const isAdmin = useMemo(
    () => GroupBS(SessionManager.currentLoginOrNull()?.user).enabled(GroupPermission.is_admin),
    [],
  );

  const load = useCallback(() => {
    setLoading(true);
    setLoadFailed(false);
    return dispatch(getConnectedStorages())
      .then((res) => setList(res))
      .catch(() => setLoadFailed(true))
      .finally(() => setLoading(false));
  }, [dispatch]);

  useEffect(() => {
    // Both values are read once and taken out of the address, so a reload does not
    // repeat the step. The ref guards against the effect running twice in development.
    if (handled.current) {
      return;
    }
    handled.current = true;

    if (flowError) {
      setResult({
        severity: "error",
        message: t(`connected.flowError.${ConnectedFlowErrors.includes(flowError) ? flowError : "provider"}`),
      });
      setFlowError(null);
    }

    if (!finish) {
      load();
      return;
    }

    const code = finish;
    setFinish(null);
    setFinishing(true);
    dispatch(sendConnectedFinish(code))
      .then((storage) => {
        setResult({ severity: "success", message: t("connected.connected", { name: storage.name }) });
      })
      .catch((e) => setResult({ severity: "error", message: errorText(e) }))
      .finally(() => {
        setFinishing(false);
        load();
      });
  }, []);

  const onRenamed = (storage: ConnectedStorage) => {
    setList((l) => l && { ...l, storages: l.storages.map((s) => (s.id == storage.id ? storage : s)) });
  };

  const onDisconnected = (id: string) => {
    setList((l) => l && { ...l, storages: l.storages.filter((s) => s.id != id) });
    enqueueSnackbar({ message: t("connected.disconnected"), variant: "success", action: DefaultCloseAction });
  };

  if (loading || finishing) {
    return (
      <Box sx={{ py: 4, display: "flex", flexDirection: "column", alignItems: "center", gap: 2 }}>
        <FacebookCircularProgress />
        {finishing && (
          <Typography variant="body2" color="text.secondary">
            {t("connected.finishing")}
          </Typography>
        )}
      </Box>
    );
  }

  const storages = list?.storages ?? [];
  const available = !!list?.available;
  const allowed = available && !!list?.allowed;
  const configured = allowed && !!list?.providers.some((p) => p.configured);
  const max = list?.max ?? ConnectedMaxPerUser;
  const full = storages.length >= max;

  return (
    <Stack spacing={1.5}>
      {result && (
        <Alert severity={result.severity} onClose={() => setResult(undefined)}>
          {result.message}
          {result.origin && (
            <>
              {" "}
              <Link href={`${result.origin}/settings?tab=${ConnectedSettingTab}`}>{result.origin}</Link>
            </>
          )}
        </Alert>
      )}
      <SettingForm title={t("connected.title")} lgWidth={12}>
        <Typography variant="caption" color="text.secondary" component="div">
          {t("connected.des")}
        </Typography>

        {loadFailed && (
          <Alert
            severity="error"
            sx={{ mt: 1 }}
            action={
              <Button color="inherit" size="small" onClick={load}>
                {t("connected.retry")}
              </Button>
            }
          >
            {t("connected.loadFailed")}
          </Alert>
        )}
        {list && !available && (
          <Alert severity="info" sx={{ mt: 1 }}>
            {t("connected.unavailable")}
          </Alert>
        )}
        {list && available && !allowed && (
          <Alert severity="info" sx={{ mt: 1 }}>
            {t(storages.length ? "connected.notAllowedWithStorages" : "connected.notAllowed")}
          </Alert>
        )}
        {list && allowed && !configured && (
          <Alert severity="info" sx={{ mt: 1 }}>
            {t("connected.noProvider")}
            {isAdmin && (
              <>
                {" "}
                <Link component={RouterLink} to="/admin/settings?tab=connected">
                  {t("connected.noProviderAdmin")}
                </Link>
              </>
            )}
          </Alert>
        )}

        {list && storages.length == 0 && configured && (
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 1,
              borderRadius: 1,
              border: 1,
              borderColor: "divider",
              px: 1.5,
              py: 1,
              mt: 1,
            }}
          >
            <CloudOutlined fontSize="small" sx={{ color: "text.secondary", flexShrink: 0 }} />
            <Typography color="textSecondary" variant="caption">
              {t("connected.empty")}
            </Typography>
          </Box>
        )}

        {list &&
          storages.map((s) => (
            <StorageRow
              key={s.id}
              storage={s}
              list={list}
              allowed={allowed}
              onRename={setRenameTarget}
              onDisconnected={onDisconnected}
              onFailed={setResult}
            />
          ))}

        {configured && (
          <Box sx={{ mt: 1 }}>
            <SecondaryLoadingButton
              variant="contained"
              disabled={full}
              onClick={() => setConnectOpen(true)}
              startIcon={<Add />}
            >
              <span>{t("connected.connect")}</span>
            </SecondaryLoadingButton>
            {full && (
              <Typography variant="caption" color="text.secondary" component="div" sx={{ mt: 0.5 }}>
                {t("connected.full", { max })}
              </Typography>
            )}
          </Box>
        )}
      </SettingForm>
      <ConnectDialog open={connectOpen} list={list} onClose={() => setConnectOpen(false)} onFailed={setResult} />
      <RenameDialog storage={renameTarget} onClose={() => setRenameTarget(undefined)} onRenamed={onRenamed} />
    </Stack>
  );
};

export default ConnectedSetting;
