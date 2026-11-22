//! Fylvo: updates from the app (desktop/UPDATER.md). Asks the signed-in servers of the drives
//! for a newer version, downloads and checks it, waits until no sync work runs, stops sync like
//! "Quit" and lets Windows install it; Windows restarts the app afterwards.
//!
//! The general steps come from the crate `fylvo-update`; only `install()` is per platform.

use cloudreve_sync::{activity, ConfigManager, DriveManager};
use fylvo_update::state::{Installing, StartOutcome, State};
use fylvo_update::{Downloaded, Platform, Release, Server, Version};
use serde::Serialize;
use std::path::PathBuf;
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tauri::AppHandle;
use tokio::sync::Notify;

const FIRST_CHECK_AFTER: Duration = Duration::from_secs(2 * 60);
const CHECK_EVERY: Duration = Duration::from_secs(6 * 60 * 60);
const BUSY_RETRY_EVERY: Duration = Duration::from_secs(5 * 60);
/// After the gate closed: how long to wait for work that was already on its way (file events
/// are delayed by 2 s and polled every 0.5 s) before sync is stopped.
const SETTLE: Duration = Duration::from_secs(5);
const BUSY_GIVE_UP_AFTER: Duration = Duration::from_secs(24 * 60 * 60);
/// Windows restarts an app after an update only if it ran for at least 60 seconds.
const MIN_UPTIME: Duration = Duration::from_secs(65);

/// What the settings page shows.
#[derive(Clone, Debug, Default, Serialize)]
pub struct UpdateStatus {
    /// The running version, empty without package identity.
    pub version: String,
    /// False when the app runs without package identity (then there are no updates).
    pub supported: bool,
    /// idle, checking, no_drive, downloading, waiting, installing, up_to_date, failed
    pub phase: &'static str,
    /// The version found or being installed.
    pub target: Option<String>,
    /// Why the last run failed.
    pub error: Option<String>,
    /// Unix seconds of the last finished check.
    pub last_check: Option<u64>,
}

struct Updater {
    status: Mutex<UpdateStatus>,
    check_now: Notify,
    started: Instant,
    dir: PathBuf,
}

static UPDATER: OnceLock<Arc<Updater>> = OnceLock::new();

/// Automatic updates are on (the switch in the settings).
fn auto_update_on() -> bool {
    ConfigManager::try_get().map(|c| c.auto_update()).unwrap_or(true)
}

fn now() -> u64 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0)
}

fn update_dir() -> PathBuf {
    dirs::home_dir().unwrap_or_default().join(cloudreve_sync::DATA_DIR).join("update")
}

fn set_status(f: impl FnOnce(&mut UpdateStatus)) {
    if let Some(u) = UPDATER.get() {
        f(&mut u.status.lock().unwrap_or_else(|e| e.into_inner()));
    }
}

/// The status for the settings page.
pub fn status() -> UpdateStatus {
    UPDATER
        .get()
        .map(|u| u.status.lock().unwrap_or_else(|e| e.into_inner()).clone())
        .unwrap_or_default()
}

/// "Check now" in the settings: runs a check at once, also with automatic updates off.
pub fn check_now() {
    if let Some(u) = UPDATER.get() {
        u.check_now.notify_one();
    }
}

