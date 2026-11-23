import { useEffect, useState } from "react";
import { ConnectedStorage, getConnectedStorages } from "../../../api/connected.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import type { AppDispatch } from "../../../redux/store.ts";
import { ConnectedChangedEvent } from "../../../api/request.ts";

// Fylvo: the connected storages of the account, shared by the sidebar and the breadcrumb.
// Loaded once per page; the settings tab publishes every list it loads, so renames and new
// connections show at once.

let storages: ConnectedStorage[] | undefined;
let loading: Promise<unknown> | undefined;
// Raised when the account changes, so an answer for the previous account is dropped.
let generation = 0;
const listeners = new Set<(s: ConnectedStorage[] | undefined) => void>();

export const publishConnectedStorages = (next: ConnectedStorage[] | undefined) => {
  if (!next) {
    generation++;
    loading = undefined;
  }
  storages = next;
  listeners.forEach((l) => l(next));
};

// Set when the list became out of date while a load was running; that load is followed by
// another one, so an answer from before the change is not the last word.
let stale = false;
// Set once a part of the page asked for the list, so a later 40921 reloads it even after a
// failed first load.
let requested = false;

// Loads the list unless a load is running; an answer for a previous account is dropped.
const load = (dispatch: AppDispatch) => {
  if (loading) {
    stale = true;
    return;
  }
  stale = false;
  requested = true;
  const started = generation;
  const request = dispatch(getConnectedStorages())
    .then((res) => started == generation && publishConnectedStorages(res.storages))
    .catch(() => {})
    .finally(() => {
      if (loading == request) {
        loading = undefined;
        if (stale && started == generation) {
          load(dispatch);
        }
      }
    });
  loading = request;
};

// A storage was reported as needing a reconnect (any request answered 40921): load the list
// again, once for the page, also when no part that shows it is open right now.
let listening = false;
const listenForChanges = (dispatch: AppDispatch) => {
  if (listening) {
    return;
  }
  listening = true;
  window.addEventListener(ConnectedChangedEvent, () => {
    if (storages || loading || requested) {
      load(dispatch);
    }
  });
};

// enabled=false only listens and never asks the server (pages without connected storages).
export const useConnectedStorages = (enabled = true) => {
  const dispatch = useAppDispatch();
  const [value, setValue] = useState(storages);

  useEffect(() => {
    listeners.add(setValue);
    if (enabled) {
      listenForChanges(dispatch);
      if (!storages) {
        load(dispatch);
      }
    }
    return () => {
      listeners.delete(setValue);
    };
  }, [enabled]);

  return value;
};

// The storage with the given id, undefined while loading or when it is unknown.
export const useConnectedStorage = (id?: string) => {
  const list = useConnectedStorages(!!id);
  return id ? list?.find((s) => s.id == id) : undefined;
};
