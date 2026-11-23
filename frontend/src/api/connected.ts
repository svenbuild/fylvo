// Fylvo connected storages: Google Drive or OneDrive shown live next to the own files.
// See service/fylvo/fylvo_connected.go on the server.
import { CrUriPrefix } from "../util/uri.ts";
import Boolset from "../util/boolset.ts";
import { FileResponse, Metadata, NavigatorCapability } from "./explorer.ts";
import { defaultOpts, send, ThunkResponse } from "./request.ts";

// Filesystem name in the address of a connected storage: <scheme>://<id>@connected/<path>
export const ConnectedFileSystem = "connected";

// Whether an address lies in a connected storage.
// The pattern is built on first use: this module and util/uri.ts import each other.
let connectedPathPattern: RegExp | undefined;
export const inConnectedStorage = (path?: string) => {
  connectedPathPattern ??= new RegExp(`^${CrUriPrefix}[^/@?#]+@${ConnectedFileSystem}(?:[/?#]|$)`);
  return !!path && connectedPathPattern.test(path);
};

// Name under which a file is saved: a document of the provider (for example a Google Sheet)
// downloads in the format named in its metadata, so it gets that extension. taken holds the
// names already used in the same place; a name in it is not given twice (Budget.gsheet next
// to Budget.xlsx becomes Budget.gsheet.xlsx). The chosen name is added to taken.
export const downloadFileName = (file: FileResponse, name: string = file.name, taken?: Set<string>) => {
  const ext = file.metadata?.[Metadata.connected_export];
  if (!ext) {
    return name;
  }
  const dot = name.lastIndexOf(".");
  let res = (dot > name.lastIndexOf("/") ? name.slice(0, dot) : name) + "." + ext;
  if (taken?.has(res)) {
    res = name + "." + ext;
  }
  taken?.add(res);
  return res;
};

// Whether a folder can be entered: rows of a connected storage say so in their capability
// (a duplicate name or a shortcut into another drive cannot); other folders always can.
export const folderEnterable = (folder: FileResponse) =>
  !inConnectedStorage(folder.path) || new Boolset(folder.capability).enabled(NavigatorCapability.enter_folder);

// Address of the root of a connected storage, as the server builds it.
export const connectedRootUri = (id: string) => `${CrUriPrefix}${id}@${ConnectedFileSystem}`;

export const ConnectedProvider = {
  drive: "drive",
  onedrive: "onedrive",
};

// Names shown where the server does not send one (admin page).
export const ConnectedProviderNames: Record<string, string> = {
  [ConnectedProvider.drive]: "Google Drive",
  [ConnectedProvider.onedrive]: "OneDrive",
};

export const ConnectedStatus = {
  ok: "ok",
  reconnect: "reconnect",
};

// Error values the server puts into the address when the browser comes back from the
// provider (/settings?tab=connected&error=...).
export const ConnectedFlowErrors = ["expired", "browser", "denied", "setup", "account", "provider"];

// Most storages one account can connect, used when the server does not send "max".
export const ConnectedMaxPerUser = 10;
// Longest name of a connection in UTF-8 bytes, as the server counts.
export const ConnectedMaxNameBytes = 64;

export const ConnectedCode = {
  // Existing code: the account at the provider is already connected.
  ObjectExists: 40004,
  Unsupported: 40920,
  Reconnect: 40921,
  Unavailable: 40922,
  Busy: 40923,
  Full: 40924,
  Item: 40925,
  ListingGone: 40926,
  OutcomeUnknown: 40927,
};

export interface ConnectedProviderInfo {
  type: string;
  name: string;
  configured: boolean;
}

export interface ConnectedStorage {
  id: string;
  provider: string;
  name: string;
  account: string;
  status: string;
  message?: string;
  uri: string;
  created_at: string;
}

export interface ConnectedListResponse {
  available: boolean;
  allowed: boolean;
  providers: ConnectedProviderInfo[];
  storages: ConnectedStorage[];
  max?: number;
}

export interface ConnectedProviderSetup {
  client_id: string;
  secret_set: boolean;
}

export interface ConnectedAdminResponse {
  callback_url: string;
  providers: Record<string, ConnectedProviderSetup>;
}

export interface ConnectedAdminRequest {
  provider: string;
  client_id: string;
  // Left out to keep the stored secret.
  client_secret?: string;
}

// Built on use: this module is part of an import cycle with ./request.ts.
const quietOpts = () => ({
  ...defaultOpts,
  bypassSnackbar: () => true,
});

// Errors are left to the caller.
export function getConnectedStorages(): ThunkResponse<ConnectedListResponse> {
  return async (dispatch) => {
    return await dispatch(send("/fylvo/connected", { method: "GET" }, quietOpts()));
  };
}

// Starts a sign-in at the provider; the browser then goes to the returned url. Errors are
// left to the caller, which shows its own text.
export function sendConnectedAuthorize(provider: string, reconnect?: string): ThunkResponse<{ url: string }> {
  return async (dispatch) => {
    return await dispatch(
      send("/fylvo/connected/authorize", { method: "POST", data: { provider, reconnect } }, quietOpts()),
    );
  };
}

// Completes a sign-in. Errors are left to the caller, which shows its own text.
export function sendConnectedFinish(code: string): ThunkResponse<ConnectedStorage> {
  return async (dispatch) => {
    return await dispatch(send("/fylvo/connected/finish", { method: "POST", data: { code } }, quietOpts()));
  };
}

export function sendConnectedRename(id: string, name: string): ThunkResponse<ConnectedStorage> {
  return async (dispatch) => {
    return await dispatch(
      send(`/fylvo/connected/${encodeURIComponent(id)}`, { method: "PATCH", data: { name } }, defaultOpts),
    );
  };
}

export function sendConnectedDisconnect(id: string): ThunkResponse {
  return async (dispatch) => {
    return await dispatch(send(`/fylvo/connected/${encodeURIComponent(id)}`, { method: "DELETE" }, defaultOpts));
  };
}

// Errors are left to the caller.
export function getConnectedAdmin(): ThunkResponse<ConnectedAdminResponse> {
  return async (dispatch) => {
    return await dispatch(send("/admin/fylvo/connected", { method: "GET" }, quietOpts()));
  };
}

export function sendConnectedAdmin(req: ConnectedAdminRequest): ThunkResponse<ConnectedAdminResponse> {
  return async (dispatch) => {
    return await dispatch(send("/admin/fylvo/connected", { method: "PUT", data: req }, defaultOpts));
  };
}
