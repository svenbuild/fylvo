use super::*;
use crate::state::{State, StartOutcome};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

fn v(text: &str) -> Version {
    Version::parse(text).unwrap()
}

#[test]
fn versions_parse_and_compare_part_by_part() {
    assert_eq!(v("0.2.2"), v("0.2.2.0"));
    assert!(v("0.10.0") > v("0.9.9.9"));
    assert!(is_newer(v("0.2.1"), v("0.2.0.9")));
    assert!(!is_newer(v("0.2.0"), v("0.2.0.0")));
    assert_eq!(v("65535.0.0.1").to_string(), "65535.0.0.1");
    for bad in ["", ".", "1.", "1..2", "1.2.3.4.5", "65536", "-1", "+1", " 1", "1.2a", "000001"] {
        assert!(Version::parse(bad).is_none(), "{bad:?} must not parse");
    }
}

#[test]
fn only_https_or_loopback_http() {
    for ok in ["https://cloud.example.net", "http://localhost:5290", "http://127.0.0.1:5290/", "http://[::1]:80"] {
        assert!(allowed_base(&Url::parse(ok).unwrap()), "{ok}");
    }
    for bad in ["http://cloud.example.net", "http://192.168.1.2", "ftp://localhost", "file:///c:/x", "http://localhost.evil.net"] {
        assert!(!allowed_base(&Url::parse(bad).unwrap()), "{bad}");
    }
    let server = Server { base_url: "http://cloud.example.net".into(), access_token: "t".into() };
    assert!(server.api_url("windows-x64/latest").is_err());
}

fn raw(version: &str, sha256: &str, size: u64) -> RawRelease {
    RawRelease { version: version.into(), sha256: sha256.into(), size, signature: None }
}

#[test]
fn release_fields_are_checked_before_use() {
    let hash = "a".repeat(64);
    assert!(raw("0.3.0", &hash, 10).check(&WINDOWS_X64).is_ok());
    assert!(raw("0.3.0", &"A".repeat(64), 10).check(&WINDOWS_X64).is_err(), "upper case hex");
    assert!(raw("0.3.0", &"a".repeat(63), 10).check(&WINDOWS_X64).is_err());
    assert!(raw("0.3.0", "../../../../etc/passwd", 10).check(&WINDOWS_X64).is_err());
    assert!(raw("0.3.0", &hash, 0).check(&WINDOWS_X64).is_err());
    assert!(raw("0.3.0", &hash, MAX_PACKAGE_SIZE + 1).check(&WINDOWS_X64).is_err());
    assert!(raw("0.3-beta", &hash, 10).check(&WINDOWS_X64).is_err());
}

/// A tiny HTTP server that answers every request with `answer` and returns its address.
async fn serve(answer: Vec<u8>) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        while let Ok((mut socket, _)) = listener.accept().await {
            let answer = answer.clone();
            tokio::spawn(async move {
                let mut buf = [0u8; 4096];
                let _ = socket.read(&mut buf).await;
                let _ = socket.write_all(&answer).await;
                let _ = socket.shutdown().await;
            });
        }
    });
    format!("http://{addr}")
}

fn http_answer(content_type: &str, body: &[u8], announced_len: usize) -> Vec<u8> {
    let mut a = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: {content_type}\r\nContent-Length: {announced_len}\r\nConnection: close\r\n\r\n"
    )
    .into_bytes();
    a.extend_from_slice(body);
    a
}

fn release_of(body: &[u8]) -> Release {
    Release { version: v("0.3.0"), sha256: format!("{:x}", Sha256::digest(body)), size: body.len() as u64 }
}

async fn try_download(answer: Vec<u8>, release: &Release) -> (Result<Downloaded>, tempfile::TempDir) {
    let base_url = serve(answer).await;
    let dir = tempfile::tempdir().unwrap();
    let server = Server { base_url, access_token: "token".into() };
    let http = http_client("test").unwrap();
    (download(&http, &server, &WINDOWS_X64, release, dir.path()).await, dir)
}

