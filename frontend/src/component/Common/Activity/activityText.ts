import { TFunction } from "i18next";
import { ActivityEntry, ActivityType } from "../../../api/activity.ts";

// Fylvo: turns activity log entries into sentences. Texts are in application.json under "activity".

const accountTypes = new Set<number>([
  ActivityType.login,
  ActivityType.login_failed,
  ActivityType.login_locked,
  ActivityType.password_change,
  ActivityType.two_fa_enable,
  ActivityType.two_fa_disable,
  ActivityType.app_revoked,
]);

export const isAccountActivity = (entry: ActivityEntry) => accountTypes.has(entry.type);

export const isFailedSignIn = (entry: ActivityEntry) =>
  entry.type === ActivityType.login_failed || entry.type === ActivityType.login_locked;

// actorName is who did it: an account, a guest of a link or the system.
export const actorName = (t: TFunction, entry: ActivityEntry) => {
  if (entry.system) {
    return t("activity.system", { ns: "application" });
  }
  if (entry.guest) {
    return t("activity.guest", { ns: "application" });
  }
  if (entry.actor) {
    return entry.actor.nickname || entry.actor.email || t("activity.deletedAccount", { ns: "application" });
  }
  return t("activity.someone", { ns: "application" });
};

const folderName = (t: TFunction, name?: string) => name || t("activity.myFiles", { ns: "application" });

// activityText is the sentence for an entry.
export const activityText = (t: TFunction, entry: ActivityEntry) => {
  const c = entry.content ?? {};
  let key = `activity.type_${entry.type}`;
  if (entry.type === ActivityType.password_change && (c.method === "admin" || c.method === "reset")) {
    key += `_${c.method}`;
  }
  if (entry.type === ActivityType.login && c.method === "app" && c.target) {
    key += "_app";
  }

  return t(key, {
    ns: "application",
    actor: actorName(t, entry),
    name: c.name ?? "",
    oldName: c.old_name ?? "",
    to: folderName(t, c.to_folder),
    kind: c.target ? t(`activity.share_${c.target}`, { ns: "application", defaultValue: c.target }) : "",
    method: c.method ? t(`activity.method_${c.method}`, { ns: "application", defaultValue: c.method }) : "",
    // Apps are named by a text key, for example "application:oauth.fylvoDesktop".
    target: c.target ? t(c.target, { defaultValue: c.target }) : "",
    defaultValue: t("activity.unknown", { ns: "application" }),
  });
};

// deviceName tells the device or way of an entry from its user agent, as far as it can be told.
export const deviceName = (t: TFunction, entry: ActivityEntry) => {
  if (entry.via === "webdav" || entry.content?.method === "webdav") {
    return "WebDAV";
  }
  if (entry.via === "office") {
    return "Office";
  }

  const ua = entry.user_agent ?? "";
  if (!ua) {
    return "";
  }
  if (ua.startsWith("fylvo-desktop/")) {
    return t("activity.windowsApp", { ns: "application" });
  }
  if (ua.startsWith("okhttp/") || ua.includes("FylvoAndroid")) {
    return t("activity.androidApp", { ns: "application" });
  }

  const browser = ua.includes("Edg/")
    ? "Edge"
    : ua.includes("Firefox/")
      ? "Firefox"
      : ua.includes("Chrome/")
        ? "Chrome"
        : ua.includes("Safari/")
          ? "Safari"
          : "";
  const system = ua.includes("Android")
    ? "Android"
    : ua.includes("iPhone") || ua.includes("iPad")
      ? "iOS"
      : ua.includes("Windows")
        ? "Windows"
        : ua.includes("Mac OS")
          ? "macOS"
          : ua.includes("Linux")
            ? "Linux"
            : "";
  const name = [browser, system].filter(Boolean).join(t("activity.on", { ns: "application" }));
  return name || ua;
};
