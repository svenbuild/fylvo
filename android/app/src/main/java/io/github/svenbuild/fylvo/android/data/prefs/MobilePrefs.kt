package io.github.svenbuild.fylvo.android.data.prefs

import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.FylvoPrefs
import io.github.svenbuild.fylvo.android.api.SidebarItem
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull

/**
 * Fylvo: a place of the app that a bottom tab or a chip above "My files" can open. [key] is its
 * value in `mobile.tabs` (design/customize-plan.md, Android section).
 */
sealed class Place(val key: String) {
    data object Home : Place("home")
    data object Files : Place("files")
    data object Shared : Place("shared")
    data object Favorites : Place("favorites")
    data object Tags : Place("tags")
    data object Recent : Place("recent")
    data object Offline : Place("offline")
    data object Trash : Place("trash")
    data object Account : Place("account")
    /** `image`, `video`, `audio` or `document`. */
    data class Category(val category: String) : Place("category:$category")
    /** A shortcut of the sidebar; [name] as the sidebar shows it (empty: the folder's name). */
    data class Link(val uri: String, val name: String = "") : Place("link:$uri") {
        override fun equals(other: Any?): Boolean = other is Link && other.uri == uri
        override fun hashCode(): Int = uri.hashCode()
    }
    data class Storage(val id: String) : Place("connected:$id")

    override fun toString(): String = key

    companion object {
        val CATEGORIES = listOf("image", "video", "audio", "document")

        fun parse(key: String): Place? = when {
            key == "home" -> Home
            key == "files" -> Files
            key == "shared" -> Shared
            key == "favorites" -> Favorites
            key == "tags" -> Tags
            key == "recent" -> Recent
            key == "offline" -> Offline
            key == "trash" -> Trash
            key == "account" -> Account
            key.startsWith("category:") -> key.removePrefix("category:").takeIf { it in CATEGORIES }?.let(::Category)
            key.startsWith("link:") -> key.removePrefix("link:").takeIf { it.startsWith("cloudreve://") }?.let { Link(it) }
            key.startsWith("connected:") -> key.removePrefix("connected:").takeIf { it.isNotEmpty() }?.let(::Storage)
            else -> null
        }
    }
}

/** Fylvo: what a swipe over a list row does. */
enum class SwipeAction(val key: String) {
    NONE("none"), FAVORITE("favorite"), SHARE("share"), OFFLINE("offline"), DELETE("delete");

    companion object {
        fun parse(key: String?): SwipeAction = entries.firstOrNull { it.key == key } ?: NONE
    }
}

data class SwipePrefs(val left: SwipeAction = SwipeAction.NONE, val right: SwipeAction = SwipeAction.NONE)

/** Fylvo: list spacing; [COMPACT] is the look of the app before customisation. */
enum class Density(val key: String) {
    COMPACT("compact"), NORMAL("normal"), COMFORTABLE("comfortable");

    companion object {
        fun parse(key: String?): Density = entries.firstOrNull { it.key == key } ?: COMPACT
    }
}

/** Fylvo: light, dark or like the device. */
enum class ThemeMode(val key: String) {
    SYSTEM("system"), LIGHT("light"), DARK("dark");

    companion object {
        fun parse(key: String?): ThemeMode = entries.firstOrNull { it.key == key } ?: SYSTEM
    }
}

/** Fylvo: whether a row can get the star, lose it, or neither. */
enum class StarState { NONE, CAN_ADD, CAN_REMOVE }

/**
 * Fylvo: the rules of the customisation, without Android (unit-tested). Reading is lenient:
 * unknown or broken values fall back to the behaviour without customisation.
 */
object MobilePrefs {
    val DEFAULT_TABS: List<Place> = listOf(Place.Home, Place.Files, Place.Shared, Place.Account)
    const val MAX_TABS = 5
    private const val FORMAT_VERSION = 1

    // ---- tabs --------------------------------------------------------------

