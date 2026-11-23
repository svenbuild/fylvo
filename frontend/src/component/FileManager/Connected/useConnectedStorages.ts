import { useEffect, useState } from "react";
import { ConnectedStorage, getConnectedStorages } from "../../../api/connected.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";

// Fylvo: the connected storages of the account, shared by the sidebar and the breadcrumb.
// Loaded once per page; the settings tab publishes every list it loads, so renames and new
// connections show at once.

let storages: ConnectedStorage[] | undefined;
let loading: Promise<unknown> | undefined;
const listeners = new Set<(s: ConnectedStorage[] | undefined) => void>();

export const publishConnectedStorages = (next: ConnectedStorage[] | undefined) => {
  storages = next;
  listeners.forEach((l) => l(next));
};

// enabled=false only listens and never asks the server (pages without connected storages).
export const useConnectedStorages = (enabled = true) => {
  const dispatch = useAppDispatch();
  const [value, setValue] = useState(storages);

  useEffect(() => {
    listeners.add(setValue);
    if (enabled && !storages && !loading) {
      loading = dispatch(getConnectedStorages())
        .then((res) => publishConnectedStorages(res.storages))
        .catch(() => {})
        .finally(() => (loading = undefined));
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
