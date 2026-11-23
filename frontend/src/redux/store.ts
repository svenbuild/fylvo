import { Action, configureStore } from "@reduxjs/toolkit";
import { ThunkAction } from "redux-thunk";
import { updateSiteConfig } from "./thunks/site.ts";
import { accountDarkMode, applyDensity } from "./thunks/fylvo.ts";
import { setDarkMode } from "./globalStateSlice.ts";
import siteConfigSliceReducer from "./siteConfigSlice";
import globalStateSliceReducer from "./globalStateSlice";
import fileManagerSliceReducer from "./fileManagerSlice.ts";
import fylvoSliceReducer from "./fylvoSlice.ts";

export const store = configureStore({
  reducer: {
    siteConfig: siteConfigSliceReducer,
    globalState: globalStateSliceReducer,
    fileManager: fileManagerSliceReducer,
    fylvo: fylvoSliceReducer,
  },
  devTools: process.env.NODE_ENV !== "production",
});

store.dispatch(updateSiteConfig());
// Fylvo: the density of the last session, until the account's preferences arrive.
applyDensity(store.getState().fylvo.prefs?.view.density);
if (store.getState().fylvo.prefs) {
  store.dispatch(setDarkMode(accountDarkMode(store.getState().fylvo.prefs)));
}

export type AppDispatch = typeof store.dispatch;
export type RootState = ReturnType<typeof store.getState>;
export type AppThunk<ReturnType = void> = ThunkAction<ReturnType, RootState, unknown, Action<string>>;
