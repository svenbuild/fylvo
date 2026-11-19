import dayjs from "dayjs";
import { default as duration, default as plugin } from "dayjs/plugin/duration";
import timezone from "dayjs/plugin/timezone";
import utc from "dayjs/plugin/utc";
import { register } from "timeago.js";
import ar from "timeago.js/esm/lang/ar";
import de from "timeago.js/esm/lang/de";
import es from "timeago.js/esm/lang/es";
import fr from "timeago.js/esm/lang/fr";
import it from "timeago.js/esm/lang/it";
import ja from "timeago.js/esm/lang/ja";
import ko from "timeago.js/esm/lang/ko";
import pl from "timeago.js/esm/lang/pl";
import pt_BR from "timeago.js/esm/lang/pt_BR";
import ru from "timeago.js/esm/lang/ru";
import zh_TW from "timeago.js/esm/lang/zh_TW";
import i18next from "../i18n";
import SessionManager, { UserSettings } from "../session";

dayjs.extend(utc);
dayjs.extend(timezone);
dayjs.extend(duration);

// Relative times ("vor 5 Minuten") follow the UI language. The keys are the
// "timeAgoLocaleCode" values of the locale files; English and Simplified Chinese are built in.
const timeAgoLocales = { ar_AR: ar, de_DE: de, es_ES: es, fr_FR: fr, it_IT: it, ja_JP: ja, ko_KR: ko, pl_PL: pl, pt_BR, ru, zh_TW };
Object.entries(timeAgoLocales).forEach(([code, locale]) => register(code, locale));

let userTimezone = "";
try {
  userTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
} catch (e) {
  console.log(e);
}

if (!userTimezone || userTimezone == "Etc/Unknown") {
  userTimezone = "Europe/Zurich";
}

const preferTimeZone: string | undefined = SessionManager.get(UserSettings.TimeZone);
export let timeZone = preferTimeZone ? preferTimeZone : userTimezone;

export function refreshTimeZone() {
  timeZone = SessionManager.get(UserSettings.TimeZone);
  timeZone = timeZone ? timeZone : userTimezone;
}

export function formatLocalTime(time: string | dayjs.Dayjs, ignoreTz?: boolean) {
  let t: dayjs.Dayjs;
  if (typeof time === "string") {
    t = dayjs(time);
  } else {
    t = time;
  }

  return i18next.t("intlDateTime", {
    ns: "common",
    val: ignoreTz ? t.utc().tz(timeZone, true).toDate() : t.toDate(),
    formatParams: {
      val: {
        year: "numeric",
        month: "numeric",
        day: "numeric",
        hour: "numeric",
        minute: "numeric",
        second: "numeric",
        timeZone: ignoreTz ? undefined : timeZone,
      },
    },
  });
}

export function formatDuration(duration: plugin.Duration): string {
  const s = duration.asSeconds();
  const m = duration.asMinutes();
  const h = duration.asHours();
  if (s < 60) {
    return duration.format(i18next.t("seconds", { ns: "common" }));
  } else if (m < 60) {
    return duration.format(i18next.t("minutes", { ns: "common" }));
  } else if (h < 24) {
    return duration.format(i18next.t("hours", { ns: "common" }));
  } else {
    return i18next.t("days", { ns: "common", d: Math.round(duration.asDays()) });
  }
}

// export function validateTimeZone(name) {
//   try {
//     dayjs().tz(name).format();
//   } catch (e) {
//     return false;
//   }
//   return true;
// }
