package io.github.svenbuild.fylvo.android.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.Transient

@Serializable
data class ApiResponse<T>(
    val code: Int = 0,
    val msg: String = "",
    val data: T? = null,
    /** Fylvo: codes of the single failures of a batch answer (40081), see ApiEnvelope. */
    @Transient val innerCodes: List<Int> = emptyList(),
)

@Serializable
data class Token(
    @SerialName("access_token") val accessToken: String = "",
    @SerialName("refresh_token") val refreshToken: String = "",
    @SerialName("access_expires") val accessExpires: String = "",
    @SerialName("refresh_expires") val refreshExpires: String = "",
)

@Serializable
data class User(
    val id: String = "",
    val email: String = "",
    val nickname: String = "",
    @SerialName("user_name") val userName: String = "",
    val status: String = "",
    val avatar: String = "",
)

@Serializable
data class LoginResponse(
    val user: User = User(),
    val token: Token = Token(),
)

@Serializable
data class PasswordLoginRequest(
    val email: String,
    val password: String,
)

/**
 * Second step of a password login when the account has two-factor auth enabled.
 * [sessionId] is the value the server returned in `data` alongside code 203.
 */
@Serializable
data class OtpLoginRequest(
    val otp: String,
    @SerialName("session_id") val sessionId: String,
)

/** Fylvo: the new password with the ticket of a sign-in that needs a password change. */
@Serializable
data class NewPasswordRequest(
    @SerialName("session_id") val sessionId: String,
    val password: String,
)

@Serializable
data class RefreshTokenRequest(
    @SerialName("refresh_token") val refreshToken: String,
)

@Serializable
data class RefreshTokenResponse(
    @SerialName("access_token") val accessToken: String = "",
    @SerialName("refresh_token") val refreshToken: String = "",
    @SerialName("access_expires") val accessExpires: String = "",
    @SerialName("refresh_expires") val refreshExpires: String = "",
)

@Serializable
data class FileObject(
    val type: Int = 0,
    val id: String = "",
    val name: String = "",
    val permission: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val size: Long = 0,
    val metadata: Map<String, String>? = null,
    val path: String = "",
    val shared: Boolean? = null,
    /** Fylvo: base64 boolset of what the user may do here; decides inside shares (util/Permissions). */
    val capability: String? = null,
    /** True when the signed-in user owns the file. */
    val owned: Boolean? = null,
    /** Fylvo: id of the file's current version (entity); changes with every new version. */
    @SerialName("primary_entity") val primaryEntity: String = "",
) {
    val isFolder: Boolean get() = type == 1

    /** Fylvo: a "Shared with me" entry points to its share with this URI (`cloudreve://<id>@share`). */
    val sharedRedirect: String? get() = metadata?.get(METADATA_SHARED_REDIRECT)

    /**
     * The list API carries no per-file thumbnail flag. Like the web UI, ask
     * for a thumbnail for every file the server has not yet marked with
     * `thumb:disabled` (set after the first failed generation attempt).
     */
    val mayHaveThumbnail: Boolean
        get() = !isFolder && !isUploading && metadata?.containsKey(METADATA_THUMB_DISABLED) != true

    /** Where a trashed file came from; only set on items listed under cloudreve://trash. */
    val restoreUri: String? get() = metadata?.get(METADATA_RESTORE_URI)

    /**
     * Fylvo: an upload into this file has not finished; the server holds a lock on it until the
     * upload ends or is cleaned up. Same test as the web UI (GridFile.tsx, Metadata.upload_session_id).
     */
    val isUploading: Boolean get() = !isFolder && metadata?.containsKey(METADATA_UPLOAD_SESSION_ID) == true

    companion object {
        const val METADATA_THUMB_DISABLED = "thumb:disabled"
        const val METADATA_RESTORE_URI = "sys:restore_uri"
        const val METADATA_SHARED_REDIRECT = "sys:shared_redirect"
        /** Set while an upload into this file is not finished (pkg/filemanager/fs/dbfs/file.go). */
        const val METADATA_UPLOAD_SESSION_ID = "sys:upload_session_id"
    }
}

@Serializable
data class Pagination(
    @SerialName("next_token") val nextToken: String? = null,
    @SerialName("page_size") val pageSize: Int = 0,
    @SerialName("is_cursor") val isCursor: Boolean = false,
)

@Serializable
data class ListFileResponse(
    val files: List<FileObject> = emptyList(),
    val pagination: Pagination = Pagination(),
    @SerialName("storage_policy") val storagePolicy: StoragePolicy? = null,
    val parent: FileObject? = null,
    /** Fylvo: a subtree search stopped early (too many folders); the result is incomplete. */
    @SerialName("recursion_limit_reached") val recursionLimitReached: Boolean = false,
)

