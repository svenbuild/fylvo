// Fylvo: account preferences, favorites and tags (design/customize-plan.md). The server knows the
// defaults and always answers with the effective values.
import { FileResponse } from "./explorer.ts";
import { defaultOpts, send, ThunkResponse } from "./request.ts";

export type SidebarItemType =
  | "home"
  | "my_files"
  | "favorites"
  | "tags"
  | "shared_with_me"
  | "trash"
  | "category"
  | "link"
  | "heading"
  | "connected";

export interface SidebarItem {
  type: SidebarItemType;
  category?: string;
  uri?: string;
  name?: string;
  id?: string;
  hidden?: boolean;
}

export type HomeWidgetType = "recent" | "favorites" | "links" | "tags" | "storage";

export interface HomeWidget {
  type: HomeWidgetType;
  hidden?: boolean;
}

export type Density = "compact" | "normal" | "comfortable";
export type AppearanceMode = "system" | "light" | "dark";
export type StartPage = "" | "home" | "my_files";

export interface Prefs {
  sidebar: { items: SidebarItem[] };
  home: { widgets: HomeWidget[] };
  start_page: StartPage;
  view: { density: Density; layout?: string; sort_by?: string; sort_direction?: string };
  appearance: { mode: AppearanceMode; accent: string };
  mobile: Record<string, any>;
  favorites: string[];
  customized: string[];
}

export interface PrefsPatch {
  // null drops the saved part, back to the default.
  sidebar?: { items: SidebarItem[] } | null;
  home?: { widgets: HomeWidget[] } | null;
  start_page?: StartPage;
  view?: { density: Density };
  appearance?: { mode?: AppearanceMode; accent?: string };
}

export interface FavoritesResponse {
  files: FileResponse[];
  unavailable: number;
}

export interface TagInfo {
  name: string;
  color: string;
  count: number;
}

// A function, not a constant: request.ts is not initialized yet when this module loads.
const quiet = () => ({ ...defaultOpts, bypassSnackbar: () => true });

export function getPrefs(): ThunkResponse<Prefs> {
  return async (dispatch) => dispatch(send("/fylvo/prefs", { method: "GET" }, quiet()));
}

export function sendPatchPrefs(patch: PrefsPatch): ThunkResponse<Prefs> {
  return async (dispatch) => dispatch(send("/fylvo/prefs", { method: "PATCH", data: patch }, defaultOpts));
}

export function sendResetPrefs(): ThunkResponse<Prefs> {
  return async (dispatch) => dispatch(send("/fylvo/prefs", { method: "DELETE" }, defaultOpts));
}

export function getFavorites(): ThunkResponse<FavoritesResponse> {
  return async (dispatch) => dispatch(send("/fylvo/favorites", { method: "GET" }, defaultOpts));
}

export interface FavoriteIds {
  favorites: string[];
}

export function sendAddFavorites(uris: string[]): ThunkResponse<FavoriteIds> {
  return async (dispatch) => dispatch(send("/fylvo/favorites", { method: "PUT", data: { uris } }, defaultOpts));
}

export function sendRemoveFavorites(ids: string[]): ThunkResponse<FavoriteIds> {
  return async (dispatch) => dispatch(send("/fylvo/favorites", { method: "DELETE", data: { ids } }, defaultOpts));
}

export function getTags(): ThunkResponse<TagInfo[]> {
  return async (dispatch) => dispatch(send("/fylvo/tags", { method: "GET" }, quiet()));
}
