//! Fylvo: the upload path of the app against a real Fylvo server (not the NAS, not production).
//! A full sync folder needs the installed app (package identity), so this runs what the sync
//! sends to the server: the uploader with `previous` and `entity_type`, renames and folders,
//! and the app's reading of the answers.
//!
//! Ignored by default. Start a local server with its own data folder and port, sign up a user,
//! then: `FYLVO_E2E_URL=http://127.0.0.1:5291 FYLVO_E2E_EMAIL=... FYLVO_E2E_PASSWORD=...
//! cargo test -p cloudreve-sync server_e2e -- --ignored --test-threads=1`

use crate::drive::case::is_name_case_taken;
use crate::inventory::InventoryDb;
use crate::tasks::upload::{is_conflict_error, upload_is_current};
use crate::uploader::{ProgressCallback, ProgressUpdate, UploadParams, Uploader, UploaderConfig};
use anyhow::Result;
use cloudreve_api::{
    Client, ClientConfig,
    api::ExplorerApi,
    models::explorer::{CreateFileService, FileResponse, GetFileInfoService, RenameFileService},
};
use std::sync::Arc;

struct NoProgress;

impl ProgressCallback for NoProgress {
    fn on_progress(&self, _update: ProgressUpdate) {}
}

pub(super) struct Server {
    pub(super) client: Arc<Client>,
    inventory: Arc<InventoryDb>,
    dir: tempfile::TempDir,
    pub(super) root: String,
    pub(super) url: String,
    pub(super) access_token: String,
    #[allow(dead_code)]
    pub(super) refresh_token: String,
}

impl Server {
    pub(super) async fn connect() -> Self {
        let var = |name| std::env::var(name).unwrap_or_else(|_| panic!("{name} is not set"));
        // A password sign-in as the web app does it (the app itself signs in through the
        // browser, `fylvo://`).
        let url = var("FYLVO_E2E_URL");
        let login: serde_json::Value = reqwest::Client::new()
            .post(format!("{url}/api/v4/session/token"))
            .json(&serde_json::json!({
                "email": var("FYLVO_E2E_EMAIL"),
                "password": var("FYLVO_E2E_PASSWORD"),
            }))
            .send()
            .await
            .expect("login")
            .json()
            .await
            .expect("login answer");
        let token = &login["data"]["token"];
        let access_token = token["access_token"].as_str().expect("access token").to_string();
        let refresh_token = token["refresh_token"].as_str().expect("refresh token").to_string();
        let client = Client::new(ClientConfig::new(url.clone()));
        client
            .set_tokens(access_token.clone(), refresh_token.clone())
            .await;
        let dir = tempfile::tempdir().unwrap();
        let inventory = Arc::new(InventoryDb::with_path(dir.path().join("meta.db")).unwrap());
        let root = format!("cloudreve://my/e2e-{}", uuid::Uuid::new_v4().simple());
        Self {
            client: Arc::new(client),
            inventory,
            dir,
            root,
            url,
            access_token,
            refresh_token,
        }
    }

