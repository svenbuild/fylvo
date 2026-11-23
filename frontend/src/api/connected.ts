// Fylvo connected storages: Google Drive or OneDrive shown live next to the own files.
// See service/fylvo/fylvo_connected.go on the server.
import { defaultOpts, send, ThunkResponse } from "./request.ts";

// Filesystem name in the address of a connected storage: cloudreve://<id>@connected/<path>
export const ConnectedFileSystem = "connected";

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

// Most storages one account can connect, as on the server.
export const ConnectedMaxPerUser = 10;
export const ConnectedMaxNameLength = 64;

export const ConnectedCode = {
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

const quietOpts = {
  ...defaultOpts,
  bypassSnackbar: () => true,
};

export function getConnectedStorages(): ThunkResponse<ConnectedListResponse> {
  return async (dispatch) => {
    return await dispatch(send("/fylvo/connected", { method: "GET" }, defaultOpts));
  };
}

// Starts a sign-in at the provider; the browser then goes to the returned url.
export function sendConnectedAuthorize(provider: string, reconnect?: string): ThunkResponse<{ url: string }> {
  return async (dispatch) => {
    return await dispatch(
      send("/fylvo/connected/authorize", { method: "POST", data: { provider, reconnect } }, defaultOpts),
    );
  };
}

// Completes a sign-in. Errors are left to the caller, which shows its own text.
export function sendConnectedFinish(code: string): ThunkResponse<ConnectedStorage> {
  return async (dispatch) => {
    return await dispatch(send("/fylvo/connected/finish", { method: "POST", data: { code } }, quietOpts));
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

export function getConnectedAdmin(): ThunkResponse<ConnectedAdminResponse> {
  return async (dispatch) => {
    return await dispatch(send("/admin/fylvo/connected", { method: "GET" }, defaultOpts));
  };
}

export function sendConnectedAdmin(req: ConnectedAdminRequest): ThunkResponse<ConnectedAdminResponse> {
  return async (dispatch) => {
    return await dispatch(send("/admin/fylvo/connected", { method: "PUT", data: req }, defaultOpts));
  };
}
