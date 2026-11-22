import { FileResponse, Share } from "../../api/explorer.ts";
import {
  AppearanceMode,
  Density,
  getPrefs,
  getTags,
  Prefs,
  PrefsPatch,
  sendAddFavorites,
  sendPatchPrefs,
  sendRemoveFavorites,
  sendResetPrefs,
} from "../../api/fylvo.ts";
import SessionManager, { UserSettings } from "../../session";
import CrUri, { Filesystem } from "../../util/uri.ts";
import {
  increaseFavoritesGeneration,
  readCachedPrefs,
  setFavoriteIds,
  setPrefs,
  setTags,
  writeCachedPrefs,
} from "../fylvoSlice.ts";
import { setDarkMode, setPreferredTheme } from "../globalStateSlice.ts";
import { AppThunk } from "../store.ts";

// Fylvo: loading and saving the account preferences (design/customize-plan.md).

const modeToDark = (mode: AppearanceMode): boolean | undefined =>
  mode == "dark" ? true : mode == "light" ? false : undefined;

// applyDensity sets the row heights of the file list and the sidebar. Compact is the default look.
const densityHeights: Record<Density, [string, string] | undefined> = {
  compact: undefined,
  normal: ["38px", "32px"],
  comfortable: ["46px", "36px"],
};

export const applyDensity = (density?: Density) => {
  const heights = density ? densityHeights[density] : undefined;
  const root = document.documentElement.style;
  if (heights) {
    root.setProperty("--fylvo-row-height", heights[0]);
    root.setProperty("--fylvo-nav-height", heights[1]);
  } else {
    root.removeProperty("--fylvo-row-height");
    root.removeProperty("--fylvo-nav-height");
  }
};

// The browser's own light/dark choice (UserSettings.PreferredDarkMode) stays untouched by the
// account: it applies again when the account has no mode saved (for example after a reset).
const browserDarkMode = (): boolean | undefined => SessionManager.get(UserSettings.PreferredDarkMode);

export const accountDarkMode = (prefs?: Prefs): boolean | undefined =>
  prefs && prefs.customized.includes("mode") ? modeToDark(prefs.appearance.mode) : browserDarkMode();

// applyPrefs stores the preferences and applies what the browser has to know: light or dark and
// the accent color.
export function applyPrefs(prefs: Prefs): AppThunk {
  return (dispatch) => {
    dispatch(setPrefs(prefs));
    writeCachedPrefs(prefs);
    applyDensity(prefs.view.density);
    dispatch(setDarkMode(accountDarkMode(prefs)));
    dispatch(setPreferredTheme(prefs.appearance.accent));
  };
}

// startPath is where the app opens: the start page when the account chose it, else the files.
export const startPath = (prefs?: Prefs): string => (prefs?.start_page == "home" ? "/start" : "/home");

// Answers can arrive late or out of order. Changes are sent one after the other; every change
// raises the version when it starts and when it ends. A read is only applied when it still
// belongs to the signed-in account and no change started or ended since it was asked for.
let prefsVersion = 0;
let changeQueue: Promise<unknown> = Promise.resolve();

// queueChange runs one change after the previous one has finished.
const queueChange = <T>(run: () => Promise<T>): Promise<T> => {
  const next = changeQueue.then(run, run);
  changeQueue = next.catch(() => undefined);
  return next;
};
const currentUid = () => SessionManager.currentLoginOrNull()?.user.id;

function applyIfCurrent(prefs: Prefs, uid: string | undefined, version: number): AppThunk<boolean> {
  return (dispatch) => {
    if (uid != currentUid() || version != prefsVersion) {
      return false;
    }
    dispatch(applyPrefs(prefs));
    return true;
  };
}

export function loadPrefs(): AppThunk<Promise<Prefs | undefined>> {
  return async (dispatch) => {
    const uid = currentUid();
    if (!uid) {
      return undefined;
    }
    const version = prefsVersion;
    try {
      const prefs = await dispatch(getPrefs());
      dispatch(applyIfCurrent(prefs, uid, version));
      return prefs;
    } catch (e) {
      return undefined;
    }
  };
}

