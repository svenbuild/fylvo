import { AppThunk } from "../store.ts";
import { sendPinFile, sendUnpinFile, sendUpdateUserSetting } from "../../api/api.ts";
import { updateSiteConfig } from "./site.ts";
import { increasePinedGeneration } from "../globalStateSlice.ts";
import i18next from "i18next";
import { inConnectedStorage } from "../../api/connected.ts";
import { getPrefs } from "../../api/fylvo.ts";
import { savePrefs } from "./fylvo.ts";

export function pinToSidebar(uri: string, name?: string): AppThunk<Promise<void>> {
  return async (dispatch, _getState) => {
    // Fylvo: a folder of a connected storage becomes a link entry of the account's sidebar;
    // the old pin list does not take such addresses.
    if (inConnectedStorage(uri)) {
      const items = (await dispatch(getPrefs())).sidebar.items;
      if (!items.some((i) => i.type == "link" && i.uri == uri)) {
        const base = items.filter((i) => i.type == "link" && i.uri).map((i) => i.uri as string);
        await dispatch(
          savePrefs({ sidebar: { items: [...items, { type: "link", uri, name: name || undefined }], base } }),
        );
      }
      return;
    }
    await dispatch(
      sendPinFile({
        uri,
        name,
      }),
    );
    await dispatch(updateSiteConfig());
    dispatch(increasePinedGeneration());
  };
}

export function unPinFromSidebar(uri: string): AppThunk<Promise<void>> {
  return async (dispatch, _getState) => {
    await dispatch(
      sendUnpinFile({
        uri,
      }),
    );
    await dispatch(updateSiteConfig());
    dispatch(increasePinedGeneration());
  };
}

export function selectLanguage(lng: string): AppThunk<Promise<void>> {
  return async (dispatch, _getState) => {
    await i18next.changeLanguage(lng);
    await dispatch(
      sendUpdateUserSetting({
        language: lng,
      }),
    );
  };
}
