import { Box, IconButton, Stack, ToggleButton, ToggleButtonGroup, Tooltip, Typography } from "@mui/material";
import { ArrowDownward, ArrowUpward } from "@mui/icons-material";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { getFileList, sendPatchViewSync } from "../../../api/api.ts";
import { ExplorerView } from "../../../api/explorer.ts";
import { Density, HomeWidget, StartPage } from "../../../api/fylvo.ts";
import { defaultPath } from "../../../hooks/useNavigation.tsx";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { loadPrefs, resetPrefs, savePrefs } from "../../../redux/thunks/fylvo.ts";
import SessionManager, { UserSettings } from "../../../session";
import { SecondaryButton } from "../../Common/StyledComponents.tsx";
import { ColorCircle, SelectorBox } from "../../FileManager/FileInfo/ColorCircle/CircleColorSelector.tsx";
import { SwitchPopover } from "../../Frame/NavBar/DarkThemeSwitcher.tsx";
import Eye from "../../Icons/Eye.tsx";
import EyeOff from "../../Icons/EyeOff.tsx";
import SettingForm from "./SettingForm.tsx";

// Fylvo: everything about how Fylvo looks and starts, stored with the account (prefs).

const widgetLabels: Record<string, string> = {
  recent: "customize.widgetRecent",
  favorites: "customize.favorites",
  links: "customize.widgetLinks",
  tags: "customize.tags",
  storage: "navbar.storage",
};

const sortOptions = ["created_at", "updated_at", "name", "size"];

const Choice = <T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: [T, string][];
  onChange: (value: T) => void;
}) => (
  <ToggleButtonGroup size="small" exclusive value={value} onChange={(_e, v) => v !== null && onChange(v)}>
    {options.map(([v, label]) => (
      <ToggleButton key={v} value={v}>
        {label}
      </ToggleButton>
    ))}
  </ToggleButtonGroup>
);