// A change belongs to the account that was signed in when the person made it. When another account
// is signed in by the time it is sent, it is dropped.
const accountChanged = () => new Error("the signed-in account changed");

function changePrefs(send: () => Promise<Prefs>): AppThunk<Promise<Prefs>> {
  return (dispatch) => {
    const uid = currentUid();
    return queueChange(async () => {
      if (!uid || uid != currentUid()) {
        throw accountChanged();
      }
      const version = ++prefsVersion;
      try {
        const prefs = await send();
        dispatch(applyIfCurrent(prefs, uid, version));
        return prefs;
      } finally {
        // Only while the same account is signed in; a newer account's reads stay valid.
        if (uid == currentUid()) {
          prefsVersion++;
        }
      }
    });
  };
}

export function savePrefs(patch: PrefsPatch): AppThunk<Promise<Prefs>> {
  return (dispatch) => dispatch(changePrefs(() => dispatch(sendPatchPrefs(patch))));
}

export function resetPrefs(): AppThunk<Promise<Prefs>> {
  return (dispatch) => dispatch(changePrefs(() => dispatch(sendResetPrefs())));
}

export function saveAppearanceMode(mode: AppearanceMode): AppThunk<Promise<void>> {
  return async (dispatch) => {
    // Applied at once. Signed in, the account keeps it; a guest (for example on a share link)
    // keeps it in this browser, as before.
    dispatch(setDarkMode(modeToDark(mode)));
    if (!currentUid()) {
      SessionManager.set(UserSettings.PreferredDarkMode, modeToDark(mode));
      return;
    }
    try {
      await dispatch(savePrefs({ appearance: { mode } }));
    } catch (e) {}
  };
}

export function loadTags(): AppThunk<Promise<void>> {
  return async (dispatch) => {
    const uid = currentUid();
    if (!uid) {
      return;
    }
    try {
      const tags = await dispatch(getTags());
      if (uid == currentUid()) {
        dispatch(setTags(tags));
      }
    } catch (e) {}
  };
}

// canBeFavorite tells whether a file in a list can get a star: own files, and files in a shared
// folder (a member-only share; it only opens for its members). Not in "Shared with me" (its
// entries are shortcuts), the trash, searches, public links or connected storages.
export const canBeFavorite = (file: FileResponse, shareInfo?: Record<string, Share>): boolean => {
  try {
    const uri = new CrUri(file.path);
    if (uri.is_search() || !currentUid()) {
      return false;
    }
    if (uri.fs() == Filesystem.my) {
      return !!file.owned && !uri.is_root();
    }
    if (uri.fs() == Filesystem.share && shareInfo) {
      // A member of the shared folder; its owner stars it in their own files instead.
      const prefix = uri.id() + "/";
      return Object.keys(shareInfo).some(
        (key) => key.startsWith(prefix) && !!shareInfo[key].member_only && shareInfo[key].owner?.id != currentUid(),
      );
    }
  } catch (e) {}
  return false;
};

export function setFavorite(files: FileResponse[], on: boolean): AppThunk<Promise<void>> {
  return (dispatch) => {
    const uid = currentUid();
    return queueChange(async () => {
      if (!uid || uid != currentUid()) {
        throw accountChanged();
      }
      prefsVersion++;
      try {
        const res = on
          ? await dispatch(sendAddFavorites(files.map((f) => f.path)))
          : await dispatch(sendRemoveFavorites(files.map((f) => f.id)));
        if (uid == currentUid()) {
          dispatch(setFavoriteIds(res.favorites));
        }
      } finally {
        if (uid == currentUid()) {
          prefsVersion++;
        }
      }
      dispatch(increaseFavoritesGeneration());
    });
  };
}

// clearAccountData forgets what belongs to the account in this tab (on sign-out or switch).
export function clearAccountData(): AppThunk {
  return (dispatch) => {
    prefsVersion++;
    dispatch(setPrefs(readCachedPrefs()));
    dispatch(setTags(undefined));
  };
}
