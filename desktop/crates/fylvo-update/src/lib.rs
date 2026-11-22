//! Fylvo Desktop: updates from the app.
//!
//! The general part, shared by all platforms: ask the user's own Fylvo server for the newest
//! release of this platform, download it and check size and checksum. Installing is per
//! platform (`windows.rs`). Server side: `service/fylvo/fylvo_desktop_release.go`, routes
//! `GET /api/v4/fylvo/desktop/<platform>/latest` and `.../package/<sha256>`.

use anyhow::{anyhow, bail, Context, Result};
use futures::StreamExt;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::cmp::Ordering;
use std::fmt;
use std::path::{Path, PathBuf};
use std::time::Duration;
use tokio::io::AsyncWriteExt;
use url::{Host, Url};

pub mod state;
#[cfg(windows)]
pub mod windows;

/// The largest package the app accepts, whatever the server says (the app is about 12 MB).
pub const MAX_PACKAGE_SIZE: u64 = 500 << 20;

/// What the app asks the server for: one package format of one operating system and processor.
/// The server knows these keys: windows-x64, windows-arm64, linux-x64-appimage, linux-x64-rpm,
/// linux-x64-deb, linux-arm64-appimage, linux-arm64-rpm, linux-arm64-deb. An app asks only for
/// the format it was installed as.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Platform {
    /// The key in the server's routes.
    pub key: &'static str,
    /// File extension of the downloaded package.
    pub ext: &'static str,
    /// Public ed25519 key the release must be signed with (see `release_message`). `None`: the
    /// operating system checks the package itself (Windows: the signed MSIX).
    pub release_key: Option<&'static [u8; 32]>,
}

pub const WINDOWS_X64: Platform = Platform { key: "windows-x64", ext: "msix", release_key: None };
pub const WINDOWS_ARM64: Platform = Platform { key: "windows-arm64", ext: "msix", release_key: None };

/// Public ed25519 key of the Linux releases (the private key stays with the maintainer, see
/// `scripts/publish-desktop.sh`). Linux has no system check like Windows' signed MSIX.
pub const LINUX_RELEASE_KEY: [u8; 32] = [
    0xef, 0x41, 0xa2, 0xfc, 0xf2, 0x47, 0xe1, 0x96, 0x1e, 0x57, 0x10, 0x8e, 0x53, 0x2b, 0x96, 0x5f,
    0xb5, 0x12, 0xc4, 0x74, 0x23, 0x13, 0xc8, 0xbf, 0xe7, 0x18, 0xd5, 0xc4, 0xb9, 0x57, 0x43, 0xb7,
];

/// How the Linux app was installed: the format decides how an update is installed.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LinuxFormat {
    AppImage,
    Rpm,
    Deb,
}

/// The update platform of a Linux app of this processor in this format.
pub fn linux_platform(format: LinuxFormat) -> Option<Platform> {
    let key = match (cfg!(target_arch = "x86_64"), cfg!(target_arch = "aarch64"), format) {
        (true, _, LinuxFormat::AppImage) => "linux-x64-appimage",
        (true, _, LinuxFormat::Rpm) => "linux-x64-rpm",
        (true, _, LinuxFormat::Deb) => "linux-x64-deb",
        (_, true, LinuxFormat::AppImage) => "linux-arm64-appimage",
        (_, true, LinuxFormat::Rpm) => "linux-arm64-rpm",
        (_, true, LinuxFormat::Deb) => "linux-arm64-deb",
        _ => return None,
    };
    let ext = match format {
        LinuxFormat::AppImage => "AppImage",
        LinuxFormat::Rpm => "rpm",
        LinuxFormat::Deb => "deb",
    };
    Some(Platform { key, ext, release_key: Some(&LINUX_RELEASE_KEY) })
}

/// The platform of this Windows build; `None` elsewhere (Linux picks its format at run time).
pub fn windows_platform() -> Option<Platform> {
    if cfg!(all(windows, target_arch = "x86_64")) {
        Some(WINDOWS_X64)
    } else if cfg!(all(windows, target_arch = "aarch64")) {
        Some(WINDOWS_ARM64)
    } else {
        None
    }
}

/// Extensions of downloaded packages, for cleaning up.
const PACKAGE_EXTS: [&str; 4] = ["msix", "AppImage", "rpm", "deb"];

/// The text a release signature covers (ed25519, by the publish script), lines ending in LF:
/// "fylvo-desktop-release", the platform key, the version as a.b.c.d, the sha256, the size.
pub fn release_message(platform: &Platform, version: Version, sha256: &str, size: u64) -> String {
    format!("fylvo-desktop-release\n{}\n{version}\n{sha256}\n{size}\n", platform.key)
}

