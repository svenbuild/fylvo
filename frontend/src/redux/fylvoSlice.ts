import { createSlice, PayloadAction } from "@reduxjs/toolkit";
import { Prefs, TagInfo } from "../api/fylvo.ts";
import SessionManager from "../session";

// Fylvo: account preferences as the server reports them, and the tag list for the sidebar.
// A copy of the preferences stays in the browser only so the sidebar does not flicker on load.

const cacheKey = "fylvo_prefs_cache";

const currentUid = () => SessionManager.currentLoginOrNull()?.user.id;

export const readCachedPrefs = (): Prefs | undefined => {
  try {
    const raw = localStorage.getItem(cacheKey);
    const cached = raw ? (JSON.parse(raw) as { uid: string; prefs: Prefs }) : undefined;
    return cached && cached.uid == currentUid() ? cached.prefs : undefined;
  } catch (e) {
    return undefined;
  }
};

export const writeCachedPrefs = (prefs?: Prefs) => {
  try {
    if (prefs && currentUid()) {
      localStorage.setItem(cacheKey, JSON.stringify({ uid: currentUid(), prefs }));
    } else {
      localStorage.removeItem(cacheKey);
    }
  } catch (e) {}
};

export interface FylvoState {
  prefs?: Prefs;
  tags?: TagInfo[];
  // Increases when favorites change, so open lists reload.
  favoritesGeneration: number;
}

const initialState: FylvoState = {
  prefs: readCachedPrefs(),
  favoritesGeneration: 0,
};

export const fylvoSlice = createSlice({
  name: "fylvo",
  initialState,
  reducers: {
    setPrefs: (state, action: PayloadAction<Prefs | undefined>) => {
      state.prefs = action.payload;
    },
    setTags: (state, action: PayloadAction<TagInfo[] | undefined>) => {
      state.tags = action.payload;
    },
    increaseFavoritesGeneration: (state) => {
      state.favoritesGeneration += 1;
    },
  },
});

export default fylvoSlice.reducer;
export const { setPrefs, setTags, increaseFavoritesGeneration } = fylvoSlice.actions;
