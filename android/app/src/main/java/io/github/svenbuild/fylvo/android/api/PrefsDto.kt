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

/** Body of `PATCH /fylvo/prefs`: only the parts that are set are sent (explicitNulls off). */
@Serializable
data class PrefsPatch(
    @SerialName("start_page") val startPage: String? = null,
    val view: DensityPatch? = null,
    val appearance: AppearancePatch? = null,
    val home: JsonElement? = null,
    val mobile: JsonElement? = null,
)

@Serializable
data class DensityPatch(val density: String)

@Serializable
data class AppearancePatch(val mode: String? = null, val accent: String? = null)

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
    @SerialName("default_theme") val defaultTheme: String = "",
)

/** Fylvo: `GET /user/setting`; only what the app needs. */
@Serializable
data class UserSettings(
    @SerialName("disable_view_sync") val disableViewSync: Boolean = false,
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
)

@Serializable
data class ShareOwner(val id: String = "")