    /**
     * The bottom tabs from `mobile.tabs`. Files and Account are always there (appended when
     * missing), unknown, repeated and vanished places ([linkExists], [storageExists]) are skipped,
     * at most [MAX_TABS]. Without a usable list: [DEFAULT_TABS].
     */
    fun tabs(
        mobile: JsonObject?,
        linkExists: (String) -> Boolean = { true },
        storageExists: (String) -> Boolean = { true },
    ): List<Place> {
        val raw = (mobile?.get("tabs") as? JsonArray) ?: return DEFAULT_TABS
        val parsed = LinkedHashSet<Place>()
        raw.forEach { element ->
            val key = (element as? JsonPrimitive)?.takeIf { it.isString }?.content ?: return@forEach
            val place = Place.parse(key) ?: return@forEach
            when (place) {
                is Place.Link -> if (!linkExists(place.uri)) return@forEach
                is Place.Storage -> if (!storageExists(place.id)) return@forEach
                else -> Unit
            }
            parsed.add(place)
        }
        return normalizeTabs(parsed.toList())
    }

    /** Files and Account added, cut to [MAX_TABS] keeping both; only those two: the default. */
    fun normalizeTabs(tabs: List<Place>): List<Place> {
        val list = tabs.distinct().toMutableList()
        if (Place.Files !in list) list.add(list.indexOf(Place.Account).takeIf { it >= 0 } ?: list.size, Place.Files)
        if (Place.Account !in list) list.add(Place.Account)
        while (list.size > MAX_TABS) {
            val drop = list.indexOfLast { it != Place.Files && it != Place.Account }
            list.removeAt(drop)
        }
        return if (list.size <= 2) DEFAULT_TABS else list
    }

    // ---- swipes ------------------------------------------------------------

    fun swipe(mobile: JsonObject?): SwipePrefs {
        val swipe = mobile?.get("swipe") as? JsonObject ?: return SwipePrefs()
        fun read(side: String) = SwipeAction.parse((swipe[side] as? JsonPrimitive)?.contentOrNull)
        return SwipePrefs(left = read("left"), right = read("right"))
    }

    /**
     * The swipe [action] for one row, or null when it cannot do it there. [limited]: rows whose
     * address may come from an older copy (Home, the favourites page): only removing the star,
     * which works by id.
     */
    fun swipeAction(
        action: SwipeAction,
        star: StarState,
        canShare: Boolean,
        canOffline: Boolean,
        canDelete: Boolean,
        limited: Boolean,
    ): SwipeAction? = action.takeIf {
        when (action) {
            SwipeAction.NONE -> false
            SwipeAction.FAVORITE -> if (limited) star == StarState.CAN_REMOVE else star != StarState.NONE
            SwipeAction.SHARE -> !limited && canShare
            SwipeAction.OFFLINE -> !limited && canOffline
            SwipeAction.DELETE -> !limited && canDelete
        }
    }

    // ---- writing mobile ----------------------------------------------------

    /** [current] with `v` and only the given parts replaced; fields of other versions stay. */
    fun withTabs(current: JsonObject?, tabs: List<Place>): JsonObject =
        put(current, "tabs", JsonArray(normalizeTabs(tabs).map { JsonPrimitive(it.key) }))

    fun withSwipe(current: JsonObject?, swipe: SwipePrefs): JsonObject =
        put(current, "swipe", JsonObject(mapOf("left" to JsonPrimitive(swipe.left.key), "right" to JsonPrimitive(swipe.right.key))))

    private fun put(current: JsonObject?, key: String, value: kotlinx.serialization.json.JsonElement): JsonObject {
        val map = LinkedHashMap(current ?: emptyMap())
        map["v"] = JsonPrimitive(FORMAT_VERSION)
        map[key] = value
        return JsonObject(map)
    }

    // ---- account parts -----------------------------------------------------

    fun density(prefs: FylvoPrefs?): Density = Density.parse(prefs?.view?.density)

    /** The account's mode only when it saved one; otherwise like the device. */
    fun mode(prefs: FylvoPrefs?): ThemeMode =
        if (prefs != null && "mode" in prefs.customized) ThemeMode.parse(prefs.appearance.mode) else ThemeMode.SYSTEM

    /** The building blocks Home knows (design/customize-plan.md). */
    val HOME_WIDGETS = setOf("recent", "favorites", "links", "tags", "storage")

    /**
     * The visible building blocks of Home in the account's order, or null while the account has
     * not arranged Home (`home` not in `customized`): Home then looks as before customisation.
     */
    fun homeWidgets(prefs: FylvoPrefs?): List<String>? {
        if (prefs == null || "home" !in prefs.customized) return null
        return prefs.home.widgets.filter { !it.hidden && it.type in HOME_WIDGETS }.map { it.type }.distinct()
    }