const CustomizeSetting = () => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const prefs = useAppSelector((state) => state.fylvo.prefs);
  const { themes } = useAppSelector((s) => s.siteConfig.basic.config);
  const [rootView, setRootView] = useState<ExplorerView | undefined>();
  const viewSync = !prefs || prefs.view.layout !== undefined;

  const themeKeys = useMemo(() => {
    try {
      return themes ? Object.keys(JSON.parse(themes)) : [];
    } catch (e) {
      return [];
    }
  }, [themes]);

  useEffect(() => {
    dispatch(loadPrefs());
    // The stored view of "My files" (without page size or sort in the request, so nothing overrides it).
    dispatch(getFileList({ uri: defaultPath }))
      .then((res) => setRootView(res.view))
      .catch(() => {});
  }, []);

  if (!prefs) {
    return null;
  }

  const widgets = prefs.home.widgets;
  const saveWidgets = (next: HomeWidget[]) => dispatch(savePrefs({ home: { widgets: next } }));
  const moveWidget = (from: number, to: number) => {
    if (to < 0 || to >= widgets.length) return;
    const next = [...widgets];
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    saveWidgets(next);
  };

  // Writes the view of "My files", which every folder without its own view follows. Only layout
  // and sort change; missing fields are read the way the file list reads them.
  const saveView = async (change: Partial<ExplorerView>) => {
    const base = rootView ?? { page_size: 0 };
    const view: ExplorerView = {
      page_size: base.page_size || SessionManager.getWithFallback(UserSettings.PageSize),
      order: base.order,
      order_direction: base.order_direction ?? "asc",
      view: base.view ?? prefs.view.layout,
      thumbnail: !!base.thumbnail,
      columns: base.columns ?? SessionManager.getWithFallback(UserSettings.ListViewColumns),
      gallery_width: base.gallery_width ?? SessionManager.getWithFallback(UserSettings.GalleryWidth),
      ...change,
    };
    try {
      await dispatch(sendPatchViewSync({ uri: defaultPath, view }));
      setRootView(view);
      dispatch(loadPrefs());
    } catch (e) {}
  };

  return (
    <Stack spacing={3}>
      <SettingForm title={t("customize.startPage")} lgWidth={12}>
        <Choice<StartPage>
          value={prefs.start_page}
          options={[
            ["", t("customize.startDefault")],
            ["home", t("customize.start")],
            ["my_files", t("navbar.myFiles")],
          ]}
          onChange={(start_page) => dispatch(savePrefs({ start_page }))}
        />
      </SettingForm>
      <SettingForm title={t("customize.startBlocks")} lgWidth={12}>
        <Box sx={{ border: 1, borderColor: "divider", borderRadius: 1, maxWidth: 420 }}>
          {widgets.map((w, i) => (
            <Stack
              key={w.type}
              direction="row"
              alignItems="center"
              sx={{
                px: 1,
                minHeight: 34,
                borderTop: i == 0 ? 0 : 1,
                borderColor: "divider",
                opacity: w.hidden ? 0.5 : 1,
              }}
            >
              <Typography variant="body2" sx={{ flexGrow: 1 }}>
                {t(widgetLabels[w.type])}
              </Typography>
              <IconButton size="small" disabled={i == 0} onClick={() => moveWidget(i, i - 1)}>
                <ArrowUpward sx={{ fontSize: 16 }} />
              </IconButton>
              <IconButton size="small" disabled={i == widgets.length - 1} onClick={() => moveWidget(i, i + 1)}>
                <ArrowDownward sx={{ fontSize: 16 }} />
              </IconButton>
              <Tooltip title={w.hidden ? t("customize.show") : t("customize.hide")}>
                <IconButton
                  size="small"
                  onClick={() => saveWidgets(widgets.map((x, j) => (j == i ? { ...x, hidden: !x.hidden } : x)))}
                >
                  {w.hidden ? <EyeOff fontSize="small" /> : <Eye fontSize="small" />}
                </IconButton>
              </Tooltip>
            </Stack>
          ))}
        </Box>
      </SettingForm>
      <SettingForm title={t("customize.density")} lgWidth={12}>
        <Choice<Density>
          value={prefs.view.density}
          options={[
            ["compact", t("customize.densityCompact")],
            ["normal", t("customize.densityNormal")],
            ["comfortable", t("customize.densityComfortable")],
          ]}
          onChange={(density) => dispatch(savePrefs({ view: { density } }))}
        />
      </SettingForm>
      {viewSync && (
        <SettingForm title={t("customize.defaultView")} lgWidth={12}>
          <Stack spacing={1} alignItems="flex-start">
            <Choice<string>
              value={prefs.view.layout ?? "grid"}
              options={[
                ["list", t("customize.layoutList")],
                ["grid", t("customize.layoutGrid")],
                ["gallery", t("customize.layoutGallery")],
              ]}
              onChange={(layout) => saveView({ view: layout })}
            />
            <Stack direction="row" spacing={1} sx={{ flexWrap: "wrap", rowGap: 1 }}>
              <Choice<string>
                value={prefs.view.sort_by ?? "created_at"}
                options={sortOptions.map((o) => [o, t(`customize.sort_${o}`)] as [string, string])}
                onChange={(order) => saveView({ order })}
              />
              <Choice<string>
                value={prefs.view.sort_direction ?? "asc"}
                options={[
                  ["asc", t("customize.ascending")],
                  ["desc", t("customize.descending")],
                ]}
                onChange={(order_direction) => saveView({ order_direction })}
              />
            </Stack>
            <Typography variant="caption" color="text.secondary">
              {t("customize.defaultViewHint")}
            </Typography>
          </Stack>
        </SettingForm>
      )}
      <SettingForm title={t("application:setting.darkMode")} lgWidth={12}>
        <SwitchPopover />
      </SettingForm>
      {themeKeys.length > 1 && (
        <SettingForm title={t("application:setting.themeColor")} lgWidth={12}>
          <SelectorBox sx={{ gap: 1 }}>
            {themeKeys.map((color) => (
              <ColorCircle
                key={color}
                size={22}
                color={color}
                onClick={() => dispatch(savePrefs({ appearance: { accent: color } }))}
                selected={prefs.appearance.accent == color}
              />
            ))}
          </SelectorBox>
        </SettingForm>
      )}
      <SettingForm title={t("customize.resetTitle")} lgWidth={12}>
        <Stack spacing={1} alignItems="flex-start">
          <Typography variant="caption" color="text.secondary">
            {t("customize.resetHint")}
          </Typography>
          <SecondaryButton variant="contained" size="small" onClick={() => dispatch(resetPrefs())}>
            {t("customize.reset")}
          </SecondaryButton>
        </Stack>
      </SettingForm>
    </Stack>
  );
};

export default CustomizeSetting;