fn files_in(dir: &Path) -> Vec<String> {
    std::fs::read_dir(dir).unwrap().map(|e| e.unwrap().file_name().to_string_lossy().into_owned()).collect()
}

#[tokio::test]
async fn download_keeps_only_a_checked_file() {
    let body = b"package bytes".to_vec();
    let release = release_of(&body);
    let (result, dir) = try_download(http_answer("application/octet-stream", &body, body.len()), &release).await;
    match result.unwrap() {
        Downloaded::File(path) => {
            assert_eq!(std::fs::read(&path).unwrap(), body);
            assert_eq!(path.file_name().unwrap().to_string_lossy(), format!("{}.msix", release.sha256));
        }
        other => panic!("unexpected {other:?}"),
    }
    assert_eq!(files_in(dir.path()).len(), 1);
}

#[tokio::test]
async fn download_with_wrong_content_leaves_nothing() {
    let body = b"package bytes".to_vec();
    let release = release_of(&body);

    let mut other = body.clone();
    other[0] ^= 1;
    let (result, dir) = try_download(http_answer("application/octet-stream", &other, other.len()), &release).await;
    assert!(result.unwrap_err().to_string().contains("checksum"));
    assert!(files_in(dir.path()).is_empty());

    let longer = [body.clone(), b"x".to_vec()].concat();
    let (result, dir) = try_download(http_answer("application/octet-stream", &longer, longer.len()), &release).await;
    assert!(result.is_err());
    assert!(files_in(dir.path()).is_empty());

    // Cut off: the server announces the full length but closes early.
    let (result, dir) = try_download(http_answer("application/octet-stream", &body[..5], body.len()), &release).await;
    assert!(result.is_err());
    assert!(files_in(dir.path()).is_empty());
}

#[tokio::test]
async fn download_of_a_replaced_release_is_gone() {
    let release = release_of(b"old");
    let json = br#"{"code":404,"msg":"No desktop release is published","data":null}"#;
    let (result, dir) = try_download(http_answer("application/json; charset=utf-8", json, json.len()), &release).await;
    assert!(matches!(result.unwrap(), Downloaded::Gone));
    assert!(files_in(dir.path()).is_empty());
}