    /** The sidebar's shortcuts (in order), for tabs and the "links" widget. */
    fun links(prefs: FylvoPrefs?): List<Place.Link> =
        prefs?.sidebar?.items.orEmpty().filter { it.type == "link" && it.uri.isNotEmpty() }.map { Place.Link(it.uri, it.name) }

    /**
     * The chips above "My files": the visible sidebar entries in their order, without Home, My
     * files and headings; favourites and tags only when there are some. Without a Shared tab
     * "Shared with me" is always offered, without a Home tab the [storages] too (nothing gets lost).
     */
    fun chips(
        prefs: FylvoPrefs?,
        tabs: List<Place>,
        hasFavorites: Boolean,
        hasTags: Boolean,
        storages: List<String> = emptyList(),
    ): List<Place> {
        if (prefs == null) return emptyList()
        val out = LinkedHashSet<Place>()
        prefs.sidebar.items.forEach { item ->
            if (item.hidden) return@forEach
            val place = placeOf(item) ?: return@forEach
            if (place == Place.Favorites && !hasFavorites) return@forEach
            if (place == Place.Tags && !hasTags) return@forEach
            out.add(place)
        }
        if (Place.Shared !in tabs) out.add(Place.Shared)
        if (Place.Home !in tabs) storages.forEach { out.add(Place.Storage(it)) }
        return out.filter { it !in tabs || it is Place.Link }
    }

    private fun placeOf(item: SidebarItem): Place? = when (item.type) {
        "favorites" -> Place.Favorites
        "tags" -> Place.Tags
        "shared_with_me" -> Place.Shared
        "trash" -> Place.Trash
        "category" -> item.category.takeIf { it in Place.CATEGORIES }?.let { Place.Category(it) }
        "link" -> item.uri.takeIf { it.isNotEmpty() }?.let { Place.Link(it, item.name) }
        "connected" -> item.id.takeIf { it.isNotEmpty() }?.let { Place.Storage(it) }
        else -> null
    }

    // ---- star, colour, tags ------------------------------------------------

    /**
     * Whether [file] can get or lose the star. Anything that is a favourite can always lose it.
     * Adding: own files below `cloudreve://my`, and files in a share that is member-only and not
     * the user's own ([memberShare] answers for the share id, null while unknown).
     */
    fun starState(file: FileObject, favorites: Set<String>, memberShare: (String) -> Boolean?): StarState {
        if (file.id.isEmpty() || file.isUploading) return StarState.NONE
        if (file.id in favorites) return StarState.CAN_REMOVE
        val path = file.path
        if (Connected.isConnected(path)) return StarState.NONE
        if (path.startsWith(CrUri.MY_PREFIX + "/") && !CrUri.isRoot(path) && '?' !in path) return StarState.CAN_ADD
        if (CrUri.isShare(path) && file.owned != true && '?' !in path) {
            val shareId = CrUri.root(path).removePrefix("cloudreve://").substringBefore('@')
            if (memberShare(shareId) == true) return StarState.CAN_ADD
        }
        return StarState.NONE
    }

    /** Colour and tags: the server lets only the owner change them, below `cloudreve://my`. */
    fun canEditMetadata(files: List<FileObject>): Boolean = files.isNotEmpty() && files.all {
        it.owned == true && !it.isUploading && it.path.startsWith(CrUri.MY_PREFIX + "/") && !CrUri.isRoot(it.path)
    }

    /** The file's tags (`tag:<name>` = colour) in name order. */
    fun tagsOf(file: FileObject): List<Pair<String, String>> =
        file.metadata.orEmpty().filterKeys { it.startsWith(TAG_PREFIX) && it.length > TAG_PREFIX.length }
            .map { (k, v) -> k.removePrefix(TAG_PREFIX) to v }
            .sortedBy { it.first.lowercase() }

    fun folderColor(file: FileObject): String? =
        file.metadata?.get(ICON_COLOR)?.takeIf { file.isFolder && it.isNotBlank() }

    const val TAG_PREFIX = "tag:"
    const val ICON_COLOR = "customize:icon_color"

    /** The search of the web app's sidebar for one tag, from "My files". */
    fun tagSearchUri(name: String): String =
        CrUri.MY_PREFIX + "?" + java.net.URLEncoder.encode("meta_$TAG_PREFIX$name", "UTF-8") + "="

    /** Folder and tag colours offered on the phone: eight of the web app's preset colours. */
    val COLORS = listOf("#f44336", "#ff9800", "#ffc107", "#4caf50", "#009688", "#2196f3", "#3f51b5", "#9c27b0")
}
