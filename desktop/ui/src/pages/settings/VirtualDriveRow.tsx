import { Alert, Box, IconButton, Stack, Tooltip, Typography } from "@mui/material";
import { CloseRounded } from "@mui/icons-material";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { invoke } from "@tauri-apps/api/core";
import { message } from "@tauri-apps/plugin-dialog";
import type { DriveInfo } from "./types";
import { SecondaryButton } from "../../common/StyledComponent";

interface VirtualDriveRowProps {
  drive: DriveInfo;
  onChanged: () => Promise<void>;
}

function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
}

// Fylvo (Linux): a virtual drive. Cache in use, folders kept offline, changes not uploaded yet.
export default function VirtualDriveRow({ drive, onChanged }: VirtualDriveRowProps) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const vfs = drive.vfs;
  if (!vfs) return null;

  const run = async (command: string, args: Record<string, unknown>) => {
    setBusy(true);
    try {
      await invoke(command, args);
    } catch (error) {
      await message(String(error), { kind: "error" });
    } finally {
      setBusy(false);
    }
    await onChanged();
  };

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="caption" color="text.secondary" component="p">
        {t("settings.vfs.cache", {
          used: formatBytes(vfs.cache_used),
          limit: formatBytes(vfs.cache_limit),
        })}
        {vfs.pinned_bytes > 0 && ` · ${t("settings.vfs.pinnedSize", { size: formatBytes(vfs.pinned_bytes) })}`}
        {!vfs.online && ` · ${t("settings.vfs.offline")}`}
      </Typography>

      {vfs.changes_waiting > 0 && (
        <Alert severity={vfs.changes_failed > 0 ? "warning" : "info"} sx={{ mt: 1 }}>
          {vfs.changes_failed > 0
            ? t("settings.vfs.changesFailed", { count: vfs.changes_waiting })
            : t("settings.vfs.changesWaiting", { count: vfs.changes_waiting })}
        </Alert>
      )}

      {vfs.pins.length > 0 && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            {t("settings.vfs.pinned")}
          </Typography>
          <Stack spacing={0.5} sx={{ mt: 0.5 }}>
            {vfs.pins.map((pin) => (
              <Box key={pin} sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }}>
                  {pin === "" ? drive.name : pin}
                </Typography>
                <Tooltip title={t("settings.vfs.unpin")}>
                  <span>
                    <IconButton
                      size="small"
                      disabled={busy}
                      onClick={() => run("unpin_drive_path", { driveId: drive.id, path: pin })}
                    >
                      <CloseRounded fontSize="small" />
                    </IconButton>
                  </span>
                </Tooltip>
              </Box>
            ))}
          </Stack>
        </Box>
      )}

      <Typography variant="caption" color="text.secondary" component="p" sx={{ mt: 1 }}>
        {t("settings.vfs.pinHint")}
      </Typography>

      <SecondaryButton
        size="small"
        sx={{ mt: 1 }}
        disabled={busy || vfs.cache_used === 0}
        onClick={() => run("free_drive_space", { driveId: drive.id })}
      >
        {t("settings.vfs.freeSpace")}
      </SecondaryButton>
    </Box>
  );
}
