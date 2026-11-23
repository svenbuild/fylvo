import { PersonOutline } from "@mui/icons-material";
import { Box, Container, Paper } from "@mui/material";
import { selectedSurface } from "../../../fylvoTheme.ts";
import { useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { CSSTransition, SwitchTransition } from "react-transition-group";
import { getUserSettings } from "../../../api/api.ts";
import { UserSettings } from "../../../api/user.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { loadSiteConfig } from "../../../redux/thunks/site.ts";
import FacebookCircularProgress from "../../Common/CircularProgress.tsx";
import ResponsiveTabs, { Tab } from "../../Common/ResponsiveTabs.tsx";
import EditSetting from "../../Icons/EditSetting.tsx";
import LockClosedOutlined from "../../Icons/LockClosedOutlined.tsx";
import PageContainer from "../PageContainer.tsx";
import PageHeader, { PageTabQuery } from "../PageHeader.tsx";
import PreferenceSetting from "./PreferenceSetting.tsx";
import ProfileSetting from "./ProfileSetting.tsx";
import SecuritySetting from "./Security/SecuritySetting.tsx";
import ConnectedSetting, { useConnectedTabVisible } from "./Connected/ConnectedSetting.tsx";
import CloudOutlined from "../../Icons/CloudOutlined.tsx";

export enum SettingPageTab {
  Profile = "profile",
  Preference = "preference",
  Security = "security",
  Connected = "connected",
}

const Setting = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [tab, setTab] = useQueryState(PageTabQuery);
  const [loading, setLoading] = useState(true);
  const [setting, setSetting] = useState<UserSettings | undefined>(undefined);
  const connectedVisible = useConnectedTabVisible(tab);

  useEffect(() => {
    setLoading(true);
    dispatch(loadSiteConfig("login"));
    dispatch(getUserSettings())
      .then((res) => {
        setSetting(res);
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  const tabs: Tab<SettingPageTab>[] = useMemo(() => {
    const res = [];
    res.push(
      ...[
        {
          label: t("application:setting.profile"),
          value: SettingPageTab.Profile,
          icon: <PersonOutline />,
        },
        {
          label: t("application:setting.preference"),
          value: SettingPageTab.Preference,
          icon: <EditSetting />,
        },
        {
          label: t("application:setting.security"),
          value: SettingPageTab.Security,
          icon: <LockClosedOutlined />,
        },
      ],
    );
    if (connectedVisible) {
      res.push({
        label: t("application:connected.title"),
        value: SettingPageTab.Connected,
        icon: <CloudOutlined />,
      });
    }
    return res;
  }, [t, connectedVisible]);

  return (
    <PageContainer compact contentWidth={780}>
      <Container maxWidth="lg">
        <PageHeader title={t("application:navbar.setting")} />
        <ResponsiveTabs
          value={tab ?? SettingPageTab.Profile}
          onChange={(_e, newValue) => setTab(newValue)}
          tabs={tabs}
        />
        <SwitchTransition>
          <CSSTransition
            addEndListener={(node, done) => node.addEventListener("transitionend", done, false)}
            classNames="fade"
            key={`${loading}`}
          >
            <Box>
              {loading && (
                <Box
                  sx={{
                    pt: 6,
                    height: "100%",
                    display: "flex",
                    justifyContent: "center",
                    alignItems: "center",
                  }}
                >
                  <FacebookCircularProgress />
                </Box>
              )}
              {!loading && setting && (
                <Paper
                  variant="outlined"
                  sx={{
                    mt: 2,
                    p: "14px",
                    bgcolor: "background.paper",
                    "& [data-setting-label]": { fontSize: 12, color: "text.secondary" },
                    "& .MuiToggleButton-root": {
                      textTransform: "none",
                      minHeight: 30,
                      "&.Mui-selected, &.Mui-selected:hover": {
                        color: "text.primary",
                        bgcolor: selectedSurface,
                      },
                    },
                    "@media (max-width: 599.95px), (pointer: coarse)": {
                      "& .MuiToggleButton-root, & .MuiFormControlLabel-root": { minHeight: 40 },
                    },
                  }}
                >
                  {(!tab || tab == SettingPageTab.Profile) && (
                    <ProfileSetting setting={setting} setSetting={setSetting} />
                  )}
                  {tab == SettingPageTab.Preference && <PreferenceSetting setting={setting} setSetting={setSetting} />}
                  {tab == SettingPageTab.Security && <SecuritySetting setting={setting} setSetting={setSetting} />}
                  {tab == SettingPageTab.Connected && <ConnectedSetting />}
                </Paper>
              )}
            </Box>
          </CSSTransition>
        </SwitchTransition>
      </Container>
    </PageContainer>
  );
};

export default Setting;
