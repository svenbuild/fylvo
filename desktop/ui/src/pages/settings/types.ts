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
}

// Fylvo: "server" = deleted on this PC, would be deleted on the server;
// "local" = deleted on the server, would be deleted on this PC.
export interface HeldDeletion {
  side: "server" | "local";
  count: number;
}

export type DriveStatus = "active" | "event_push_lost" | "credential_expired";

export interface CapacitySummary {
  total: number;
  used: number;
  label: string;
}
