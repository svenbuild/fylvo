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

struct Server {
    client: Arc<Client>,
    inventory: Arc<InventoryDb>,
    dir: tempfile::TempDir,
    root: String,
}

impl Server {
    async fn connect() -> Self {
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
        let client = Client::new(ClientConfig::new(url));
        client
            .set_tokens(
                token["access_token"].as_str().expect("access token").to_string(),
                token["refresh_token"].as_str().expect("refresh token").to_string(),
            )
            .await;
        let dir = tempfile::tempdir().unwrap();
        let inventory = Arc::new(InventoryDb::with_path(dir.path().join("meta.db")).unwrap());
        let root = format!("cloudreve://my/e2e-{}", uuid::Uuid::new_v4().simple());
        Self {
            client: Arc::new(client),
            inventory,
            dir,
            root,
        }
    }

    fn uri(&self, relative: &str) -> String {
        format!("{}/{}", self.root, relative)
    }

    async fn folder(&self, relative: &str) -> Result<FileResponse> {
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
    async fn upload(
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

    async fn info(&self, relative: &str) -> FileResponse {
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

    async fn rename(&self, relative: &str, new_name: &str) -> Result<()> {
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