@Serializable
data class StoragePolicy(
    val id: String = "",
    val name: String = "",
    val type: String = "",
)

@Serializable
data class FileUrlRequest(
    val uris: List<String>,
    val download: Boolean = false,
    /** Server-side zip of a folder; the returned URL points at the archive. */
    val archive: Boolean = false,
    /** Fylvo: this exact version (entity id) of a single file; empty = the current one. */
    val entity: String = "",
)

@Serializable
data class CapacityResponse(
    val used: Long = 0,
    val total: Long = 0,
)

@Serializable
data class FileUrlResponse(
    val urls: List<FileUrl> = emptyList(),
)

@Serializable
data class FileUrl(
    val url: String = "",
)

@Serializable
data class CreateFileRequest(
    val uri: String,
    val type: String,
    @SerialName("err_on_conflict") val errOnConflict: Boolean = false,
)

@Serializable
data class RenameFileRequest(
    val uri: String,
    @SerialName("new_name") val newName: String,
)

@Serializable
data class MoveFileRequest(
    val uris: List<String>,
    val dst: String,
    val copy: Boolean = false,
)

@Serializable
data class DeleteFileRequest(
    val uris: List<String>,
    val unlink: Boolean = false,
    /** Bypass the trash and remove right away (used from the trash view). */
    @SerialName("skip_soft_delete") val skipSoftDelete: Boolean = false,
)

@Serializable
data class RestoreFileRequest(val uris: List<String>)

@Serializable
data class CreateUploadSessionRequest(
    val uri: String,
    val size: Long,
    @SerialName("last_modified") val lastModified: Long = 0,
    @SerialName("mime_type") val mimeType: String = "",
    @SerialName("policy_id") val policyId: String = "",
    @SerialName("entity_type") val entityType: String = "",
    /** Fylvo: the version (entity id) an edit is based on; the server refuses with 40076 when it is not current. */
    val previous: String = "",
    /** Fylvo: with [previous], fail with 40904 instead of creating the file when it is gone. */
    @SerialName("previous_required") val previousRequired: Boolean = false,
)

@Serializable
data class DeleteUploadSessionRequest(
    val id: String,
    val uri: String,
)

@Serializable
data class UploadSessionResponse(
    @SerialName("session_id") val sessionId: String = "",
    @SerialName("upload_id") val uploadId: String = "",
    @SerialName("chunk_size") val chunkSize: Long = 0,
    val expires: Long = 0,
    @SerialName("upload_urls") val uploadUrls: List<String>? = null,
    @SerialName("completeURL") val completeUrl: String = "",
    val uri: String = "",
    @SerialName("storage_policy") val storagePolicy: StoragePolicy? = null,
    @SerialName("callback_secret") val callbackSecret: String = "",
    @SerialName("rapid_uploaded") val rapidUploaded: Boolean = false,
    /** Fylvo: the entity (version) this upload becomes once it is finished. */
    @SerialName("entity_id") val entityId: String = "",
)

@Serializable
data class ThumbResponse(
    val url: String = "",
    val expires: String? = null,
)

@Serializable
data class ShareCreateRequest(
    val uri: String,
    val downloads: Int = 0,
    @SerialName("is_private") val isPrivate: Boolean = false,
    val password: String = "",
    val expire: Int = 0,
    @SerialName("share_view") val shareView: Boolean = false,
    @SerialName("show_readme") val showReadme: Boolean = false,
    @SerialName("allow_upload") val allowUpload: Boolean = false,
    @SerialName("allow_edit") val allowEdit: Boolean = false,
    @SerialName("preview_only") val previewOnly: Boolean = false,
    @SerialName("upload_only") val uploadOnly: Boolean = false,
)

@Serializable
data class SiteConfig(
    val authn: Boolean = false,
    @SerialName("register_enabled") val registerEnabled: Boolean = false,
    @SerialName("invitation_code") val invitationCode: Boolean = false,
    @SerialName("tos_url") val tosUrl: String = "",
    @SerialName("privacy_policy_url") val privacyPolicyUrl: String = "",
)

@Serializable
data class TaskItem(
    val id: String = "",
    val status: String = "",
    val type: String = "",
    val error: String? = null,
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class TaskListResponse(
    val tasks: List<TaskItem> = emptyList(),
)

/** Fylvo: an Android release the server offers for in-app updates (data/AppUpdater). */
@Serializable
data class AndroidRelease(
    @SerialName("version_code") val versionCode: Int = 0,
    @SerialName("version_name") val versionName: String = "",
    /** Lowercase hex SHA-256 of the APK. */
    val sha256: String = "",
    val size: Long = 0,
)

/** Fylvo: `site/config/explorer`; `icons` is a JSON string of a list (or a list). */
@Serializable
data class ExplorerConfig(
    val icons: kotlinx.serialization.json.JsonElement? = null,
)