#[tokio::test]
async fn latest_reads_release_nothing_and_errors() {
    let http = http_client("test").unwrap();
    let hash = "b".repeat(64);
    let ok = format!(r#"{{"code":0,"data":{{"version":"0.3.0.0","sha256":"{hash}","size":12,"file_name":"F.msix"}}}}"#);
    let server = Server { base_url: serve(http_answer("application/json", ok.as_bytes(), ok.len())).await, access_token: "t".into() };
    assert_eq!(
        latest(&http, &server, &WINDOWS_X64).await.unwrap(),
        Some(Release { version: v("0.3.0"), sha256: hash, size: 12 })
    );

    let none = br#"{"code":404,"msg":"none","data":null}"#;
    let server = Server { base_url: serve(http_answer("application/json", none, none.len())).await, access_token: "t".into() };
    assert_eq!(latest(&http, &server, &WINDOWS_X64).await.unwrap(), None);

    let denied = br#"{"code":401,"msg":"Login required","data":null}"#;
    let server = Server { base_url: serve(http_answer("application/json", denied, denied.len())).await, access_token: "t".into() };
    assert!(latest(&http, &server, &WINDOWS_X64).await.is_err());
}

#[test]
fn redirects_are_not_followed() {
    // A redirect answer is not a success, so it ends as an error instead of a second request.
    let rt = tokio::runtime::Runtime::new().unwrap();
    rt.block_on(async {
        let answer = b"HTTP/1.1 302 Found\r\nLocation: http://example.net/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".to_vec();
        let server = Server { base_url: serve(answer).await, access_token: "t".into() };
        let err = latest(&http_client("test").unwrap(), &server, &WINDOWS_X64).await.unwrap_err();
        assert!(err.to_string().contains("302"), "{err}");
    });
}

#[test]
fn clean_downloads_keeps_the_state_file() {
    let dir = tempfile::tempdir().unwrap();
    for name in ["a.part", "b.msix", "c.AppImage", "d.rpm", "e.deb", "state.json", "notes.txt"] {
        std::fs::write(dir.path().join(name), b"x").unwrap();
    }
    clean_downloads(dir.path());
    let mut left = files_in(dir.path());
    left.sort();
    assert_eq!(left, vec!["notes.txt", "state.json"]);
}

#[test]
fn start_tells_success_from_failure_and_blocks_a_failed_version() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = State::default();
    s.installing = Some(state::Installing { version: v("0.3.0"), from: v("0.2.0") });
    s.save(dir.path()).unwrap();

    // Still the old version after the restart: failed, blocked for a day.
    let mut loaded = State::load(dir.path());
    assert_eq!(loaded.on_start(v("0.2.0"), 1000), StartOutcome::Failed { version: v("0.3.0") });
    assert!(loaded.blocked(v("0.3.0"), 1000 + 60));
    assert!(!loaded.blocked(v("0.3.1"), 1000 + 60));
    assert!(!loaded.blocked(v("0.3.0"), 1000 + state::RETRY_FAILED_AFTER_SECS));
    loaded.save(dir.path()).unwrap();
    assert_eq!(State::load(dir.path()).on_start(v("0.2.0"), 2000), StartOutcome::Nothing);
    assert!(State::load(dir.path()).blocked(v("0.3.0"), 2000), "the block survives a restart");

    // The new version runs: updated, the block is gone.
    let mut s = State::load(dir.path());
    s.installing = Some(state::Installing { version: v("0.3.0"), from: v("0.2.0") });
    assert_eq!(s.on_start(v("0.3.0"), 3000), StartOutcome::Updated { from: v("0.2.0"), to: v("0.3.0") });
    assert!(s.failed.is_none());

    // A broken file is an empty state.
    std::fs::write(dir.path().join("state.json"), b"{broken").unwrap();
    assert_eq!(State::load(dir.path()), State::default());
}

#[test]
fn a_platform_with_a_key_needs_a_matching_signature() {
    use base64::Engine;
    use ed25519_dalek::{Signer, SigningKey};
    let signing = SigningKey::from_bytes(&[7u8; 32]);
    static KEY: std::sync::OnceLock<[u8; 32]> = std::sync::OnceLock::new();
    let key = KEY.get_or_init(|| signing.verifying_key().to_bytes());
    let platform = Platform { key: "linux-x64-rpm", ext: "rpm", release_key: Some(key) };
    let hash = "c".repeat(64);
    let sign = |p: &Platform, version: &str, size: u64| {
        let msg = release_message(p, v(version), &hash, size);
        base64::engine::general_purpose::STANDARD.encode(signing.sign(msg.as_bytes()).to_bytes())
    };
    let signed = |signature: Option<String>| RawRelease { version: "0.3.0".into(), sha256: hash.clone(), size: 10, signature };

    assert!(signed(Some(sign(&platform, "0.3.0", 10))).check(&platform).is_ok());
    assert!(signed(None).check(&platform).is_err(), "unsigned");
    assert!(signed(Some(sign(&platform, "0.3.1", 10))).check(&platform).is_err(), "other version");
    assert!(signed(Some(sign(&platform, "0.3.0", 11))).check(&platform).is_err(), "other size");
    let deb = Platform { key: "linux-x64-deb", ..platform };
    assert!(signed(Some(sign(&deb, "0.3.0", 10))).check(&platform).is_err(), "signed for another format");
    assert!(signed(Some("not base64!".into())).check(&platform).is_err());
    // Windows has no key: the operating system checks the signed MSIX.
    assert!(signed(None).check(&WINDOWS_X64).is_ok());
    assert_eq!(
        release_message(&platform, v("0.3.0"), &hash, 10),
        format!("fylvo-desktop-release\nlinux-x64-rpm\n0.3.0.0\n{hash}\n10\n")
    );
}
