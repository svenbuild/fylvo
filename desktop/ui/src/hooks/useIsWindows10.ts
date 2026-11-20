import { useState } from "react";
import { version } from "@tauri-apps/plugin-os";

/**
 * Hook to detect if the current OS is Windows 10.
 * Windows 10 version strings start with "10.0" but the build number is below 22000.
 * Windows 11 also reports as "10.0" but with build number >= 22000.
 */
export function useIsWindows10(): boolean {
  // The OS version does not change while the app runs, so it is read once.
  const [isWindows10] = useState(() => {
    // Windows version format: "10.0.19045" (Win10) or "10.0.22631" (Win11)
    const parts = version().split(".");
    const buildNumber = parts.length >= 3 ? parseInt(parts[2], 10) : NaN;
    return !isNaN(buildNumber) && buildNumber < 22000;
  });
  return isWindows10;
}
