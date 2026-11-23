import { useEffect, useState } from "react";
import { ConnectedStorage, getConnectedStorages } from "../../../api/connected.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
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

// enabled=false only listens and never asks the server (pages without connected storages).
export const useConnectedStorages = (enabled = true) => {
  const dispatch = useAppDispatch();
  const [value, setValue] = useState(storages);

  useEffect(() => {
    // A storage was reported as needing a reconnect: load the list again.
    const onChanged = () => {
      if (!enabled || loading) {
        return;
      }
      const started = generation;
      const request = dispatch(getConnectedStorages())
        .then((res) => started == generation && publishConnectedStorages(res.storages))
        .catch(() => {})
        .finally(() => {
          if (loading == request) {
            loading = undefined;
          }
        });
      loading = request;
    };
    window.addEventListener(ConnectedChangedEvent, onChanged);
    listeners.add(setValue);
    if (enabled && !storages && !loading) {
      const started = generation;
      const request = dispatch(getConnectedStorages())
        .then((res) => started == generation && publishConnectedStorages(res.storages))
        .catch(() => {})
        .finally(() => {
          if (loading == request) {
            loading = undefined;
          }
        });
      loading = request;
    }
    return () => {
      window.removeEventListener(ConnectedChangedEvent, onChanged);
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
