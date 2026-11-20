// Fylvo activity log, see pkg/activity on the server.

export const ActivityType = {
  file_create: 1,
  file_upload: 2,
  file_update: 3,
  file_rename: 4,
  file_move: 5,
  file_copy: 6,
  file_trash: 7,
  file_restore: 8,
  file_delete: 9,
  version_restore: 10,
  version_delete: 11,
  share_create: 12,
  share_update: 13,
  share_delete: 14,
  guest_download: 15,
  login: 30,
  login_failed: 31,
  login_locked: 32,
  password_change: 33,
  two_fa_enable: 34,
  two_fa_disable: 35,
  app_revoked: 36,
  account_delete: 37,
};

export interface ActivityUser {
  id: string;
  nickname: string;
  email?: string;
}

export interface ActivityContent {
  name?: string;
  old_name?: string;
  is_folder?: boolean;
  from_folder?: string;
  to_folder?: string;
  method?: string;
  target?: string;
}

export interface ActivityEntry {
  id: number;
  type: number;
  created_at: string;
  actor?: ActivityUser;
  owner?: ActivityUser;
  guest?: boolean;
  system?: boolean;
  via?: string;
  ip?: string;
  user_agent?: string;
  file_id?: string;
  content?: ActivityContent;
}

export interface ListActivityResponse {
  entries: ActivityEntry[];
  next?: number;
}

export interface AdminActivityService {
  next?: number;
  account?: string;
  group?: string;
}