/// Starts the updater once the sync service runs. Without package identity it only reports
/// that updates are not available.
pub fn start(app: AppHandle, drives: Arc<DriveManager>) {
    let running = current_version();
    let updater = Arc::new(Updater {
        status: Mutex::new(UpdateStatus {
            version: running.map(|v| v.to_string()).unwrap_or_default(),
            supported: running.is_some(),
            phase: "idle",
            ..Default::default()
        }),
        check_now: Notify::new(),
        started: Instant::now(),
        dir: update_dir(),
    });
    if UPDATER.set(updater.clone()).is_err() {
        return;
    }
    let Some(running) = running else {
        tracing::info!(target: "updater", "No package identity, updates are off");
        return;
    };

    report_last_update(&updater.dir, running);
    fylvo_update::clean_downloads(&updater.dir);
    tracing::info!(target: "updater", %running, "Updater started");

    let task = tauri::async_runtime::spawn(async move {
        let mut wait = FIRST_CHECK_AFTER;
        loop {
            let manual = tokio::select! {
                _ = tokio::time::sleep(wait) => false,
                _ = updater.check_now.notified() => true,
            };
            wait = CHECK_EVERY;
            if !manual && !auto_update_on() {
                continue;
            }
            tracing::info!(target: "updater", manual, "Checking for updates");
            if let Err(e) = run(&app, &updater, &drives, running, manual).await {
                tracing::warn!(target: "updater", error = %e, "Update failed");
                set_status(|s| {
                    s.phase = "failed";
                    s.error = Some(e.to_string());
                });
            }
            set_status(|s| s.last_check = Some(now()));
            fylvo_update::clean_downloads(&updater.dir);
        }
    });
    // A panic in the loop would otherwise end it without a trace.
    tauri::async_runtime::spawn(async move {
        // The loop never ends, so only a panic gets here.
        let Err(e) = task.await;
        tracing::error!(target: "updater", error = %e, "The updater stopped");
    });
}

/// At start: was the last update installed? Shows the notification and keeps a failure.
fn report_last_update(dir: &std::path::Path, running: Version) {
    let mut state = State::load(dir);
    let outcome = state.on_start(running, now());
    if outcome == StartOutcome::Nothing {
        return;
    }
    if let Err(e) = state.save(dir) {
        tracing::warn!(target: "updater", error = %e, "Failed to save the update state");
    }
    match outcome {
        StartOutcome::Updated { from, to } => {
            tracing::info!(target: "updater", %from, %to, "Updated");
            cloudreve_sync::utils::toast::send_general_text_toast(
                &t!("updateDoneTitle"),
                &t!("updateDoneMessage", version = short(to)),
            );
        }
        StartOutcome::Failed { version, error } => {
            tracing::warn!(target: "updater", %version, ?error, "The last update did not install");
            let reason = error.unwrap_or_else(|| t!("updateNotInstalled").to_string());
            set_status(|s| {
                s.phase = "failed";
                s.target = Some(short(version));
                s.error = Some(reason.clone());
            });
            cloudreve_sync::utils::toast::send_warning_toast(
                &t!("updateFailedTitle"),
                &t!("updateFailedMessage", version = short(version), reason = reason),
            );
        }
        StartOutcome::Nothing => {}
    }
}

/// "0.3.0.0" -> "0.3.0" for people.
fn short(v: Version) -> String {
    let [a, b, c, d] = v.0;
    if d == 0 { format!("{a}.{b}.{c}") } else { format!("{a}.{b}.{c}.{d}") }
}

/// One check: find, download, check, wait for idle, install.
async fn run(
    app: &AppHandle,
    updater: &Updater,
    drives: &Arc<DriveManager>,
    running: Version,
    manual: bool,
) -> anyhow::Result<()> {
    set_status(|s| {
        s.phase = "checking";
        s.error = None;
    });
    if drives.mounts().await.is_empty() {
        set_status(|s| s.phase = "no_drive");
        return Ok(());
    }
    let platform = current_platform().ok_or_else(|| anyhow::anyhow!("no updates for this platform"))?;
    let http = fylvo_update::http_client(cloudreve_sync::USER_AGENT)?;
    let Some((server, release)) = find_release(&http, drives, &platform).await? else {
        tracing::info!(target: "updater", "No release published");
        set_status(|s| s.phase = "up_to_date");
        return Ok(());
    };
    if !fylvo_update::is_newer(release.version, running) {
        tracing::info!(target: "updater", published = %release.version, "Up to date");
        set_status(|s| s.phase = "up_to_date");
        return Ok(());
    }
    if !manual && State::load(&updater.dir).blocked(release.version, now()) {
        tracing::info!(target: "updater", version = %release.version, "Failed earlier, not tried again yet");
        set_status(|s| {
            s.phase = "failed";
            s.target = Some(short(release.version));
            s.error = Some(t!("updateNotInstalled").to_string());
        });
        return Ok(());
    }

    tracing::info!(target: "updater", version = %release.version, "Update found, downloading");
    set_status(|s| {
        s.phase = "downloading";
        s.target = Some(short(release.version));
    });
    let mut release = release;
    let path = match download_release(&http, &server, &platform, &mut release, running, &updater.dir).await {
        Ok(Some(path)) => path,
        Ok(None) => {
            set_status(|s| s.phase = "up_to_date");
            return Ok(());
        }
        Err(e) => {
            report_failure(&updater.dir, release.version, &e);
            return Err(e);
        }
    };

    let result = install(app, updater, drives, running, &release, path, manual).await;
    if let Err(e) = &result {
        report_failure(&updater.dir, release.version, e);
    }
    result
}

