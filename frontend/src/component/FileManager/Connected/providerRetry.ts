import { ConnectedCode } from "../../../api/connected.ts";
import { AppError } from "../../../api/request.ts";

// Fylvo: rules for walking a connected storage, so the provider does not slow everybody
// down: a request the provider calls too many (40923, HTTP 429 or 503) waits as long as
// Retry-After says, else 2 seconds doubling up to 60; after 5 such answers in a row the
// whole download stops. A storage that needs a reconnect (40921) stops it at once.

export const firstWait = 2000;
export const longestWait = 60000;
export const maxBusyInARow = 5;

// An answer of a content address that says "later" (HTTP 429 or 503).
class ContentBusy extends Error {
  constructor(public retryAfter?: number) {
    super("The storage provider is busy");
  }
}

// Thrown when a download of a connected storage has to stop; message is the text key.
export class ProviderGaveUp extends Error {
  constructor(message: string = "application:connected.providerGaveUp") {
    super(message);
    this.name = "ProviderGaveUp";
  }
}

// Shared by all requests of one download, so busy answers in a row count across files,
// and so a cancel stops waiting and retrying.
export interface BusyState {
  inARow: number;
  wait: number;
  signal?: AbortSignal;
}

export const newBusyState = (signal?: AbortSignal): BusyState => ({ inARow: 0, wait: firstWait, signal });

const aborted = () => new DOMException("Aborted", "AbortError");

const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(aborted());
      return;
    }
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(aborted());
    });
  });

// Seconds or an HTTP date, as Retry-After may carry; undefined when there is none.
const retryAfterMs = (value?: string | null): number | undefined => {
  if (!value) {
    return undefined;
  }
  const seconds = Number(value);
  if (!isNaN(seconds)) {
    return Math.max(0, seconds * 1000);
  }
  const date = Date.parse(value);
  return isNaN(date) ? undefined : Math.max(0, date - Date.now());
};

// The code of a server error; a single failed address of POST /file/url comes wrapped in a
// batch error (40081) with one entry.
export const codeOf = (e: unknown): number | undefined => {
  if (!(e instanceof AppError)) {
    return undefined;
  }
  const entries = Object.values(e.aggregatedError ?? {});
  return entries.length == 1 ? entries[0].code : e.code;
};

// A readable text for an error of one file, also when it came wrapped in a batch error.
export const errorMessage = (e: unknown): string => {
  if (e instanceof AppError) {
    const entries = Object.values(e.aggregatedError ?? {});
    return entries.length == 1 ? new AppError(entries[0]).message : e.message;
  }
  return e instanceof Error ? e.message : String(e);
};

// How long to wait when e says "later", undefined when it says something else. HTTP 429
// and 503 come as a content answer (ContentBusy) or as a failed API request.
const busyWait = (e: unknown): { ms?: number } | undefined => {
  if (e instanceof ContentBusy) {
    return { ms: e.retryAfter };
  }
  if (codeOf(e) == ConnectedCode.Busy) {
    return {};
  }
  const response = (e as { response?: { status?: number; headers?: Record<string, string> } })?.response;
  if (response?.status == 429 || response?.status == 503) {
    return { ms: retryAfterMs(response.headers?.["retry-after"]) };
  }
  return undefined;
};

// Runs request again while the provider is busy, under the rules above.
export const retryBusy = async <T>(state: BusyState, request: () => Promise<T>): Promise<T> => {
  while (true) {
    if (state.signal?.aborted) {
      throw aborted();
    }
    try {
      const res = await request();
      state.inARow = 0;
      state.wait = firstWait;
      return res;
    } catch (e) {
      if (codeOf(e) == ConnectedCode.Reconnect) {
        throw new ProviderGaveUp("common:errors.40921");
      }
      const busy = busyWait(e);
      if (!busy) {
        // Another answer: busy answers before it are no longer "in a row".
        state.inARow = 0;
        state.wait = firstWait;
        throw e;
      }
      state.inARow++;
      if (state.inARow >= maxBusyInARow) {
        throw new ProviderGaveUp();
      }
      // The provider's own wait is kept as it is; only the doubling has an upper bound.
      const wait = busy.ms ?? state.wait;
      state.wait = Math.min(state.wait * 2, longestWait);
      await sleep(wait, state.signal);
    }
  }
};

// Fetches a content address; an error answer is thrown, never returned as content.
export const fetchContent = async (url: string, signal?: AbortSignal): Promise<Response> => {
  const res = await fetch(url, { signal });
  if (res.status == 429 || res.status == 503) {
    throw new ContentBusy(retryAfterMs(res.headers.get("Retry-After")));
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return res;
};

// Above this many files a download of a connected storage asks first.
export const askAboveFiles = 2000;
