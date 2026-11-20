export interface DriveInfo {
  id: string;
  name: string;
  instance_url: string;
  sync_path: string;
  icon_path?: string;
  raw_icon_path?: string;
  enabled: boolean;
  user_id: string;
  remote_path: string
  status: DriveStatus;
  capacity?: CapacitySummary;
  // Fylvo: shared folder the user may only read.
  read_only: boolean;
  // Fylvo: mass deletions waiting for the user's answer.
  held_deletions: HeldDeletion[];
  // Fylvo: the drive letter the folder also shows as (null = off), and whether it works.
  drive_letter: string | null;
  drive_letter_state: "off" | "active" | "conflict";
}

// Fylvo: "server" = deleted on this PC, would be deleted on the server;
// "local" = deleted on the server, would be deleted on this PC.
export interface HeldDeletion {
  side: "server" | "local";
  count: number;
  // Fylvo: sent back on confirm, so the answer matches what was shown.
  version: number;
}

export type DriveStatus = "active" | "event_push_lost" | "credential_expired";

export interface CapacitySummary {
  total: number;
  used: number;
  label: string;
}
