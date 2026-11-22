//! Fylvo: the whole Linux sync (a real sync folder, the app's drive manager, server events)
//! against a local Fylvo server. Linux needs no package identity, so this runs the real thing.
//!
//! Ignored by default; needs the same variables as `server_e2e.rs`:
//! `FYLVO_E2E_URL=http://127.0.0.1:5290 FYLVO_E2E_EMAIL=... FYLVO_E2E_PASSWORD=...
//! cargo test -p cloudreve-sync server_e2e_linux -- --ignored --test-threads=1`
//!
//! The test sets `HOME` to a temp folder (app data, trash), so it must run alone.

use super::server_e2e::Server;
use crate::drive::local::LocalFileInfo;
use crate::drive::local_linux::LocalStamp;
use crate::drive::manager::DriveManager;
use crate::drive::mounts::{Credentials, DriveConfig};
use crate::events::EventBroadcaster;
use cloudreve_api::api::ExplorerApi;
use cloudreve_api::models::explorer::DeleteFileService;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

/// Waits up to a minute for `check`.
async fn wait_for(what: &str, check: impl Fn() -> bool) {
    for _ in 0..240 {
        if check() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(250)).await;
    }
    panic!("timed out waiting for: {what}");
}

fn names(dir: &Path) -> Vec<String> {
    let mut names: Vec<String> = std::fs::read_dir(dir)
        .map(|entries| {
            entries
                .flatten()
                .map(|e| e.file_name().to_string_lossy().to_string())
                .collect()
        })
        .unwrap_or_default();
    names.sort();
    names
}

async fn add_drive(manager: &DriveManager, server: &Server, sync: &Path) {
    let far = "2099-01-01T00:00:00Z".to_string();
    let (access_token, refresh_token) = server.desktop_tokens().await;
    let config = DriveConfig {
        id: uuid::Uuid::new_v4().to_string(),
        name: "e2e".into(),
        instance_url: server.url.clone(),
        remote_path: server.root.clone(),
        credentials: Credentials {
            access_token: Some(access_token),
            refresh_token,
            refresh_expires: far.clone(),
            access_expires: Some(far),
        },
        sync_path: sync.to_path_buf(),
        enabled: true,
        user_id: "e2e".into(),
        ..Default::default()
    };
    manager.add_drive(config).await.expect("add drive");
}

struct Setup {
    server: Server,
    sync: PathBuf,
    home: tempfile::TempDir,
    _manager: Arc<DriveManager>,
}

