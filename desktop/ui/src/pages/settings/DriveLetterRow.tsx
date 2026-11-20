import { Box, MenuItem, Select, Switch, Typography } from "@mui/material";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { invoke } from "@tauri-apps/api/core";
import { message } from "@tauri-apps/plugin-dialog";
import type { DriveInfo } from "./types";

interface DriveLetterRowProps {
  drive: DriveInfo;
  onChanged: () => Promise<void>;
}

// Fylvo: show the drive's folder additionally as its own drive letter in the Explorer.
export default function DriveLetterRow({ drive, onChanged }: DriveLetterRowProps) {
  const { t } = useTranslation();
  const [freeLetters, setFreeLetters] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const letter = drive.drive_letter;
  const enabled = letter !== null;

  const loadFreeLetters = async () => {
    try {
      setFreeLetters(await invoke<string[]>("get_free_drive_letters"));
    } catch (error) {
      console.error("Failed to list free drive letters:", error);
    }
  };

  const apply = async (on: boolean, newLetter: string | null) => {
    setBusy(true);
    try {
      await invoke("set_drive_letter", { driveId: drive.id, enabled: on, letter: newLetter });
    } catch (error) {
      await message(String(error), { kind: "error" });
    } finally {
      setBusy(false);
    }
    await onChanged();
  };

  const description = !enabled
    ? t("settings.driveLetter.off")
    : drive.drive_letter_state === "conflict"
      ? t("settings.driveLetter.conflict", { letter })
      : t("settings.driveLetter.active", { letter });

  // The current letter stays selectable even though it is in use (by this drive).
  const options = Array.from(new Set([...(letter ? [letter] : []), ...freeLetters])).sort();

  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1, mt: 1.5 }}>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Typography variant="body2">{t("settings.driveLetter.title")}</Typography>
        <Typography
          variant="caption"
          color={drive.drive_letter_state === "conflict" ? "warning.main" : "text.secondary"}
        >
          {description}
        </Typography>
      </Box>
      {enabled && (
        <Select
          size="small"
          value={letter}
          disabled={busy}
          onOpen={loadFreeLetters}
          onChange={(e) => apply(true, e.target.value)}
          renderValue={(value) => `${value}:`}
          sx={{ minWidth: 72 }}
        >
          {options.map((option) => (
            <MenuItem key={option} value={option}>
              {option}:
            </MenuItem>
          ))}
        </Select>
      )}
      <Switch
        checked={enabled}
        disabled={busy}
        onChange={(e) => apply(e.target.checked, null)}
        slotProps={{ input: { "aria-label": t("settings.driveLetter.title") } }}
      />
    </Box>
  );
}
