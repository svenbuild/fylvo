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
import { AppError, ConnectedChangedEvent } from "../../../../api/request.ts";
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
import { publishConnectedStorages } from "../../../FileManager/Connected/useConnectedStorages.ts";
import { loadPrefs } from "../../../../redux/thunks/fylvo.ts";

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
      // Shown, so its retry can be reached.
      .catch(() => setVisible(true));
  }, []);
  return visible || openTab == ConnectedSettingTab;
};

// Values of data.reason the server sends with errors of authorize and finish.
const knownReasons = [
  "expired",
  "account",
  "other_account",
  "exists",
  "too_many",
  "setup",
  "not_configured",
  "not_allowed",
  // Started under another address than the main one (the same text as when the web app
  // notices it itself, see openProvider).
  "other_address",
];

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
        if (e.code == ConnectedCode.ObjectExists) {
          return t("connected.reason.exists");
        }
        if (e.code == ConnectedCode.Busy) {
          return t("common:errors.40923");
        }
        if (e.code == ConnectedCode.Unavailable) {
          return t("connected.reason.unavailable");
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
  const target = new URL(url);
  if (target.protocol != "https:" && target.protocol != "http:") {
    throw new Error("unexpected address");
  }
  const back = target.searchParams.get("redirect_uri");
  const origin = back ? new URL(back).origin : undefined;
  if (origin && origin != window.location.origin) {
    return origin;
  }
  window.location.assign(target.href);
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

// The finish step of a sign-in that is still running. It lives outside the component, so
// leaving the tab and coming back while it runs shows its result and a fresh list.
let finishRun: Promise<FlowResult> | undefined;

// Counts renames and disconnects, also of a tab that was left meanwhile, so a list request
// older than one of them is not shown.
let listChanges = 0;

// The sign-in start that is waiting for the server, also across leaving the tab: only one
// at a time, since each start replaces the cookie of the one before.
let authorizeRun: { key: string; done: Promise<unknown> } | undefined;

const RenameDialog = ({
  storage,
  onClose,
  onRenamed,
  onFailed,
}: {
  storage?: ConnectedStorage;
  onClose: () => void;
  onRenamed: (s: ConnectedStorage) => void;
  // The list may be out of date (for example disconnected on another device).
  onFailed: () => void;
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
      .catch(onFailed)
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
  authorizing,
  onClose,
  onConnect,
}: {
  open: boolean;
  list?: ConnectedListResponse;
  authorizing?: string;
  onClose: () => void;
  onConnect: (provider: string) => void;
}) => {
  const { t } = useTranslation();
  const providers = list?.providers.filter((p) => p.configured) ?? [];

  return (
    <DraggableDialog
      title={t("connected.connect")}
      loading={!!authorizing}
      dialogProps={{ open, onClose, fullWidth: true, maxWidth: "xs" }}
    >
      <DialogContent>
        <StyledDialogContentText sx={{ mb: 1.5 }}>{t("connected.connectDes")}</StyledDialogContentText>
        <List disablePadding>
          {providers.map((p) => (
            <ListItemButton
              key={p.type}
              disabled={!!authorizing}
              onClick={() => onConnect(p.type)}
              sx={{ border: 1, borderColor: "divider", borderRadius: 1, mt: 1 }}
            >
              <ListItemIcon sx={{ minWidth: 34 }}>
                <CloudOutlined fontSize="small" />
              </ListItemIcon>
              <ListItemText primary={p.name} slotProps={{ primary: { variant: "subtitle2" } }} />
              {authorizing == p.type && <CircularProgress size={18} />}
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
  authorizing,
  highlight,
  onRename,
  onReconnect,
  onDisconnected,
  onFailed,
}: {
  storage: ConnectedStorage;
  list: ConnectedListResponse;
  allowed: boolean;
  authorizing?: string;
  // Named in the address (?reconnect=<id>): scrolled to, its reconnect button stands out.
  highlight?: boolean;
  onRename: (s: ConnectedStorage) => void;
  onReconnect: (s: ConnectedStorage) => void;
  onDisconnected: (id: string) => void;
  onFailed: () => void;
}) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [disconnecting, setDisconnecting] = useState(false);
  const needsReconnect = storage.status == ConnectedStatus.reconnect;
  const configured = list.providers.some((p) => p.type == storage.provider && p.configured);
  const row = useRef<HTMLDivElement>(null);
  const reconnectButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (highlight) {
      row.current?.scrollIntoView({ block: "center" });
      reconnectButton.current?.focus();
    }
  }, [highlight, needsReconnect]);

  const disconnect = () => {
    dispatch(confirmOperation(t("connected.disconnectConfirm", { name: storage.name, account: storage.account })))
      .then(() => {
        setDisconnecting(true);
        return dispatch(sendConnectedDisconnect(storage.id))
          .then(() => onDisconnected(storage.id), onFailed)
          .finally(() => setDisconnecting(false));
      })
      .catch(() => {});
  };

  return (
    <Box
      ref={row}
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
        <Typography variant="caption" color="text.secondary" component="div" sx={{ overflowWrap: "anywhere" }}>
          {[providerName(list, storage.provider), storage.account].filter(Boolean).join(" · ")}
        </Typography>
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
            variant={highlight ? "contained" : "outlined"}
            color="warning"
            ref={reconnectButton}
            loading={authorizing == storage.id}
            disabled={!!authorizing}
            onClick={() => onReconnect(storage)}
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
  // ?reconnect=<id> (from the apps): only points at the storage; starting a sign-in
  // always takes a click.
  const [reconnectParam, setReconnectParam] = useQueryState("reconnect");
  const [highlight, setHighlight] = useState(reconnectParam);
  const [list, setList] = useState<ConnectedListResponse | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [finishing, setFinishing] = useState(false);
  const [result, setResult] = useState<FlowResult | undefined>(undefined);
  const [connectOpen, setConnectOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState<ConnectedStorage | undefined>(undefined);
  // A start from before leaving the tab, taken once: its end clears the pending state.
  const [pendingRun] = useState(() => authorizeRun);
  // Provider type (connect) or connection id (reconnect) of the sign-in being started.
  const [authorizing, setAuthorizing] = useState<string | undefined>(pendingRun?.key);
  const handled = useRef(false);
  const mounted = useRef(true);
  useResetOnBack(() => setAuthorizing(undefined));

  useEffect(() => {
    mounted.current = true;
    // A start from before leaving the tab: wait for it; it does not leave the page any more.
    pendingRun?.done.finally(() => mounted.current && setAuthorizing(undefined));
    return () => {
      mounted.current = false;
    };
  }, []);

  // The sidebar and the breadcrumb show the same names.
  useEffect(() => {
    if (list) {
      publishConnectedStorages(list.storages);
    }
  }, [list]);

  const isAdmin = useMemo(
    () => GroupBS(SessionManager.currentLoginOrNull()?.user).enabled(GroupPermission.is_admin),
    [],
  );

  // The spinner shows only until the first list is there, so a refresh keeps open dialogs.
  // After a failed refresh the old list is dropped, so no action works on stale data.
  // An open rename dialog closes when its storage is gone or may no longer be renamed.
  const load = useCallback((): Promise<void> => {
    // A shown error stays, with its retry button disabled, until the answer is there.
    setRefreshing(true);
    const before = listChanges;
    return dispatch(getConnectedStorages())
      .then((res) => {
        if (listChanges != before) {
          return load();
        }
        setList(res);
        setLoadFailed(false);
        setRenameTarget((r) => (r && res.allowed && res.storages.some((s) => s.id == r.id) ? r : undefined));
      })
      .catch(() => {
        setList(undefined);
        setRenameTarget(undefined);
        setLoadFailed(true);
      })
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, [dispatch]);

  // A 40921 anywhere (for example during a download) makes this list out of date.
  useEffect(() => {
    const onChanged = () => {
      load();
    };
    window.addEventListener(ConnectedChangedEvent, onChanged);
    return () => window.removeEventListener(ConnectedChangedEvent, onChanged);
  }, [load]);

  useEffect(() => {
    // ?reconnect=<id> may also change while the tab is open.
    if (reconnectParam) {
      setHighlight(reconnectParam);
      setReconnectParam(null);
      if (handled.current) {
        // The tab was open: its list may not know yet that the storage needs a reconnect.
        load();
      }
    }
  }, [reconnectParam]);

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

    if (finish) {
      const code = finish;
      setFinish(null);
      const run: Promise<FlowResult> = dispatch(sendConnectedFinish(code)).then(
        (storage) => ({ severity: "success", message: t("connected.connected", { name: storage.name }) }),
        (e) => ({ severity: "error", message: errorText(e) }),
      );
      finishRun = run;
      run.then(() => {
        if (finishRun == run) {
          finishRun = undefined;
        }
      });
    }

    if (!finishRun) {
      load();
      return;
    }
    setFinishing(true);
    finishRun.then((res) => {
      setResult(res);
      setFinishing(false);
      load();
    });
  }, []);

  const authorize = (provider: string, reconnect?: string) => {
    if (authorizeRun) {
      return;
    }
    const key = reconnect ?? provider;
    setAuthorizing(key);
    const done = dispatch(sendConnectedAuthorize(provider, reconnect))
      .then((res) => {
        if (!mounted.current) {
          // The tab was left meanwhile: do not take the browser away from where it is now.
          return;
        }
        // On success the page is left; the state stays until the browser is gone.
        const origin = openProvider(res.url);
        if (origin) {
          setAuthorizing(undefined);
          setConnectOpen(false);
          setResult({ severity: "error", message: t("connected.reason.other_address"), origin });
        }
      })
      .catch((e) => {
        if (!mounted.current) {
          return;
        }
        setAuthorizing(undefined);
        setConnectOpen(false);
        setResult({ severity: "error", message: errorText(e) });
        // The refusal may come from a change elsewhere (permission, another tab).
        load();
      })
      .finally(() => {
        authorizeRun = undefined;
      });
    authorizeRun = { key, done };
  };

  const onRenamed = (storage: ConnectedStorage) => {
    listChanges++;
    setList((l) => l && { ...l, storages: l.storages.map((s) => (s.id == storage.id ? storage : s)) });
  };

  const onDisconnected = (id: string) => {
    listChanges++;
    setList((l) => l && { ...l, storages: l.storages.filter((s) => s.id != id) });
    // The server drops the sidebar entries into it; show that at once.
    dispatch(loadPrefs());
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
              <Button color="inherit" size="small" disabled={refreshing} onClick={load}>
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
              authorizing={authorizing}
              highlight={s.id == highlight}
              onRename={setRenameTarget}
              onReconnect={(r) => authorize(r.provider, r.id)}
              onDisconnected={onDisconnected}
              onFailed={load}
            />
          ))}

        {configured && (
          <Box sx={{ mt: 1 }}>
            <SecondaryLoadingButton
              variant="contained"
              disabled={full || !!authorizing}
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
      <ConnectDialog
        open={connectOpen}
        list={list}
        authorizing={authorizing}
        onClose={() => setConnectOpen(false)}
        onConnect={(provider) => authorize(provider)}
      />
      <RenameDialog
        storage={renameTarget}
        onClose={() => setRenameTarget(undefined)}
        onRenamed={onRenamed}
        onFailed={load}
      />
    </Stack>
  );
};

export default ConnectedSetting;