    /// Tokens as Fylvo Desktop gets them: the web app's consent for the desktop client (with
    /// PKCE), then the code exchange. Password tokens carry no scopes, the app refuses them.
    pub(super) async fn desktop_tokens(&self) -> (String, String) {
        use base64::Engine;
        use sha2::Digest;
        const CLIENT_ID: &str = "512cd652-c028-4332-992e-d88a4406e5b2";
        const CLIENT_SECRET: &str = "ZO3QjjTTSLiRjaw8ixyryEdJmTnmhtCj";
        const SCOPES: &str = "profile email openid offline_access UserInfo.Write Workflow.Write Files.Write Shares.Write";
        const CALLBACK: &str = "/callback/fylvo-desktop";
        let verifier = format!("{}{}", uuid::Uuid::new_v4().simple(), uuid::Uuid::new_v4().simple());
        let challenge = base64::engine::general_purpose::URL_SAFE_NO_PAD
            .encode(sha2::Sha256::digest(verifier.as_bytes()));
        let http = reqwest::Client::new();
        let consent: serde_json::Value = http
            .post(format!("{}/api/v4/session/oauth/consent", self.url))
            .bearer_auth(&self.access_token)
            .json(&serde_json::json!({
                "client_id": CLIENT_ID,
                "response_type": "code",
                "redirect_uri": CALLBACK,
                "state": "e2e",
                "scope": SCOPES,
                "code_challenge": challenge,
                "code_challenge_method": "S256",
            }))
            .send()
            .await
            .expect("consent")
            .json()
            .await
            .expect("consent answer");
        let code = consent["data"]["code"]
            .as_str()
            .unwrap_or_else(|| panic!("no code in {consent}"))
            .to_string();
        let token: serde_json::Value = http
            .post(format!("{}/api/v4/session/oauth/token", self.url))
            .form(&[
                ("grant_type", "authorization_code"),
                ("client_id", CLIENT_ID),
                ("client_secret", CLIENT_SECRET),
                ("code", code.as_str()),
                ("redirect_uri", CALLBACK),
                ("code_verifier", verifier.as_str()),
            ])
            .send()
            .await
            .expect("token")
            .json()
            .await
            .expect("token answer");
        let access = token["access_token"]
            .as_str()
            .unwrap_or_else(|| panic!("no access token in {token}"))
            .to_string();
        let refresh = token["refresh_token"].as_str().unwrap_or_default().to_string();
        (access, refresh)
    }

    pub(super) fn uri(&self, relative: &str) -> String {
        format!("{}/{}", self.root, relative)
    }

    pub(super) async fn folder(&self, relative: &str) -> Result<FileResponse> {
        Ok(self
            .client
            .create_file(&CreateFileService {
                uri: self.uri(relative),
                file_type: "folder".to_string(),
                err_on_conflict: Some(false),
                metadata: None,
            })
            .await?)
    }

    /// Upload `content` as the sync does: a known file names the version it was changed from
    /// (`previous`) and makes a new version of it. Returns the version the upload created.
    pub(super) async fn upload(
        &self,
        relative: &str,
        content: &str,
        previous: Option<&str>,
    ) -> Result<Option<String>> {
        let local = self.dir.path().join(uuid::Uuid::new_v4().simple().to_string());
        std::fs::write(&local, content)?;
        let params = UploadParams {
            local_path: local,
            remote_uri: self.uri(relative),
            file_size: content.len() as u64,
            mime_type: None,
            last_modified: None,
            overwrite: previous.is_some(),
            previous_version: previous.unwrap_or_default().to_string(),
            task_id: uuid::Uuid::new_v4().to_string(),
            drive_id: uuid::Uuid::nil().to_string(),
        };
        let uploader = Uploader::new(
            self.client.clone(),
            self.inventory.clone(),
            UploaderConfig::default(),
        );
        uploader.upload(params, NoProgress).await
    }

    pub(super) async fn info(&self, relative: &str) -> FileResponse {
        self.client
            .get_file_info(&GetFileInfoService {
                uri: Some(self.uri(relative)),
                id: None,
                extended: None,
                folder_summary: None,
            })
            .await
            .expect("file info")
    }

    pub(super) async fn rename(&self, relative: &str, new_name: &str) -> Result<()> {
        self.client
            .rename_file(&RenameFileService {
                uri: self.uri(relative),
                new_name: new_name.to_string(),
            })
            .await?;
        Ok(())
    }
}

