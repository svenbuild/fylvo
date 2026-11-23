import { LoadingButton } from "@mui/lab";
import { Alert, Box, Button, FormControl, IconButton, InputAdornment, Stack, Tooltip, Typography } from "@mui/material";
import { useSnackbar } from "notistack";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ConnectedAdminResponse,
  ConnectedProvider,
  ConnectedProviderNames,
  ConnectedProviderSetup,
  getConnectedAdmin,
  sendConnectedAdmin,
} from "../../../../api/connected.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { confirmOperation } from "../../../../redux/thunks/dialog.ts";
import { copyToClipboard } from "../../../../util";
import FacebookCircularProgress from "../../../Common/CircularProgress.tsx";
import { DefaultCloseAction } from "../../../Common/Snackbar/snackbar.tsx";
import { DenseFilledTextField, SquareChip } from "../../../Common/StyledComponents.tsx";
import CopyOutlined from "../../../Icons/CopyOutlined.tsx";
import SettingForm from "../../../Pages/Setting/SettingForm.tsx";
import { NoMarginHelperText, SettingSection, SettingSectionContent } from "../Settings.tsx";

// Fylvo: the OAuth clients of this installation for connected storages. A secret can be
// set, never read back; the server only says whether one is stored.

const providerOrder = [ConnectedProvider.drive, ConnectedProvider.onedrive];

const ProviderForm = ({
  provider,
  setup,
  onSaved,
}: {
  provider: string;
  setup: ConnectedProviderSetup;
  onSaved: (provider: string, res: ConnectedAdminResponse) => void;
}) => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();
  const [clientID, setClientID] = useState(setup.client_id);
  const [secret, setSecret] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setClientID(setup.client_id);
  }, [setup.client_id]);

  const changed = clientID.trim() != setup.client_id || secret.trim() != "";
  const ready = !!setup.client_id && setup.secret_set;
  const providerTitle = ConnectedProviderNames[provider] ?? provider;

  const save = () => {
    // An empty client id removes the provider; connections to it stop working.
    const confirm =
      !clientID.trim() && setup.client_id
        ? dispatch(confirmOperation(t("connected.removeConfirm", { provider: providerTitle })))
        : Promise.resolve();
    confirm
      .then(() => {
        setSaving(true);
        return dispatch(
          sendConnectedAdmin({
            provider,
            client_id: clientID.trim(),
            client_secret: secret.trim() ? secret.trim() : undefined,
          }),
        )
          .then((res) => {
            setSecret("");
            onSaved(provider, res);
            enqueueSnackbar({ message: t("settings.saved"), variant: "success", action: DefaultCloseAction });
          })
          .finally(() => setSaving(false));
      })
      .catch(() => {});
  };

  return (
    <SettingSection>
      <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
        <Typography variant="h6">{providerTitle}</Typography>
        <SquareChip
          size="small"
          variant="outlined"
          color={ready ? "success" : "default"}
          label={t(ready ? "connected.ready" : setup.client_id ? "connected.secretMissing" : "connected.notSetUp")}
          sx={{ height: "initial", fontSize: (theme) => theme.typography.caption.fontSize }}
        />
      </Box>
      <SettingSectionContent>
        <Typography variant="body2" color="text.secondary">
          {t(`connected.${provider}Des`)}
        </Typography>
        <SettingForm title={t("connected.clientId")} lgWidth={5}>
          <FormControl fullWidth>
            <DenseFilledTextField
              value={clientID}
              disabled={saving}
              onChange={(e) => setClientID(e.target.value)}
              inputProps={{
                maxLength: 512,
                autoComplete: "off",
                spellCheck: false,
                "aria-label": `${providerTitle}: ${t("connected.clientId")}`,
              }}
            />
            <NoMarginHelperText>{t("connected.clientIdDes")}</NoMarginHelperText>
          </FormControl>
        </SettingForm>
        <SettingForm title={t("connected.clientSecret")} lgWidth={5}>
          <FormControl fullWidth>
            <DenseFilledTextField
              type="password"
              value={secret}
              disabled={saving || !clientID.trim()}
              placeholder={setup.secret_set ? t("connected.secretKept") : undefined}
              onChange={(e) => setSecret(e.target.value)}
              inputProps={{
                maxLength: 2048,
                autoComplete: "new-password",
                "aria-label": `${providerTitle}: ${t("connected.clientSecret")}`,
              }}
            />
            <NoMarginHelperText>
              {t(setup.secret_set ? "connected.clientSecretSetDes" : "connected.clientSecretDes")}
            </NoMarginHelperText>
          </FormControl>
        </SettingForm>
        <Box>
          <LoadingButton variant="contained" disabled={!changed} loading={saving} onClick={save}>
            <span>{t("settings.save")}</span>
          </LoadingButton>
        </Box>
      </SettingSectionContent>
    </SettingSection>
  );
};

const ConnectedProviders = () => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const [setup, setSetup] = useState<ConnectedAdminResponse | undefined>(undefined);
  const [loadFailed, setLoadFailed] = useState(false);

  const load = () => {
    setLoadFailed(false);
    dispatch(getConnectedAdmin())
      .then(setSetup)
      .catch(() => setLoadFailed(true));
  };

  useEffect(load, []);

  // Each form takes over only its own provider, so answers of two saves cannot undo each
  // other.
  const onSaved = (provider: string, res: ConnectedAdminResponse) => {
    setSetup((s) => s && { ...s, providers: { ...s.providers, [provider]: res.providers[provider] } });
  };

  if (loadFailed) {
    return (
      <Alert
        severity="error"
        action={
          <Button color="inherit" size="small" onClick={load}>
            {t("connected.retry")}
          </Button>
        }
      >
        {t("connected.loadFailed")}
      </Alert>
    );
  }

  if (!setup) {
    return (
      <Box sx={{ pt: 6, display: "flex", justifyContent: "center" }}>
        <FacebookCircularProgress />
      </Box>
    );
  }

  const providers = [
    ...providerOrder.filter((p) => setup.providers[p]),
    ...Object.keys(setup.providers).filter((p) => !providerOrder.includes(p)),
  ];

  return (
    <Stack spacing={5}>
      <SettingSection>
        <Typography variant="h6">{t("connected.title")}</Typography>
        <SettingSectionContent>
          <Typography variant="body2" color="text.secondary">
            {t("connected.des")}
          </Typography>
          <SettingForm title={t("connected.callbackUrl")} lgWidth={8}>
            <FormControl fullWidth>
              <DenseFilledTextField
                value={setup.callback_url}
                slotProps={{
                  htmlInput: { readOnly: true, spellCheck: false, "aria-label": t("connected.callbackUrl") },
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <Tooltip title={t("connected.copy")}>
                          <IconButton
                            edge="end"
                            size="small"
                            onClick={() => copyToClipboard(setup.callback_url)}
                            aria-label={t("connected.copy")}
                          >
                            <CopyOutlined fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <NoMarginHelperText>{t("connected.callbackUrlDes")}</NoMarginHelperText>
            </FormControl>
          </SettingForm>
        </SettingSectionContent>
      </SettingSection>
      {providers.map((p) => (
        <ProviderForm key={p} provider={p} setup={setup.providers[p]} onSaved={onSaved} />
      ))}
    </Stack>
  );
};

export default ConnectedProviders;