/// A version with up to four parts of 0 to 65535 (like a Windows package version). Missing
/// parts count as 0, so "0.2.2" equals "0.2.2.0".
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct Version(pub [u16; 4]);

impl Version {
    pub fn parse(text: &str) -> Option<Version> {
        let parts: Vec<&str> = text.split('.').collect();
        if parts.is_empty() || parts.len() > 4 {
            return None;
        }
        let mut v = [0u16; 4];
        for (i, p) in parts.iter().enumerate() {
            if p.is_empty() || p.len() > 5 || !p.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            v[i] = p.parse().ok()?;
        }
        Some(Version(v))
    }
}

impl fmt::Display for Version {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let [a, b, c, d] = self.0;
        write!(f, "{a}.{b}.{c}.{d}")
    }
}

/// The published release of this platform, already checked.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Release {
    pub version: Version,
    pub sha256: String,
    pub size: u64,
}

#[derive(Deserialize)]
struct RawRelease {
    version: String,
    sha256: String,
    size: u64,
    /// Base64 ed25519 signature over `release_message`; required when the platform has a key.
    #[serde(default)]
    signature: Option<String>,
}

impl RawRelease {
    fn check(self, platform: &Platform) -> Result<Release> {
        let version = Version::parse(&self.version)
            .ok_or_else(|| anyhow!("the server names an invalid version"))?;
        if self.sha256.len() != 64 || !self.sha256.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f')) {
            bail!("the server names an invalid checksum");
        }
        if self.size == 0 || self.size > MAX_PACKAGE_SIZE {
            bail!("the server names an invalid size ({} bytes)", self.size);
        }
        if let Some(key) = platform.release_key {
            verify_signature(key, &release_message(platform, version, &self.sha256, self.size), self.signature.as_deref())?;
        }
        Ok(Release { version, sha256: self.sha256, size: self.size })
    }
}

/// Checks the release signature against the key built into the app.
fn verify_signature(key: &[u8; 32], message: &str, signature: Option<&str>) -> Result<()> {
    use base64::Engine;
    use ed25519_dalek::{Signature, VerifyingKey};
    let signature = signature.ok_or_else(|| anyhow!("the release is not signed"))?;
    let raw = base64::engine::general_purpose::STANDARD
        .decode(signature)
        .map_err(|_| anyhow!("the release signature is not valid"))?;
    let signature = Signature::from_slice(&raw).map_err(|_| anyhow!("the release signature is not valid"))?;
    let key = VerifyingKey::from_bytes(key).map_err(|_| anyhow!("the built-in release key is not valid"))?;
    key.verify_strict(message.as_bytes(), &signature)
        .map_err(|_| anyhow!("the release signature does not match"))
}

#[derive(Deserialize)]
struct ApiAnswer {
    code: i64,
    #[serde(default)]
    msg: String,
    data: Option<RawRelease>,
}

/// Cloudreve's code for "not found": nothing is published for this platform.
const CODE_NOT_FOUND: i64 = 404;

/// Where and as whom to ask: a signed-in drive's server and a fresh access token.
#[derive(Clone)]
pub struct Server {
    pub base_url: String,
    pub access_token: String,
}

impl Server {
    /// The URL of an API path, but only over https, or http to this computer (local tests).
    fn api_url(&self, path: &str) -> Result<Url> {
        let base = Url::parse(self.base_url.trim_end_matches('/')).context("invalid server address")?;
        if !allowed_base(&base) {
            bail!("updates are only loaded over https");
        }
        Url::parse(&format!("{}/api/v4/fylvo/desktop/{path}", base.as_str().trim_end_matches('/')))
            .context("invalid server address")
    }
}

/// https, or http only to a loopback address.
fn allowed_base(url: &Url) -> bool {
    match url.scheme() {
        "https" => true,
        "http" => match url.host() {
            Some(Host::Domain(d)) => d.eq_ignore_ascii_case("localhost"),
            Some(Host::Ipv4(ip)) => ip.is_loopback(),
            Some(Host::Ipv6(ip)) => ip.is_loopback(),
            None => false,
        },
        _ => false,
    }
}

/// The HTTP client for updates: no redirects (a redirect could leave https).
pub fn http_client(user_agent: &str) -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(30))
        .read_timeout(Duration::from_secs(60))
        .user_agent(user_agent)
        .build()
        .context("failed to create the HTTP client")
}

