// Fylvo: updates from the app (desktop/UPDATER.md): version, automatic updates, "Check now".
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { invoke } from "@tauri-apps/api/core";
import { SettingActionItem, SettingItem, SettingsGroup } from "./GeneralSection";

interface UpdateStatus {
  version: string;
  supported: boolean;
  phase: string;
  target: string | null;
  error: string | null;
  last_check: number | null;
}

interface GeneralSettings {
  auto_update: boolean;
}

const BUSY_PHASES = ["checking", "downloading", "waiting", "installing"];

export default function UpdateSettings() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [autoUpdate, setAutoUpdate] = useState(true);

  useEffect(() => {
    let active = true;
    const load = () =>
      invoke<UpdateStatus>("get_update_status")
        .then((s) => active && setStatus(s))
        .catch((e) => console.error("Failed to load update status:", e));
    invoke<GeneralSettings>("get_general_settings")
      .then((s) => active && setAutoUpdate(s.auto_update))
      .catch((e) => console.error("Failed to load settings:", e));
    load();
    const timer = setInterval(load, 2000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, []);

  const handleAutoUpdateChange = async (checked: boolean) => {
    const previous = autoUpdate;
    setAutoUpdate(checked);
    try {
      await invoke("set_auto_update", { enabled: checked });
    } catch (error) {
      console.error("Failed to change update setting:", error);
      setAutoUpdate(previous);
    }
  };

  const handleCheckNow = async () => {
    try {
      await invoke("check_for_update");
      setStatus((s) => (s ? { ...s, phase: "checking", error: null } : s));
    } catch (error) {
      console.error("Failed to start the update check:", error);
    }
  };

  const supported = status?.supported ?? false;
  const busy = status ? BUSY_PHASES.includes(status.phase) : false;

  let line: string = t("updates.idle");
  if (!status) {
    line = "...";
  } else if (!status.supported) {
    line = t("updates.unsupported");
  } else if (status.phase === "failed") {
    line = t("updates.failed", { version: status.target ?? "", reason: status.error ?? "" });
  } else if (status.phase === "downloading" || status.phase === "waiting" || status.phase === "installing") {
    line = t(`updates.${status.phase}`, { version: status.target ?? "" });
  } else if (status.phase === "checking" || status.phase === "up_to_date" || status.phase === "no_drive") {
    line = t(`updates.${status.phase}`);
  }

  return (
    <SettingsGroup title={t("updates.title")}>
      <SettingItem
        title={t("updates.auto")}
        description={t("updates.autoDescription")}
        checked={autoUpdate}
        onChange={handleAutoUpdateChange}
        disabled={!supported}
        isLast={false}
      />
      <SettingActionItem
        title={t("updates.version", { version: status?.version || "-" })}
        description={line}
        actionLabel={t("updates.checkNow")}
        onAction={handleCheckNow}
        disabled={!supported || busy}
        isLast={true}
      />
    </SettingsGroup>
  );
}
