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
        sync_path: sync.clone(),
        enabled: true,
        user_id: "e2e".into(),
        ..Default::default()
    };
    manager.add_drive(config).await.expect("add drive");
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
        let info = LocalFileInfo::from_path(path).unwrap();
        assert!(info.is_placeholder() && info.in_sync(), "{} tracked and in sync", path.display());
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

    // A file created here is not sent to the server in this stage.
    std::fs::write(s.sync.join("local-only.txt"), "mine").unwrap();
    tokio::time::sleep(Duration::from_secs(4)).await;
    let listing = s
        .server
        .client
        .get_file_info(&cloudreve_api::models::explorer::GetFileInfoService {
            uri: Some(s.server.uri("local-only.txt")),
            id: None,
            extended: None,
            folder_summary: None,
        })
        .await;
    assert!(listing.is_err(), "nothing is uploaded yet");
}