/// Asks the server for the published release of this platform. `None`: nothing published.
pub async fn latest(http: &reqwest::Client, server: &Server, platform: &Platform) -> Result<Option<Release>> {
    let url = server.api_url(&format!("{}/latest", platform.key))?;
    let answer = http
        .get(url)
        .bearer_auth(&server.access_token)
        .send()
        .await
        .context("the server is not reachable")?;
    if !answer.status().is_success() {
        bail!("the server answered {}", answer.status());
    }
    let answer: ApiAnswer = answer.json().await.context("unexpected answer from the server")?;
    match answer.code {
        0 => answer
            .data
            .ok_or_else(|| anyhow!("the server sent no release"))?
            .check(platform)
            .map(Some),
        CODE_NOT_FOUND => Ok(None),
        code => bail!("the server answered with code {code}: {}", answer.msg),
    }
}

/// What a download ended with.
#[derive(Debug)]
pub enum Downloaded {
    /// The checked package, `<dir>/<sha256>.<ext>`.
    File(PathBuf),
    /// The server no longer has this release (a newer one was published meanwhile).
    Gone,
}

/// Downloads the package of `release` into `dir`. The file only gets its final name when size
/// and checksum match; otherwise nothing is left behind.
pub async fn download(
    http: &reqwest::Client,
    server: &Server,
    platform: &Platform,
    release: &Release,
    dir: &Path,
) -> Result<Downloaded> {
    let url = server.api_url(&format!("{}/package/{}", platform.key, release.sha256))?;
    tokio::fs::create_dir_all(dir).await.context("failed to create the update folder")?;
    // The checksum was checked to be 64 hex characters, so the names stay inside dir.
    let part = dir.join(format!("{}.part", release.sha256));
    let target = dir.join(format!("{}.{}", release.sha256, platform.ext));

    let answer = http
        .get(url)
        .bearer_auth(&server.access_token)
        .send()
        .await
        .context("the server is not reachable")?;
    if !answer.status().is_success() {
        bail!("the server answered {}", answer.status());
    }
    // A JSON answer is the server's error ("not found" when the release changed).
    let is_json = answer
        .headers()
        .get(reqwest::header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .is_some_and(|v| v.starts_with("application/json"));
    if is_json {
        let text = answer.text().await.unwrap_or_default();
        return match serde_json::from_str::<ApiAnswer>(&text) {
            Ok(a) if a.code == CODE_NOT_FOUND => Ok(Downloaded::Gone),
            Ok(a) => bail!("the server answered with code {}: {}", a.code, a.msg),
            Err(_) => bail!("unexpected answer from the server"),
        };
    }

    let result = write_checked(answer, &part, release).await;
    if let Err(e) = result {
        let _ = tokio::fs::remove_file(&part).await;
        return Err(e);
    }
    tokio::fs::rename(&part, &target).await.context("failed to keep the downloaded update")?;
    Ok(Downloaded::File(target))
}

async fn write_checked(answer: reqwest::Response, part: &Path, release: &Release) -> Result<()> {
    let mut file = tokio::fs::File::create(part).await.context("failed to save the update")?;
    let mut hasher = Sha256::new();
    let mut written: u64 = 0;
    let mut body = answer.bytes_stream();
    while let Some(chunk) = body.next().await {
        let chunk = chunk.context("the download was interrupted")?;
        written += chunk.len() as u64;
        if written > release.size {
            bail!("the download is larger than announced");
        }
        hasher.update(&chunk);
        file.write_all(&chunk).await.context("failed to save the update")?;
    }
    file.flush().await.context("failed to save the update")?;
    file.sync_all().await.context("failed to save the update")?;
    drop(file);
    if written != release.size {
        bail!("the download is incomplete ({written} of {} bytes)", release.size);
    }
    let sum = format!("{:x}", hasher.finalize());
    if sum != release.sha256 {
        bail!("the checksum of the download does not match");
    }
    Ok(())
}

/// Removes downloaded packages and unfinished downloads; the state file stays.
pub fn clean_downloads(dir: &Path) {
    let Ok(entries) = std::fs::read_dir(dir) else { return };
    for entry in entries.flatten() {
        let path = entry.path();
        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        if (ext == "part" || PACKAGE_EXTS.contains(&ext)) && path.is_file() {
            let _ = std::fs::remove_file(&path);
        }
    }
}

/// True if `candidate` is newer than `running`.
pub fn is_newer(candidate: Version, running: Version) -> bool {
    candidate.cmp(&running) == Ordering::Greater
}

#[cfg(test)]
mod tests;