#[tokio::test]
#[ignore = "needs a local Fylvo server, see the module comment"]
async fn uploads_name_the_version_they_create_and_replace_only_the_version_they_saw() {
    let server = Server::connect().await;
    server.folder("Docs").await.unwrap();

    // A new file: the created version is the current one.
    let first = server.upload("Docs/b.txt", "one", None).await.unwrap();
    assert!(first.is_some(), "the server names the created version");
    assert!(upload_is_current(first.as_deref(), &server.info("Docs/b.txt").await));

    // A change of the version this PC has: replaces it.
    let second = server
        .upload("Docs/b.txt", "two", first.as_deref())
        .await
        .unwrap();
    assert!(upload_is_current(second.as_deref(), &server.info("Docs/b.txt").await));

    // Another device saved meanwhile: a change of the old version does not replace it, the
    // app takes it as a conflict.
    let stale = server
        .upload("Docs/b.txt", "three", first.as_deref())
        .await
        .unwrap_err();
    assert!(is_conflict_error(&stale), "{stale:?}");
    assert_eq!(
        server.info("Docs/b.txt").await.primary_entity,
        second,
        "the other device's version stays current"
    );

    // Another device saves right after an upload of this PC: its version is not taken for the
    // content of this PC's file.
    let mine = server
        .upload("Docs/b.txt", "four", second.as_deref())
        .await
        .unwrap();
    server
        .upload("Docs/b.txt", "five", mine.as_deref())
        .await
        .unwrap();
    assert!(!upload_is_current(mine.as_deref(), &server.info("Docs/b.txt").await));
}

#[tokio::test]
#[ignore = "needs a local Fylvo server, see the module comment"]
async fn names_in_other_case_are_refused_with_40905_and_read_as_such() {
    let server = Server::connect().await;
    server.folder("Docs").await.unwrap();
    let version = server.upload("Docs/b.txt", "one", None).await.unwrap();

    // An upload queued before the server renamed `docs` to `Docs` (the race of the review):
    // the server does not create a second folder `docs`.
    let err = server.upload("docs/c.txt", "new", None).await.unwrap_err();
    assert!(is_name_case_taken(&err), "{err:?}");
    assert!(!is_conflict_error(&err), "not a conflict: nothing to choose");
    // A change of `b.txt` queued before the server renamed it to `B.txt`.
    server.rename("Docs/b.txt", "B.txt").await.unwrap();
    let err = server
        .upload("Docs/b.txt", "two", version.as_deref())
        .await
        .unwrap_err();
    assert!(is_name_case_taken(&err), "{err:?}");
    // A new folder in other case.
    let err = server.folder("docs").await.unwrap_err();
    assert!(is_name_case_taken(&err), "{err:?}");
    // A rename from the Explorer to a name that is there in other case.
    server.upload("Docs/x.txt", "x", None).await.unwrap();
    let err = server.rename("Docs/x.txt", "b.TXT").await.unwrap_err();
    assert!(is_name_case_taken(&err), "{err:?}");

    // What is there is unchanged: one folder, `B.txt` with its version, `x.txt`.
    let info = server.info("Docs/B.txt").await;
    assert_eq!(info.primary_entity, version);
    assert_eq!(info.name, "B.txt");
    server.info("Docs/x.txt").await;
    // After the rename the change goes up under the server's spelling.
    let after = server
        .upload("Docs/B.txt", "two", version.as_deref())
        .await
        .unwrap();
    assert!(upload_is_current(after.as_deref(), &server.info("Docs/B.txt").await));
}

#[tokio::test]
#[ignore = "needs a local Fylvo server, see the module comment"]
async fn a_file_saved_by_replacement_stays_the_same_file_on_the_server() {
    let server = Server::connect().await;
    server.folder("Docs").await.unwrap();
    let first = server.upload("Docs/Book.xlsx", "one", None).await.unwrap();
    let before = server.info("Docs/Book.xlsx").await;

    // Excel deleted and wrote the file again; the inventory row stayed, so the app uploads
    // a new version that names the one it replaces.
    let second = server
        .upload("Docs/Book.xlsx", "two", first.as_deref())
        .await
        .unwrap();
    let after = server.info("Docs/Book.xlsx").await;
    assert_eq!(after.id, before.id, "same file: versions and shares stay with it");
    assert_eq!(after.primary_entity, second);
    assert_ne!(after.primary_entity, before.primary_entity);
}