/// Downloads the release; a release replaced between the check and the download is asked for
/// once more. `None`: nothing newer is published any more.
async fn download_release(
    http: &reqwest::Client,
    server: &Server,
    platform: &Platform,
    release: &mut Release,
    running: Version,
    dir: &std::path::Path,
) -> anyhow::Result<Option<PathBuf>> {
    for _ in 0..2 {
        match fylvo_update::download(http, server, platform, release, dir).await? {
            Downloaded::File(path) => return Ok(Some(path)),
            Downloaded::Gone => match fylvo_update::latest(http, server, platform).await? {
                Some(r) if fylvo_update::is_newer(r.version, running) => *release = r,
                _ => return Ok(None),
            },
        }
    }
    anyhow::bail!("the release changed during the download")
}

/// A found update did not install: one notification, and not tried again for a day unless the
/// user asks.
fn report_failure(dir: &std::path::Path, version: Version, error: &anyhow::Error) {
    let mut state = State::load(dir);
    state.failed = Some(fylvo_update::state::Failed { version, at: now() });
    let _ = state.save(dir);
    cloudreve_sync::utils::toast::send_warning_toast(
        &t!("updateFailedTitle"),
        &t!("updateFailedMessage", version = short(version), reason = format!("{error:#}")),
    );
}

/// The first signed-in drive whose server has a release for this platform.
async fn find_release(
    http: &reqwest::Client,
    drives: &Arc<DriveManager>,
    platform: &Platform,
) -> anyhow::Result<Option<(Server, Release)>> {
    let mut last_error = None;
    for mount in drives.mounts().await {
        let token = match mount.cr_client.fresh_access_token().await {
            Ok(t) => t,
            Err(e) => {
                last_error = Some(anyhow::anyhow!("not signed in: {e}"));
                continue;
            }
        };
        let server = Server { base_url: mount.cr_client.base_url().to_string(), access_token: token };
        match fylvo_update::latest(http, &server, &platform).await {
            Ok(Some(release)) => return Ok(Some((server, release))),
            Ok(None) => {}
            Err(e) => {
                tracing::info!(target: "updater", server = %server.base_url, error = %e, "No answer about updates");
                last_error = Some(e);
            }
        }
    }
    match last_error {
        Some(e) => Err(e),
        None => Ok(None),
    }
}

/// The package format this app was installed as.
#[cfg(not(target_os = "linux"))]
fn current_platform() -> Option<Platform> {
    fylvo_update::windows_platform()
}

/// Linux: an AppImage knows itself (`APPIMAGE`); else the package manager that owns the program.
#[cfg(target_os = "linux")]
fn current_platform() -> Option<Platform> {
    fylvo_update::linux_platform(crate::linux_update::format()?)
}

#[cfg(windows)]
fn current_version() -> Option<Version> {
    fylvo_update::windows::current().map(|id| id.version)
}

#[cfg(target_os = "linux")]
fn current_version() -> Option<Version> {
    // A build from source (no package) has no update path.
    crate::linux_update::format()?;
    Version::parse(env!("CARGO_PKG_VERSION"))
}

#[cfg(not(any(windows, target_os = "linux")))]
fn current_version() -> Option<Version> {
    None
}