async fn start() -> Setup {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_test_writer()
        .try_init();
    let home = tempfile::tempdir().unwrap();
    // SAFETY: the test runs alone (`--test-threads=1`), nothing reads the environment meanwhile.
    unsafe { std::env::set_var("HOME", home.path()) };
    let server = Server::connect().await;
    server.folder("").await.expect("remote root");
    let sync = home.path().join("Fylvo");
    std::fs::create_dir(&sync).unwrap();

    let manager = Arc::new(DriveManager::new(Arc::new(EventBroadcaster::new(64))).unwrap());
    add_drive(&manager, &server, &sync).await;
    Setup {
        server,
        sync,
        home,
        _manager: manager,
    }
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs a local Fylvo server, see the module comment"]
async fn server_to_linux_folder() {
    let s = start().await;
    s.server.folder("sub").await.unwrap();
    s.server.folder("sub/deep").await.unwrap();
    s.server.upload("a.txt", "version 1", None).await.unwrap();
    s.server.upload("sub/b.txt", "bee", None).await.unwrap();
    let big = "x".repeat(3 * 1024 * 1024);
    s.server.upload("sub/deep/c.bin", &big, None).await.unwrap();

    let a = s.sync.join("a.txt");
    let b = s.sync.join("sub/b.txt");
    let c = s.sync.join("sub/deep/c.bin");
    wait_for("first downloads", || {
        std::fs::read_to_string(&a).is_ok_and(|t| t == "version 1")
            && std::fs::read_to_string(&b).is_ok_and(|t| t == "bee")
            && std::fs::metadata(&c).is_ok_and(|m| m.len() == big.len() as u64)
    })
    .await;
    for path in [&a, &b, &c, &s.sync.join("sub")] {
        wait_for("tracked and in sync", || {
            let info = LocalFileInfo::from_path(path).unwrap();
            let ok = info.is_placeholder() && info.in_sync();
            if !ok {
                let row = s._manager.get_inventory().query_by_path(path.to_str().unwrap()).unwrap();
                eprintln!("NOT IN SYNC {} now={:?} row={:?}", path.display(), LocalStamp::of_path(path), row.map(|r| r.metadata));
            }
            ok
        })
        .await;
    }
    // No temp files or backups are left.
    assert_eq!(names(&s.sync), ["a.txt", "sub"]);

    // A new version on the server replaces the file here, without leftovers.
    let a_info = s.server.info("a.txt").await;
    s.server
        .upload("a.txt", "version 2, longer", a_info.primary_entity.as_deref())
        .await
        .unwrap();
    wait_for("update", || {
        std::fs::read_to_string(&a).is_ok_and(|t| t == "version 2, longer")
    })
    .await;
    // The old version is removed right after (nothing has it open), no conflict copy.
    wait_for("backup removed", || names(&s.sync) == ["a.txt", "sub"]).await;
    assert!(LocalFileInfo::from_path(&a).unwrap().in_sync());

    // A rename on the server moves the file here (same inode, no new download).
    let inode = LocalStamp::of_path(&b).unwrap().inode;
    s.server.rename("sub/b.txt", "b2.txt").await.unwrap();
    let b2 = s.sync.join("sub/b2.txt");
    wait_for("rename", || b2.exists() && !b.exists()).await;
    assert_eq!(LocalStamp::of_path(&b2).unwrap().inode, inode);
    assert!(LocalFileInfo::from_path(&b2).unwrap().in_sync());

    // A deletion on the server moves the file here to the trash.
    s.server
        .client
        .delete_files(&DeleteFileService {
            uris: vec![s.server.uri("a.txt")],
            unlink: None,
            skip_soft_delete: None,
        })
        .await
        .unwrap();
    wait_for("deletion", || !a.exists()).await;
    let trash = s.home.path().join(".local/share/Trash/files");
    assert!(names(&trash).iter().any(|n| n.starts_with("a")), "{:?}", names(&trash));

    // The server deletion did not come back as an upload, the trash move did not go back to
    // the server as a deletion of something else.
    tokio::time::sleep(Duration::from_secs(5)).await;
    assert!(!s.server.exists("a.txt").await);
    assert!(s.server.exists("sub/b2.txt").await);
}

impl Server {
    /// Whether the server has `relative` (a missing file is an error answer).
    async fn exists(&self, relative: &str) -> bool {
        self.client
            .get_file_info(&cloudreve_api::models::explorer::GetFileInfoService {
                uri: Some(self.uri(relative)),
                id: None,
                extended: None,
                folder_summary: None,
            })
            .await
            .is_ok()
    }

    async fn content(&self, relative: &str) -> Option<String> {
        let mut request = cloudreve_api::models::explorer::FileURLService::default();
        request.uris.push(self.uri(relative));
        let urls = self.client.get_file_url(&request).await.ok()?;
        let url = &urls.urls.first()?.url;
        reqwest::get(url).await.ok()?.text().await.ok()
    }
}

/// Waits until the server has `relative` (any content).
async fn wait_exists(s: &Setup, relative: &str) {
    for _ in 0..120 {
        if s.server.exists(relative).await {
            return;
        }
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    panic!("timed out waiting for the server to have {relative}");
}

/// Waits until the server has `relative` with `content` (or is gone for `None`).
async fn wait_server(s: &Setup, relative: &str, content: Option<&str>) {
    for _ in 0..120 {
        let now = match content {
            Some(_) => s.server.content(relative).await,
            None => (!s.server.exists(relative).await).then(String::new),
        };
        let done = match content {
            Some(want) => now.as_deref() == Some(want),
            None => now.is_some(),
        };
        if done {
            return;
        }
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    panic!("timed out waiting for the server: {relative} = {content:?}");
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "needs a local Fylvo server, see the module comment"]
async fn linux_folder_to_server() {
    let s = start().await;
    let file_id = |relative: &'static str| {
        let server = &s.server;
        async move { server.info(relative).await.id }
    };

    // A new file and a new folder with content go up.
    std::fs::write(s.sync.join("notes.txt"), "one").unwrap();
    std::fs::create_dir_all(s.sync.join("Docs/Old")).unwrap();
    std::fs::write(s.sync.join("Docs/Old/x.txt"), "x").unwrap();
    wait_server(&s, "notes.txt", Some("one")).await;
    wait_server(&s, "Docs/Old/x.txt", Some("x")).await;
    wait_for("tracked", || {
        LocalFileInfo::from_path(&s.sync.join("notes.txt")).is_ok_and(|i| i.in_sync())
    })
    .await;
    let notes_id = file_id("notes.txt").await;

    // An edit in place is a new version of the same file.
    std::fs::write(s.sync.join("notes.txt"), "one, two").unwrap();
    wait_server(&s, "notes.txt", Some("one, two")).await;
    assert_eq!(file_id("notes.txt").await, notes_id);

    // A save as gedit does it (temp file renamed over the file) is a new version as well.
    let temp = s.sync.join(".goutputstream-ABC123");
    std::fs::write(&temp, "one, two, three").unwrap();
    std::fs::rename(&temp, s.sync.join("notes.txt")).unwrap();
    wait_server(&s, "notes.txt", Some("one, two, three")).await;
    assert_eq!(file_id("notes.txt").await, notes_id);
    assert!(!s.server.exists(".goutputstream-ABC123").await);

    // A rename and a move keep the server file (same ID), no copy is left behind.
    std::fs::rename(s.sync.join("notes.txt"), s.sync.join("renamed.txt")).unwrap();
    wait_server(&s, "notes.txt", None).await;
    wait_exists(&s, "renamed.txt").await;
    assert_eq!(file_id("renamed.txt").await, notes_id);
    std::fs::rename(s.sync.join("renamed.txt"), s.sync.join("Docs/moved.txt")).unwrap();
    wait_server(&s, "renamed.txt", None).await;
    wait_exists(&s, "Docs/moved.txt").await;
    assert_eq!(file_id("Docs/moved.txt").await, notes_id);
    // Moved and renamed in one step.
    std::fs::rename(s.sync.join("Docs/moved.txt"), s.sync.join("both.txt")).unwrap();
    wait_server(&s, "Docs/moved.txt", None).await;
    wait_exists(&s, "both.txt").await;
    assert_eq!(file_id("both.txt").await, notes_id);
    // A move into a folder made a moment ago (not on the server yet) is still a move.
    // (Only once the last rename is recorded here: a second move within that second is seen
    // as delete plus upload, nothing is lost.)
    wait_for("both.txt tracked", || {
        LocalFileInfo::from_path(&s.sync.join("both.txt")).is_ok_and(|i| i.in_sync())
    })
    .await;
    std::fs::create_dir(s.sync.join("Fresh")).unwrap();
    std::fs::rename(s.sync.join("both.txt"), s.sync.join("Fresh/both.txt")).unwrap();
    wait_server(&s, "both.txt", None).await;
    wait_exists(&s, "Fresh/both.txt").await;
    assert_eq!(file_id("Fresh/both.txt").await, notes_id);
    std::fs::rename(s.sync.join("Fresh/both.txt"), s.sync.join("both.txt")).unwrap();
    wait_exists(&s, "both.txt").await;

    // A folder rename takes its content along.
    std::fs::rename(s.sync.join("Docs/Old"), s.sync.join("Docs/New")).unwrap();
    wait_server(&s, "Docs/Old", None).await;
    wait_server(&s, "Docs/New/x.txt", Some("x")).await;

    // A deletion and a "Move to Trash" (a move out of the folder) go to the server's trash.
    std::fs::remove_file(s.sync.join("Docs/New/x.txt")).unwrap();
    wait_server(&s, "Docs/New/x.txt", None).await;
    let trash = s.home.path().join("trash-of-the-desktop");
    std::fs::create_dir(&trash).unwrap();
    std::fs::rename(s.sync.join("both.txt"), trash.join("both.txt")).unwrap();
    wait_server(&s, "both.txt", None).await;

    // A folder moved in from outside goes up with its content.
    let outside = s.home.path().join("Outside");
    std::fs::create_dir_all(outside.join("inner")).unwrap();
    std::fs::write(outside.join("inner/y.txt"), "y").unwrap();
    std::fs::rename(&outside, s.sync.join("Outside")).unwrap();
    wait_server(&s, "Outside/inner/y.txt", Some("y")).await;

    // A symlink to a folder outside is never followed or uploaded.
    let secret = s.home.path().join("Secret");
    std::fs::create_dir(&secret).unwrap();
    std::fs::write(secret.join("pw.txt"), "secret").unwrap();
    std::os::unix::fs::symlink(&secret, s.sync.join("link")).unwrap();

    // Two names that differ only in case: the server takes one, the other stays here.
    std::fs::write(s.sync.join("case.txt"), "lower").unwrap();
    wait_server(&s, "case.txt", Some("lower")).await;
    std::fs::write(s.sync.join("CASE.txt"), "upper").unwrap();

    tokio::time::sleep(Duration::from_secs(8)).await;
    assert!(!s.server.exists("link").await);
    assert!(!s.server.exists("link/pw.txt").await);
    assert_eq!(s.server.content("case.txt").await.as_deref(), Some("lower"));
    assert!(s.sync.join("CASE.txt").exists());

    // Many deletions at once wait for the user.
    std::fs::create_dir(s.sync.join("Many")).unwrap();
    for i in 0..15 {
        std::fs::write(s.sync.join(format!("Many/{i}.txt")), format!("{i}")).unwrap();
    }
    wait_server(&s, "Many/14.txt", Some("14")).await;
    wait_for("all tracked", || {
        (0..15).all(|i| {
            LocalFileInfo::from_path(&s.sync.join(format!("Many/{i}.txt"))).is_ok_and(|f| f.in_sync())
        })
    })
    .await;
    for i in 0..15 {
        std::fs::remove_file(s.sync.join(format!("Many/{i}.txt"))).unwrap();
    }
    tokio::time::sleep(Duration::from_secs(10)).await;
    assert!(s.server.exists("Many/0.txt").await, "a mass deletion is held");

    // A move into a second sync folder is undone, as Windows refuses it; copying works.
    let other = Server::connect().await;
    other.folder("").await.unwrap();
    let second = s.home.path().join("Fylvo2");
    std::fs::create_dir(&second).unwrap();
    add_drive(&s._manager, &other, &second).await;
    std::fs::write(s.sync.join("stay.txt"), "stay").unwrap();
    wait_server(&s, "stay.txt", Some("stay")).await;
    wait_for("stay tracked", || {
        LocalFileInfo::from_path(&s.sync.join("stay.txt")).is_ok_and(|i| i.in_sync())
    })
    .await;
    std::fs::rename(s.sync.join("stay.txt"), second.join("stay.txt")).unwrap();
    wait_for("moved back", || s.sync.join("stay.txt").exists() && !second.join("stay.txt").exists()).await;
    tokio::time::sleep(Duration::from_secs(6)).await;
    assert_eq!(s.server.content("stay.txt").await.as_deref(), Some("stay"));
    assert!(!other.exists("stay.txt").await);
}
