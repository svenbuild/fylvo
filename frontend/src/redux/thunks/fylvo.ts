import { FileResponse } from "../../api/explorer.ts";
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
import { increaseFavoritesGeneration, setPrefs, setTags, writeCachedPrefs } from "../fylvoSlice.ts";
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

// applyPrefs stores the preferences and applies what the browser has to know: light or dark
// (only when the account saved it, otherwise the browser's own choice stays) and the accent.
export function applyPrefs(prefs: Prefs): AppThunk {
  return (dispatch) => {
    dispatch(setPrefs(prefs));
    writeCachedPrefs(prefs);
    applyDensity(prefs.view.density);
    if (prefs.customized.includes("mode")) {
      const dark = modeToDark(prefs.appearance.mode);
      dispatch(setDarkMode(dark));
      SessionManager.set(UserSettings.PreferredDarkMode, dark);
    }
    dispatch(setPreferredTheme(prefs.appearance.accent));
  };
}

// startPath is where the app opens: the start page when the account chose it, else the files.
export const startPath = (prefs?: Prefs): string => (prefs?.start_page == "home" ? "/start" : "/home");

export function loadPrefs(): AppThunk<Promise<Prefs | undefined>> {
  return async (dispatch) => {
    if (!SessionManager.currentLoginOrNull()) {
      return undefined;
    }
    try {
      const prefs = await dispatch(getPrefs());
      dispatch(applyPrefs(prefs));
      return prefs;
    } catch (e) {
      return undefined;
    }
  };
}

export function savePrefs(patch: PrefsPatch): AppThunk<Promise<Prefs>> {
  return async (dispatch) => {
    const prefs = await dispatch(sendPatchPrefs(patch));
    dispatch(applyPrefs(prefs));
    return prefs;
  };
}

export function resetPrefs(): AppThunk<Promise<Prefs>> {
  return async (dispatch) => {
    const prefs = await dispatch(sendResetPrefs());
    dispatch(applyPrefs(prefs));
    return prefs;
  };
}

export function saveAppearanceMode(mode: AppearanceMode): AppThunk<Promise<void>> {
  return async (dispatch) => {
    // Applied at once; the account follows.
    dispatch(setDarkMode(modeToDark(mode)));
    SessionManager.set(UserSettings.PreferredDarkMode, modeToDark(mode));
    try {
      await dispatch(savePrefs({ appearance: { mode } }));
    } catch (e) {}
  };
}

export function loadTags(): AppThunk<Promise<void>> {
  return async (dispatch) => {
    try {
      dispatch(setTags(await dispatch(getTags())));
    } catch (e) {}
  };
}

// canBeFavorite tells whether a file in a list can get a star: own files and files inside a shared
// folder. Not in "Shared with me" (its entries are shortcuts), the trash, searches or connected
// storages. A public link the person is no member of is refused by the server.
export const canBeFavorite = (file: FileResponse): boolean => {
  try {
    const uri = new CrUri(file.path);
    if (uri.is_search()) {
      return false;
    }
    if (uri.fs() == Filesystem.my) {
      return !!file.owned && !uri.is_root();
    }
    if (uri.fs() == Filesystem.share) {
      return !!SessionManager.currentLoginOrNull();
    }
  } catch (e) {}
  return false;
};

export function setFavorite(files: FileResponse[], on: boolean): AppThunk<Promise<void>> {
  return async (dispatch) => {
    const prefs = on
      ? await dispatch(sendAddFavorites(files.map((f) => f.path)))
      : await dispatch(sendRemoveFavorites(files.map((f) => f.id)));
    dispatch(applyPrefs(prefs));
    dispatch(increaseFavoritesGeneration());
  };
}