/// Waits until no sync work runs; then the gate holds new work back. `false`: automatic updates
/// were switched off meanwhile (an automatic update does not install then, a manual one does).
/// `min_uptime`: Windows restarts an app after an update only if it ran that long.
async fn wait_until_idle(
    updater: &Updater,
    drives: &Arc<DriveManager>,
    manual: bool,
    min_uptime: Duration,
) -> anyhow::Result<bool> {
    set_status(|s| s.phase = "waiting");
    let waiting_since = Instant::now();
    loop {
        if !manual && !auto_update_on() {
            tracing::info!(target: "updater", "Automatic updates were switched off, not installing");
            set_status(|s| s.phase = "idle");
            return Ok(false);
        }
        if updater.started.elapsed() >= min_uptime && activity::close_if_idle(|| drives.has_pending_work()) {
            // Work that was on its way when the gate closed (a save in the 2 s file event
            // delay, a command in a channel) shows up at the gate within this pause.
            tokio::time::sleep(SETTLE).await;
            if !manual && !auto_update_on() {
                activity::reopen();
                continue;
            }
            if activity::still_idle() && !drives.has_pending_work() {
                return Ok(true);
            }
            activity::reopen();
        }
        if waiting_since.elapsed() >= BUSY_GIVE_UP_AFTER {
            anyhow::bail!("sync was busy for a whole day");
        }
        let pause = if updater.started.elapsed() < min_uptime { Duration::from_secs(5) } else { BUSY_RETRY_EVERY };
        tokio::time::sleep(pause).await;
    }
}

/// Windows: check the package, let Windows stage it, wait until idle, stop sync, register.
#[cfg(windows)]
async fn install(
    app: &AppHandle,
    updater: &Updater,
    drives: &Arc<DriveManager>,
    running: Version,
    release: &Release,
    path: PathBuf,
    manual: bool,
) -> anyhow::Result<()> {
    use fylvo_update::windows as win;

    let current = win::current().ok_or_else(|| anyhow::anyhow!("no package identity"))?;
    let package = {
        let path = path.clone();
        tokio::task::spawn_blocking(move || win::read_identity(&path)).await??
    };
    win::check_package(&package, &current, release.version)?;
    {
        let path = path.clone();
        tokio::task::spawn_blocking(move || win::stage(&path)).await??;
    }
    tracing::info!(target: "updater", package = %package.full_name, "Update staged");

    if !wait_until_idle(updater, drives, manual, MIN_UPTIME).await? {
        return Ok(());
    }

    set_status(|s| s.phase = "installing");
    let mut state = State::load(&updater.dir);
    state.installing = Some(Installing { version: release.version, from: running, error: None });
    if let Err(e) = state.save(&updater.dir) {
        activity::reopen();
        anyhow::bail!("failed to save the update state: {e}");
    }

    tracing::info!(target: "updater", "Stopping sync for the update");
    if let Err(e) = crate::shutdown_sync().await {
        // Sync is stopped already: start the app again instead of installing.
        tracing::error!(target: "updater", error = %e, "Failed to save drive settings, not installing");
        let mut state = State::load(&updater.dir);
        state.installing = None;
        let _ = state.save(&updater.dir);
        restart(app, &current.family_name);
        anyhow::bail!("failed to save drive settings: {e}");
    }

    let full_name = package.full_name.clone();
    let result = tokio::task::spawn_blocking(move || win::register(&full_name)).await;
    // Here Windows normally has closed the app already. Still running: either it failed, or
    // Windows did not need to close it; start the installed version fresh in both cases.
    let error = match result {
        Ok(Ok(())) => None,
        Ok(Err(e)) => Some(e),
        Err(e) => Some(anyhow::anyhow!(e)),
    };
    // The state file still says "installing": the next start tells success from failure and
    // shows the one notification about it, with Windows' reason.
    if let Some(e) = &error {
        tracing::error!(target: "updater", error = %e, "Registering the update failed");
        let mut state = State::load(&updater.dir);
        if let Some(installing) = state.installing.as_mut() {
            installing.error = Some(format!("{e:#}"));
            let _ = state.save(&updater.dir);
        }
    }
    restart(app, &current.family_name);
    Ok(())
}

