// Dev only: stand-in for the Tauri bridge, so the windows can be looked at in a normal
// browser (`yarn dev`, then e.g. http://127.0.0.1:5215/#/settings) with example data.
// main.tsx loads it only in dev mode and only outside the app shell; it is not part of the
// production build.

const now = Math.floor(Date.now() / 1000);

const drives = [
  {
    id: "drive-1",
    name: "Fylvo",
    instance_url: "https://cloud.example.net",
    sync_path: "C:\\Users\\Alex\\Fylvo",
    enabled: true,
    user_id: "u1",
    remote_path: "cloudreve://my",
    status: "active",
    capacity: { total: 500 * 1024 ** 3, used: 182 * 1024 ** 3, label: "182 GB / 500 GB" },
    read_only: false,
    held_deletions: [{ side: "server", count: 214, version: 7 }],
  },
  {
    id: "drive-2",
    name: "Fotos",
    instance_url: "https://cloud.example.net",
    sync_path: "C:\\Users\\Alex\\Fotos",
    enabled: true,
    user_id: "u1",
    remote_path: "cloudreve://shared/fotos",
    status: "active",
    read_only: true,
    held_deletions: [],
  },
];

const task = (id: string, path: string, status: string, progress: number, total: number, ago: number, error?: string) => ({
  id,
  drive_id: "drive-1",
  task_type: id.startsWith("u") ? "upload" : "download",
  local_path: path,
  status,
  progress,
  total_bytes: total,
  processed_bytes: Math.round(total * progress),
  error,
  created_at: now - ago,
  updated_at: now - ago,
});

const summary = {
  drives: drives.map(({ id, name, instance_url, sync_path }) => ({ id, name, instance_url, sync_path })),
  active_tasks: [
    {
      ...task("u1", "C:\\Users\\Alex\\Fylvo\\Projekte\\Offerte Küche.pdf", "Running", 0.42, 18_400_000, 5),
      live_progress: {
        task_id: "u1",
        kind: "Upload",
        local_path: "C:\\Users\\Alex\\Fylvo\\Projekte\\Offerte Küche.pdf",
        progress: 0.42,
        processed_bytes: 7_728_000,
        total_bytes: 18_400_000,
        speed_bytes_per_sec: 2_300_000,
        eta_seconds: 5,
      },
    },
    task("d1", "C:\\Users\\Alex\\Fylvo\\Ferien\\IMG_2041.jpg", "Pending", 0, 4_100_000, 2),
  ],
  finished_tasks: [
    task("d2", "C:\\Users\\Alex\\Fylvo\\Steuern 2025.xlsx", "Completed", 1, 230_000, 120),
    task("u2", "C:\\Users\\Alex\\Fylvo\\Notizen.txt", "Completed", 1, 2_000, 600),
    task("u3", "C:\\Users\\Alex\\Fotos\\Neu.txt", "Failed", 0, 1_000, 900, "Read-only drive"),
  ],
};

const generalSettings = {
  notify_credential_expired: true,
  notify_file_conflict: true,
  fast_popup_launch: false,
  log_to_file: true,
  log_level: "info",
  log_max_files: 5,
  log_dir: "C:\\Users\\Alex\\.fylvo\\logs",
  language: null,
};

const answers: Record<string, unknown> = {
  get_drives_info: drives,
  get_status_summary: summary,
  get_general_settings: generalSettings,
  get_auto_start_enabled: true,
  get_ignore_patterns: ["*.tmp", "~$*"],
  is_dir_empty: true,
  "plugin:app|version": "0.1.0",
  "plugin:app|name": "Fylvo Desktop",
  "plugin:app|tauri_version": "2.0.0",
};

let nextCallback = 1;
const w = window as unknown as Record<string, unknown>;

w.__TAURI_OS_PLUGIN_INTERNALS__ = {
  version: "10.0.26200",
  platform: "windows",
  os_type: "windows",
  family: "windows",
  arch: "x86_64",
  exe_extension: "exe",
  eol: "\r\n",
};

w.__TAURI_INTERNALS__ = {
  metadata: {
    currentWindow: { label: location.hash.slice(2).split("/")[0] || "main" },
    currentWebview: { label: location.hash.slice(2).split("/")[0] || "main", windowLabel: "main" },
  },
  transformCallback: () => nextCallback++,
  unregisterCallback: () => {},
  convertFileSrc: (path: string) => path,
  invoke: async (cmd: string, args?: unknown) => {
    if (cmd in answers) return structuredClone(answers[cmd]);
    if (cmd.startsWith("plugin:event|")) return nextCallback++;
    console.info("[dev stand-in] invoke", cmd, args);
    return null;
  },
};
