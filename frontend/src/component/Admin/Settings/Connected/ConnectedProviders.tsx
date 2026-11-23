import { LoadingButton } from "@mui/lab";
import { Box, FormControl, IconButton, InputAdornment, Stack, Tooltip, Typography } from "@mui/material";
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
  onSaved: (res: ConnectedAdminResponse) => void;
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

  const save = () => {
    setSaving(true);
    dispatch(
      sendConnectedAdmin({
        provider,
        client_id: clientID.trim(),
        client_secret: secret.trim() ? secret.trim() : undefined,
      }),
    )
      .then((res) => {
        setSecret("");
        onSaved(res);
        enqueueSnackbar({ message: t("settings.saved"), variant: "success", action: DefaultCloseAction });
      })
      .catch(() => {})
      .finally(() => setSaving(false));
  };

  return (
    <SettingSection>
      <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
        <Typography variant="h6">{ConnectedProviderNames[provider] ?? provider}</Typography>
        <SquareChip
          size="small"
          variant="outlined"
          color={ready ? "success" : "default"}
          label={t(
            ready
              ? "connected.ready"
              : setup.client_id
                ? "connected.secretMissing"
                : "connected.notSetUp",
          )}
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
              onChange={(e) => setClientID(e.target.value)}
              inputProps={{ maxLength: 512, autoComplete: "off", spellCheck: false }}
            />
            <NoMarginHelperText>{t("connected.clientIdDes")}</NoMarginHelperText>
          </FormControl>
        </SettingForm>
        <SettingForm title={t("connected.clientSecret")} lgWidth={5}>
          <FormControl fullWidth>
            <DenseFilledTextField
              type="password"
              value={secret}
              disabled={!clientID.trim()}
              placeholder={setup.secret_set ? t("connected.secretKept") : undefined}
              onChange={(e) => setSecret(e.target.value)}
              inputProps={{ maxLength: 2048, autoComplete: "new-password" }}
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
  const { enqueueSnackbar } = useSnackbar();
  const [setup, setSetup] = useState<ConnectedAdminResponse | undefined>(undefined);

  useEffect(() => {
    dispatch(getConnectedAdmin())
      .then(setSetup)
      .catch(() => {});
  }, []);

  if (!setup) {
    return (
      <Box sx={{ pt: 6, display: "flex", justifyContent: "center" }}>
        <FacebookCircularProgress />
      </Box>
    );
  }

  const copy = () => {
    copyToClipboard(setup.callback_url).then(() =>
      enqueueSnackbar({ message: t("connected.copied"), variant: "success", action: DefaultCloseAction }),
    );
  };

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
                  htmlInput: { readOnly: true, spellCheck: false },
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <Tooltip title={t("connected.copy")}>
                          <IconButton edge="end" size="small" onClick={copy} aria-label={t("connected.copy")}>
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
        <ProviderForm key={p} provider={p} setup={setup.providers[p]} onSaved={setSetup} />
      ))}
    </Stack>
  );
};

export default ConnectedProviders;
