// Fylvo: after an update of the server, a tab of the older version asks for code files (chunks)
// that are gone, for example when an editor is opened for the first time. Only the core of the
// app is kept by the service worker; the rest is loaded when needed. Such a tab takes the new
// version once: it activates the waiting service worker (or finds the new one first), then
// reloads. Once per page load, so a real failure does not reload over and over; a tab that ran
// fine for a while may do it again after a later update.
const reloadedKey = "fylvo_chunk_reload";
let reloading = false;

async function activateNewVersion() {
  reloading = true;
  const registration = await navigator.serviceWorker?.getRegistration();
  if (registration) {
    await registration.update().catch(() => undefined);
    const waiting = registration.waiting ?? (await installed(registration.installing));
    if (waiting) {
      await new Promise<void>((resolve) => {
        navigator.serviceWorker.addEventListener("controllerchange", () => resolve(), { once: true });
        waiting.postMessage({ type: "SKIP_WAITING" });
        setTimeout(resolve, 5000);
      });
    }
  }
  window.location.reload();
}

function installed(worker: ServiceWorker | null): Promise<ServiceWorker | null> {
  if (!worker) {
    return Promise.resolve(null);
  }
  return new Promise((resolve) => {
    const check = () => {
      if (worker.state === "installed") resolve(worker);
      else if (worker.state === "redundant" || worker.state === "activated") resolve(null);
    };
    worker.addEventListener("statechange", check);
    check();
    setTimeout(() => resolve(null), 10000);
  });
}

export function reloadOnMissingChunk() {
  window.addEventListener("load", () => {
    setTimeout(() => {
      if (reloading) {
        return;
      }
      try {
        sessionStorage.removeItem(reloadedKey);
      } catch {
        // Nothing to clear.
      }
    }, 30000);
  });

  window.addEventListener("vite:preloadError", (event) => {
    try {
      if (sessionStorage.getItem(reloadedKey)) {
        return;
      }
      sessionStorage.setItem(reloadedKey, "1");
    } catch {
      return;
    }
    event.preventDefault();
    activateNewVersion();
  });
}
