package io.github.svenbuild.fylvo.android.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject

/**
 * Fylvo: the account's customisation as `GET /api/v4/fylvo/prefs` returns it (the effective
 * values, see design/customize-plan.md). [mobile] belongs to this app (format in MobilePrefs).
 */
@Serializable
data class FylvoPrefs(
    val sidebar: Sidebar = Sidebar(),
    val home: Home = Home(),
    @SerialName("start_page") val startPage: String = "",
    val view: PrefsView = PrefsView(),
    val appearance: Appearance = Appearance(),
    val mobile: JsonObject = JsonObject(emptyMap()),
    val favorites: List<String> = emptyList(),
    val customized: List<String> = emptyList(),
) {
    @Serializable
    data class Sidebar(val items: List<SidebarItem> = emptyList())

    @Serializable
    data class Home(val widgets: List<HomeWidget> = emptyList())
}

@Serializable
data class SidebarItem(
    val type: String = "",
    val hidden: Boolean = false,
    val category: String = "",
    val uri: String = "",
    val name: String = "",
    val id: String = "",
)

@Serializable
data class HomeWidget(
    val type: String = "",
    val hidden: Boolean = false,
)

@Serializable
data class PrefsView(
    val density: String = "compact",
    val layout: String = "",
    @SerialName("sort_by") val sortBy: String = "",
    @SerialName("sort_direction") val sortDirection: String = "",
)

@Serializable
data class Appearance(
    val mode: String = "system",
    val accent: String = "",
)

@Serializable
data class FavoritesList(
    val files: List<FileObject> = emptyList(),
    val unavailable: Int = 0,
)

@Serializable
data class FavoriteIds(val favorites: List<String> = emptyList())

@Serializable
data class FavoritesAddRequest(val uris: List<String>)

@Serializable
data class FavoritesRemoveRequest(val ids: List<String>)

@Serializable
data class TagInfo(
    val name: String = "",
    val color: String = "",
    val count: Int = 0,
)

/** Fylvo: `GET /site/config/basic`; only the colour themes are read. */
@Serializable
data class SiteBasic(
    /** JSON text: theme key (dark accent colour) to an MUI theme with `light`/`dark` palettes. */
    val themes: String = "",
)

/** Fylvo: `GET /user/setting`; only what the app needs. */
@Serializable
data class UserSettings(
    @SerialName("disable_view_sync") val disableViewSync: Boolean = false,
    /** Fylvo 1.6: the account has no password (then none can be changed). */
    val passwordless: Boolean = false,
    @SerialName("two_fa_enabled") val twoFaEnabled: Boolean = false,
    /** Apps that may use the account (OAuth); revoked with their [OauthGrant.clientId]. */
    @SerialName("oauth_grants") val oauthGrants: List<OauthGrant> = emptyList(),
)

@Serializable
data class OauthGrant(
    @SerialName("client_id") val clientId: String = "",
    @SerialName("client_name") val clientName: String = "",
    @SerialName("last_used_at") val lastUsedAt: String? = null,
)

/** Fylvo: `PATCH /file/metadata`. */
@Serializable
data class MetadataPatchRequest(
    val uris: List<String>,
    val patches: List<MetadataPatch>,
)

@Serializable
data class MetadataPatch(
    val key: String,
    val value: String = "",
    val remove: Boolean = false,
)

/** Fylvo: `PATCH /file/view` with the folder's whole view object. */
@Serializable
data class ViewPatchRequest(
    val uri: String,
    val view: JsonObject,
)

/** Fylvo: the session a request belongs to (SessionManager.generation), checked by ApiClient. */
data class SessionTag(val generation: Long)

/** Fylvo: only the `view` of a listing, kept whole so a write sends back every field. */
@Serializable
data class FolderViewResponse(
    val view: JsonObject? = null,
    val parent: FileObject? = null,
)

@Serializable
data class ShareInfo(
    val id: String = "",
    @SerialName("member_only") val memberOnly: Boolean = false,
    val owner: ShareOwner? = null,
    /** Fylvo: name of the shared file or folder. */
    val name: String = "",
    /** 0 file, 1 folder. */
    @SerialName("source_type") val sourceType: Int? = null,
    val url: String = "",
    val visited: Int = 0,
    val downloaded: Int = 0,
    /** Downloads left; null = no limit. Only when [unlocked]. */
    @SerialName("remain_downloads") val remainDownloads: Int? = null,
    /** Absolute end (RFC 3339); null = never. Only when [unlocked]. */
    val expires: String? = null,
    val expired: Boolean = false,
    val unlocked: Boolean = false,
    @SerialName("password_protected") val passwordProtected: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    // Owner only (the server leaves these out for everyone else).
    @SerialName("is_private") val isPrivate: Boolean = false,
    /** The link's password, sent to its owner; kept in memory only. */
    val password: String = "",
    @SerialName("share_view") val shareView: Boolean = false,
    @SerialName("show_readme") val showReadme: Boolean = false,
    @SerialName("allow_upload") val allowUpload: Boolean = false,
    @SerialName("hide_content") val hideContent: Boolean = false,
    @SerialName("upload_limit") val uploadLimit: Long = 0,
    val members: List<ShareMemberDto> = emptyList(),
    /** With `owner_extended`: the shared item's folder (a single file: its parent). */
    @SerialName("source_uri") val sourceUri: String = "",
) {
    val isFolder: Boolean get() = sourceType == 1
}

@Serializable
data class ShareOwner(val id: String = "", val nickname: String = "")

@Serializable
data class ShareMemberDto(
    @SerialName("user_id") val userId: String,
    @SerialName("can_edit") val canEdit: Boolean = false,
)

@Serializable
data class ShareListResponse(
    val shares: List<ShareInfo> = emptyList(),
    val pagination: Pagination? = null,
)

/** Fylvo: `GET /user/me`; [group] carries the account's group rights. */
@Serializable
data class Me(
    val id: String = "",
    val email: String = "",
    val nickname: String = "",
    /** "" none, "file" an uploaded picture, "gravatar" (never fetched by the app). */
    val avatar: String = "",
    val group: MeGroup? = null,
)

@Serializable
data class MeGroup(
    val id: String = "",
    val name: String = "",
    /** Base64 bit set of the group's rights (see util/Permissions). */
    val permission: String? = null,
)
