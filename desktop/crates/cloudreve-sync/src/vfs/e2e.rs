//! Fylvo: the virtual drive end to end, a real FUSE mount against a local Fylvo server.
//!
//! Ignored by default. Needs `/dev/fuse`, `fusermount3` and the variables of
//! `tasks/server_e2e.rs`:
//! `FYLVO_E2E_URL=http://127.0.0.1:5290 FYLVO_E2E_EMAIL=... FYLVO_E2E_PASSWORD=...
//! cargo test -p cloudreve-sync vfs::e2e -- --ignored --test-threads=1`
//!
//! The tests set `HOME` to a temp folder (the drive keeps its store there), so they run alone.

use super::{VirtualDrive, TEST_LIMIT};
use crate::inventory::InventoryDb;
use crate::tasks::server_e2e::Server;
use cloudreve_api::api::explorer::ExplorerApiExt;
use cloudreve_api::api::ExplorerApi;
use cloudreve_api::models::explorer::FileURLService;
use cloudreve_api::{Client, ClientConfig};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::{Duration, Instant};

const MIB: usize = 1024 * 1024;

/// Run blocking file system calls off the runtime (the mount is served by it).
async fn fs<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    tokio::task::spawn_blocking(f).await.unwrap()
}

/// Wait up to a minute for `check`.
async fn wait_for<F, Fut>(what: &str, check: F)
where
    F: Fn() -> Fut,
    Fut: std::future::Future<Output = bool>,
{
    for _ in 0..240 {
        if check().await {
            return;
        }
        tokio::time::sleep(Duration::from_millis(250)).await;
    }
    panic!("timed out waiting for: {what}");
}

/// The bytes of the current version of `relative` on the server.
async fn server_bytes(server: &Server, relative: &str) -> Option<Vec<u8>> {
    let mut request = FileURLService::default();
    request.uris.push(server.uri(relative));
    let answer = server.client.get_file_url(&request).await.ok()?;
    let url = answer.urls.first()?.url.clone();
    Some(reqwest::get(url).await.ok()?.bytes().await.ok()?.to_vec())
}

async fn server_names(server: &Server, relative: &str) -> Vec<String> {
    let uri = if relative.is_empty() { server.root.clone() } else { server.uri(relative) };
    match server.client.list_files_all(None, &uri, 1000).await {
        Ok(page) => page.res.files.iter().map(|f| f.name.clone()).collect(),
        Err(_) => Vec::new(),
    }
}

fn pattern(len: usize) -> String {
    (0..len).map(|i| (b'a' + (i % 26) as u8) as char).collect()
}

fn mounted(path: &Path) -> bool {
    std::fs::read_to_string("/proc/self/mountinfo")
        .unwrap_or_default()
        .contains(&format!(" {} ", path.display()))
}

struct Setup {
    server: Server,
    home: tempfile::TempDir,
    inventory: Arc<InventoryDb>,
}

async fn setup() -> Setup {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(std::env::var("RUST_LOG").unwrap_or_else(|_| "vfs=debug,uploader=info".into()))
        .with_test_writer()
        .try_init();
    let home = tempfile::tempdir().unwrap();
    // SAFETY: the e2e tests run alone (`--test-threads=1`).
    unsafe { std::env::set_var("HOME", home.path()) };
    let server = Server::connect().await;
    server.folder("").await.expect("test root");
    let inventory = Arc::new(InventoryDb::with_path(home.path().join("meta.db")).unwrap());
    Setup { server, home, inventory }
}

async fn mount(setup: &Setup, client: Arc<Client>, url: &str) -> (Arc<VirtualDrive>, PathBuf) {
    let mount_point = setup.home.path().join("Fylvo");
    let drive = VirtualDrive::start("e2e-drive", &mount_point, &setup.server.root, url, client, setup.inventory.clone())
        .await
        .expect("mount");
    assert!(mounted(&mount_point));
    (drive, mount_point)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore]
