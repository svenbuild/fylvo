import { ConnectedCode } from "../../../api/connected.ts";
import { AppError } from "../../../api/request.ts";

// Fylvo: rules for walking a connected storage, so the provider does not slow everybody
// down: a request the provider calls too many (40923, HTTP 429 or 503) waits as long as
// Retry-After says, else 2 seconds doubling up to 60; after 5 such answers in a row the
// whole download stops.

const firstWait = 2000;
const longestWait = 60000;
const maxBusyInARow = 5;

// An answer of a content address that says "later" (HTTP 429 or 503).
class ContentBusy extends Error {
  constructor(public retryAfter?: number) {
    super("The storage provider is busy");
  }
}

// Thrown when the provider stayed busy: the download stops and says so.
export class ProviderGaveUp extends Error {
  constructor() {
    super("The storage provider stayed busy");
    this.name = "ProviderGaveUp";
  }
}

// Shared by all requests of one download, so busy answers in a row count across files.
export interface BusyState {
  inARow: number;
  wait: number;
}

export const newBusyState = (): BusyState => ({ inARow: 0, wait: firstWait });

const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    });
  });

// The code of a server error; a single failed address of POST /file/url comes wrapped in a
// batch error (40081) with one entry.
const codeOf = (e: unknown): number | undefined => {
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

// Runs request again while the provider is busy, under the rules above.
export const retryBusy = async <T>(state: BusyState, request: () => Promise<T>, signal?: AbortSignal): Promise<T> => {
  while (true) {
    try {
      const res = await request();
      state.inARow = 0;
      state.wait = firstWait;
      return res;
    } catch (e) {
      const busy = e instanceof ContentBusy || codeOf(e) == ConnectedCode.Busy;
      if (!busy) {
        throw e;
      }
      state.inARow++;
      if (state.inARow >= maxBusyInARow) {
        throw new ProviderGaveUp();
      }
      const wait = e instanceof ContentBusy && e.retryAfter ? e.retryAfter * 1000 : state.wait;
      state.wait = Math.min(state.wait * 2, longestWait);
      await sleep(Math.min(wait, longestWait), signal);
    }
  }
};

// Fetches a content address; an error answer is thrown, never returned as content.
export const fetchContent = async (url: string, signal?: AbortSignal): Promise<Response> => {
  const res = await fetch(url, { signal });
  if (res.status == 429 || res.status == 503) {
    const after = parseInt(res.headers.get("Retry-After") ?? "", 10);
    throw new ContentBusy(isNaN(after) ? undefined : after);
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return res;
};

// Above this many files a download of a connected storage asks first.
export const askAboveFiles = 2000;