/// Linux: wait until idle, stop sync, put the new version in place (AppImage: the file itself;
/// rpm/deb: the package manager, after the system asked for the admin password), start it.
#[cfg(target_os = "linux")]
async fn install(
    app: &AppHandle,
    updater: &Updater,
    drives: &Arc<DriveManager>,
    running: Version,
    release: &Release,
    path: PathBuf,
    manual: bool,
) -> anyhow::Result<()> {
    use crate::linux_update as lin;
    let format = lin::format().ok_or_else(|| anyhow::anyhow!("not installed as a package"))?;
    // The AppImage is prepared next to the running one before anything stops.
    let prepared = match format {
        fylvo_update::LinuxFormat::AppImage => Some(lin::prepare_appimage(&path)?),
        _ => None,
    };
    if !wait_until_idle(updater, drives, manual, Duration::ZERO).await? {
        if let Some(prepared) = &prepared {
            let _ = std::fs::remove_file(prepared);
        }
        return Ok(());
    }
    set_status(|s| s.phase = "installing");
    let mut state = State::load(&updater.dir);
    state.installing = Some(Installing { version: release.version, from: running, error: None });
    if let Err(e) = state.save(&updater.dir) {
        activity::reopen();
        anyhow::bail!("failed to save the update state: {e}");
    }
    tracing::info!(target: "updater", "Stopping sync for the update");
    if let Err(e) = crate::shutdown_sync().await {
        tracing::error!(target: "updater", error = %e, "Failed to save drive settings, not installing");
        let mut state = State::load(&updater.dir);
        state.installing = None;
        let _ = state.save(&updater.dir);
        lin::restart(app);
        anyhow::bail!("failed to save drive settings: {e}");
    }
    let result = match (format, prepared) {
        (fylvo_update::LinuxFormat::AppImage, Some(prepared)) => lin::replace_appimage(&prepared),
        (other, _) => {
            let path = path.clone();
            tokio::task::spawn_blocking(move || lin::install_package(other, &path))
                .await
                .map_err(|e| anyhow::anyhow!(e))
                .and_then(|r| r)
        }
    };
    if let Err(e) = &result {
        // The next start tells the failure (the state still says "installing").
        tracing::error!(target: "updater", error = %e, "Installing the update failed");
        let mut state = State::load(&updater.dir);
        if let Some(installing) = state.installing.as_mut() {
            installing.error = Some(format!("{e:#}"));
            let _ = state.save(&updater.dir);
        }
    }
    lin::restart(app);
    Ok(())
}

#[cfg(not(any(windows, target_os = "linux")))]
async fn install(
    _app: &AppHandle,
    _updater: &Updater,
    _drives: &Arc<DriveManager>,
    _running: Version,
    _release: &Release,
    _path: PathBuf,
    _manual: bool,
) -> anyhow::Result<()> {
    anyhow::bail!("updates are not supported on this platform yet")
}

/// Starts the app again through Windows (so it has its package identity) once this process
/// has ended, then ends this process. Started earlier, the single-instance check would hand
/// the new start to this process. If the helper cannot be started, the app restarts itself.
#[cfg(windows)]
fn restart(app: &AppHandle, family_name: &str) {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    // The family name is Windows' own ([A-Za-z0-9._-]), so it needs no quoting.
    // Waits for this process, then starts the app and checks that it runs (three tries).
    let script = format!(
        "Wait-Process -Id {pid} -Timeout 120 -ErrorAction SilentlyContinue; \
         for ($i = 0; $i -lt 3; $i++) {{ \
           Start-Process 'shell:AppsFolder\\{family_name}!Fylvo.Sync' -ErrorAction SilentlyContinue; \
           Start-Sleep -Seconds 10; \
           if (Get-Process fylvo-desktop -ErrorAction SilentlyContinue | Where-Object Id -ne {pid}) {{ break }} \
         }}",
        pid = std::process::id()
    );
    let started = std::process::Command::new("powershell.exe")
        .args(["-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", &script])
        .creation_flags(CREATE_NO_WINDOW)
        .spawn();
    match started {
        Ok(_) => app.exit(0),
        Err(e) => {
            tracing::error!(target: "updater", error = %e, "Failed to start the restart helper, restarting directly");
            app.restart();
        }
    }
}