async fn the_drive_reads_and_writes_like_a_folder() {
    let setup = setup().await;
    let server = &setup.server;
    server.folder("docs").await.unwrap();
    server.upload("docs/hello.txt", "hello", None).await.unwrap();
    let big = pattern(10 * MIB);
    server.upload("big.bin", &big, None).await.unwrap();
    let (drive, mnt) = mount(&setup, server.client.clone(), &server.url).await;

    // A path is found before its folder was listed.
    let m = mnt.clone();
    let size = fs(move || std::fs::metadata(m.join("docs/hello.txt")).unwrap().len()).await;
    assert_eq!(size, 5);
    let m = mnt.clone();
    let mut names: Vec<String> = fs(move || {
        std::fs::read_dir(&m).unwrap().map(|e| e.unwrap().file_name().to_string_lossy().to_string()).collect()
    })
    .await;
    names.sort();
    assert_eq!(names, vec!["big.bin".to_string(), "docs".to_string()]);
    let m = mnt.clone();
    assert_eq!(fs(move || std::fs::read_to_string(m.join("docs/hello.txt")).unwrap()).await, "hello");

    // A read in the middle of a big file loads only that part.
    let m = mnt.clone();
    let part = fs(move || {
        let mut file = std::fs::File::open(m.join("big.bin")).unwrap();
        file.seek(SeekFrom::Start(9 * MIB as u64 + 7)).unwrap();
        let mut buf = vec![0u8; 100];
        file.read_exact(&mut buf).unwrap();
        buf
    })
    .await;
    assert_eq!(part, big.as_bytes()[9 * MIB + 7..9 * MIB + 107]);
    let used = drive.status().cache_used;
    assert!(used <= (2 * MIB) as u64 + 5, "loaded {used} bytes for a read of 100");

    // A new file is uploaded after it is closed.
    let m = mnt.clone();
    fs(move || std::fs::write(m.join("docs/new.txt"), "new content").unwrap()).await;
    wait_for("new file on the server", || async {
        server_bytes(server, "docs/new.txt").await.as_deref() == Some(b"new content".as_slice())
    })
    .await;

    // An edit makes a new version of the same file.
    let id = server.info("docs/hello.txt").await.id;
    let m = mnt.clone();
    fs(move || {
        let mut file = std::fs::OpenOptions::new().append(true).open(m.join("docs/hello.txt")).unwrap();
        file.write_all(b" world").unwrap();
    })
    .await;
    wait_for("edit on the server", || async {
        server_bytes(server, "docs/hello.txt").await.as_deref() == Some(b"hello world".as_slice())
    })
    .await;
    assert_eq!(server.info("docs/hello.txt").await.id, id);

    // gedit's save (temp file, renamed over the file) keeps the same file on the server.
    let m = mnt.clone();
    fs(move || {
        std::fs::write(m.join("docs/.goutputstream-ABC123"), "saved by gedit").unwrap();
        std::fs::rename(m.join("docs/.goutputstream-ABC123"), m.join("docs/hello.txt")).unwrap();
    })
    .await;
    wait_for("gedit save on the server", || async {
        server_bytes(server, "docs/hello.txt").await.as_deref() == Some(b"saved by gedit".as_slice())
    })
    .await;
    assert_eq!(server.info("docs/hello.txt").await.id, id);
    assert!(!server_names(server, "docs").await.iter().any(|n| n.starts_with(".goutputstream")));

    // Folders, renames, moves and deletions reach the server.
    let m = mnt.clone();
    let cross = fs(move || {
        std::fs::create_dir(m.join("newdir")).unwrap();
        std::fs::rename(m.join("docs/new.txt"), m.join("docs/renamed.txt")).unwrap();
        std::fs::rename(m.join("docs/renamed.txt"), m.join("newdir/renamed.txt")).unwrap();
        std::fs::rename(m.join("newdir/renamed.txt"), m.join("docs/other-name.txt"))
    })
    .await;
    assert_eq!(cross.unwrap_err().raw_os_error(), Some(libc::EXDEV));
    assert!(server_names(server, "newdir").await.contains(&"renamed.txt".to_string()));
    assert!(!server_names(server, "docs").await.contains(&"new.txt".to_string()));
    let m = mnt.clone();
    fs(move || {
        std::fs::remove_file(m.join("newdir/renamed.txt")).unwrap();
        std::fs::remove_dir(m.join("newdir")).unwrap();
    })
    .await;
    assert!(!server_names(server, "").await.contains(&"newdir".to_string()));

    // Someone else saves while we edit: our version becomes a conflict copy.
    let m = mnt.clone();
    let mut handle = fs(move || {
        let mut file = std::fs::OpenOptions::new().write(true).truncate(true).open(m.join("docs/hello.txt")).unwrap();
        file.write_all(b"local edit").unwrap();
        file
    })
    .await;
    let current = server.info("docs/hello.txt").await.primary_entity.unwrap();
    server.upload("docs/hello.txt", "server edit", Some(&current)).await.unwrap();
    fs(move || {
        handle.flush().unwrap();
        drop(handle);
    })
    .await;
    wait_for("conflict copy on the server", || async {
        server_names(server, "docs").await.iter().any(|n| n.contains("hello") && n != "hello.txt")
    })
    .await;
    let copy = server_names(server, "docs").await.into_iter().find(|n| n.contains("hello") && n != "hello.txt").unwrap();
    assert_eq!(server_bytes(server, &format!("docs/{copy}")).await.unwrap(), b"local edit");
    assert_eq!(server_bytes(server, "docs/hello.txt").await.unwrap(), b"server edit");
    let m = mnt.clone();
    wait_for("the file shows the server version", || {
        let m = m.clone();
        async move { fs(move || std::fs::read_to_string(m.join("docs/hello.txt")).unwrap_or_default()).await == "server edit" }
    })
    .await;

    // The limit evicts the oldest content once a file is closed.
    TEST_LIMIT.store(3 * MIB as u64, Ordering::SeqCst);
    let m = mnt.clone();
    let all = fs(move || std::fs::read(m.join("big.bin")).unwrap()).await;
    assert_eq!(all, big.as_bytes());
    drive.free_space(Some("docs"));
    let m = mnt.clone();
    fs(move || std::fs::read(m.join("docs/hello.txt")).unwrap()).await;
    wait_for("the limit is kept", || async { drive.status().cache_used <= 3 * MIB as u64 }).await;
    TEST_LIMIT.store(0, Ordering::SeqCst);

    // A pinned folder is loaded fully.
    drive.pin("docs").unwrap();
    let expected: u64 = 11 + 10; // "server edit" and "local edit"
    wait_for("pinned folder loaded", || async { drive.status().pinned_bytes >= expected }).await;

    drive.stop();
    assert!(!mounted(&mnt));
    assert_eq!(drive.unsent_count(), 0);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore]
