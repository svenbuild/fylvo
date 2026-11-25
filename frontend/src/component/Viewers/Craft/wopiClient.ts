// Fylvo: the WOPI calls a Craft editor's page makes itself. They are the calls Collabora makes, so
// the server treats both the same: rights, locks, versions and connected storages. The address is
// on this server (the session answer gives the path), the token goes in the query.

export interface WopiInfo {
  BaseFileName: string;
  Version: string;
  Size: number;
  UserCanWrite: boolean;
  SupportsLocks?: boolean;
  UserFriendlyName?: string;
}

export interface WopiAnswer {
  status: number;
  // The version the file has after a save, when the server says it.
  version?: string;
}

export type LockCall = "LOCK" | "REFRESH_LOCK" | "UNLOCK";

export class WopiClient {
  constructor(
    private readonly src: string,
    private readonly token: string,
  ) {}

  private url(suffix = "") {
    return `${this.src}${suffix}?access_token=${encodeURIComponent(this.token)}`;
  }

  async info(): Promise<WopiInfo> {
    const res = await fetch(this.url(), { cache: "no-store", credentials: "omit" });
    if (!res.ok) {
      throw new WopiError(res.status);
    }
    return res.json();
  }

  async contents(): Promise<ArrayBuffer> {
    const res = await fetch(this.url("/contents"), { cache: "no-store", credentials: "omit" });
    if (!res.ok) {
      throw new WopiError(res.status);
    }
    return res.arrayBuffer();
  }

  // keepalive lets an UNLOCK go out while the page is closing.
  async lock(call: LockCall, lock: string, keepalive = false): Promise<number> {
    const res = await fetch(this.url(), {
      method: "POST",
      credentials: "omit",
      keepalive,
      headers: { "X-WOPI-Override": call, "X-WOPI-Lock": lock },
    });
    return res.status;
  }

  // A save over the version expected (the server refuses it with 409 when the file is at another
  // one by then) under the lock held, if any.
  async put(bytes: ArrayBuffer, lock?: string, expected?: string): Promise<WopiAnswer> {
    const headers: Record<string, string> = { "X-WOPI-Override": "PUT", "Content-Type": "application/octet-stream" };
    if (lock) {
      headers["X-WOPI-Lock"] = lock;
    }
    if (expected) {
      headers["X-Fylvo-Expected-Version"] = expected;
    }
    const res = await fetch(this.url("/contents"), { method: "POST", credentials: "omit", headers, body: bytes });
    return { status: res.status, version: res.headers.get("X-WOPI-ItemVersion") ?? undefined };
  }
}

export class WopiError extends Error {
  constructor(public readonly status: number) {
    super(`WOPI ${status}`);
  }
}

export const newLockToken = () => {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
};

// What happens with a file an editor hands over to save.
export type SaveRoute = "put" | "copy";

// The file the editor was given is saved over (put) only when the editor saves it under that
// name; Save As, an export to another format, or a new name become a copy the user places.
export const saveRoute = (opened: string, saved: string, saveAs: boolean): SaveRoute =>
  !saveAs && saved.toLowerCase() == opened.toLowerCase() ? "put" : "copy";

// A blank document for an empty file (one just made with "New"), as the editors cannot save a
// document they made themselves in the right format.
export const blankTemplate = (ext: string) =>
  ["docx", "xlsx", "pptx"].includes(ext) ? `/static/craft-blank/blank.${ext}` : undefined;
