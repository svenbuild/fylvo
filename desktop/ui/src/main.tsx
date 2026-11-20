import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

async function start() {
  // Dev only: example data when the page runs in a normal browser instead of the app.
  if (import.meta.env.DEV && !("__TAURI_INTERNALS__" in window)) {
    await import("./dev/tauriStandIn");
  }
  const { default: App } = await import("./App.tsx");
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>
  );
}

start().catch((error) => console.error("Failed to start:", error));