async fn offline_only_cached_files_are_there_and_changes_wait() {
    let setup = setup().await;
    let server = &setup.server;
    server.upload("cached.txt", "cached", None).await.unwrap();
    server.upload("remote.txt", "only on the server", None).await.unwrap();

    // Online once: list and read one file.
    let (drive, mnt) = mount(&setup, server.client.clone(), &server.url).await;
    let m = mnt.clone();
    assert_eq!(fs(move || std::fs::read_to_string(m.join("cached.txt")).unwrap()).await, "cached");
    drive.stop();
    drop(drive);

    // Again, with a server that cannot be reached.
    let dead = "http://127.0.0.1:9";
    let client = Client::new(ClientConfig::new(dead));
    client.set_tokens(server.access_token.clone(), server.refresh_token.clone()).await;
    let (drive, mnt) = mount(&setup, Arc::new(client), dead).await;
    let m = mnt.clone();
    // The first listing comes from the store at once; its refresh fails and the drive is offline.
    let started = Instant::now();
    let first = fs(move || std::fs::read_dir(&m).unwrap().count()).await;
    assert!(first >= 1);
    assert!(started.elapsed() < Duration::from_secs(2), "listing took {:?}", started.elapsed());
    wait_for("offline", || async { !drive.status().online }).await;

    let m = mnt.clone();
    let names: Vec<String> = fs(move || {
        std::fs::read_dir(&m).unwrap().map(|e| e.unwrap().file_name().to_string_lossy().to_string()).collect()
    })
    .await;
    assert_eq!(names, vec!["cached.txt".to_string()], "offline only the cached file is shown");
    let m = mnt.clone();
    assert_eq!(fs(move || std::fs::read_to_string(m.join("cached.txt")).unwrap()).await, "cached");
    let m = mnt.clone();
    let started = Instant::now();
    let missing = fs(move || std::fs::read_to_string(m.join("remote.txt"))).await;
    assert!(missing.is_err());
    assert!(started.elapsed() < Duration::from_secs(2));

    // An edit offline waits; after removal it is kept for the user.
    let m = mnt.clone();
    fs(move || std::fs::write(m.join("cached.txt"), "edited offline").unwrap()).await;
    let m = mnt.clone();
    let mkdir = fs(move || std::fs::create_dir(m.join("x"))).await;
    assert_eq!(mkdir.unwrap_err().raw_os_error(), Some(libc::ENETUNREACH));
    tokio::time::sleep(Duration::from_secs(3)).await;
    assert_eq!(drive.status().changes_waiting, 1);
    drive.stop();
    let kept = setup.home.path().join("kept");
    assert_eq!(drive.export_unsent(&kept).unwrap(), 1);
    assert_eq!(std::fs::read_to_string(kept.join("cached.txt")).unwrap(), "edited offline");
}

/// Not a test: mounts a drive for checks by hand (`kill -9`, file managers) until the file
/// `$FYLVO_E2E_STOP` exists. Mount point: `$FYLVO_E2E_MOUNT`.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore]
async fn manual_mount() {
    let mount_point = PathBuf::from(std::env::var("FYLVO_E2E_MOUNT").expect("FYLVO_E2E_MOUNT"));
    let stop = PathBuf::from(std::env::var("FYLVO_E2E_STOP").expect("FYLVO_E2E_STOP"));
    let server = Server::connect().await;
    let root = std::env::var("FYLVO_E2E_ROOT").unwrap_or_else(|_| "cloudreve://my".to_string());
    let data = std::env::var("FYLVO_E2E_DATA").map(PathBuf::from).unwrap_or_else(|_| std::env::temp_dir().join("fylvo-manual"));
    std::fs::create_dir_all(&data).unwrap();
    // SAFETY: runs alone.
    unsafe { std::env::set_var("HOME", &data) };
    let inventory = Arc::new(InventoryDb::with_path(data.join("meta.db")).unwrap());
    let drive = VirtualDrive::start("manual", &mount_point, &root, &server.url, server.client.clone(), inventory)
        .await
        .expect("mount");
    println!("mounted at {}", mount_point.display());
    while !stop.exists() {
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    drive.stop();
}
