package io.github.svenbuild.fylvo.android.data.prefs

import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri

/**
 * Fylvo: where the screen is, as far as the bottom tabs care. [origin] is the way back of a place
 * opened from a chip, the home page or a page ("home", a URI, "page:<page>"), null for a tab.
 */
data class Where(
    val home: Boolean = false,
    val uri: String = CrUri.MY_PREFIX,
    val origin: String? = null,
    /** A page over the files: "favorites" or "tags". */
    val page: String? = null,
    val account: Boolean = false,
    val offline: Boolean = false,
)

/** Fylvo: which tab is active, where "back" leads (unit-tested). */
object Navigation {
    const val RECENT_URI = "cloudreve://my?type=file"
    const val TRASH_URI = "cloudreve://trash"
    private fun categoryUri(category: String) = "cloudreve://my?category=$category"

    /** The place the screen shows, whether or not it is a tab. */
    fun placeOf(where: Where, links: List<Place.Link>): Place = when {
        where.page == "favorites" -> Place.Favorites
        where.page == "tags" -> Place.Tags
        where.account -> if (where.offline) Place.Offline else Place.Account
        where.home -> Place.Home
        where.uri == RECENT_URI -> Place.Recent
        where.uri == TRASH_URI -> Place.Trash
        where.uri == CrUri.SHARED_WITH_ME || CrUri.isShare(where.uri) -> Place.Shared
        else -> Place.CATEGORIES.firstOrNull { where.uri == categoryUri(it) }?.let { Place.Category(it) }
            ?: Connected.storageId(where.uri)?.let { Place.Storage(it) }
            ?: linkOf(where.uri, links)
            ?: Place.Files
    }

    private fun linkOf(uri: String, links: List<Place.Link>): Place.Link? = links.firstOrNull { link ->
        if (CrUri.isSearch(link.uri)) uri == link.uri else uri == link.uri || uri.startsWith(link.uri.trimEnd('/') + "/")
    }

    /**
     * The active tab: the place itself when it is a tab; a place opened from the chips (or a page
     * opened from them) belongs to Files, one opened from Home to Home; otherwise [last].
     */
    fun activeTab(where: Where, tabs: List<Place>, links: List<Place.Link>, last: Place?): Place {
        val place = placeOf(where, links)
        if (place in tabs) return place
        val origin = where.origin
        val fromFiles = origin == CrUri.MY_PREFIX || origin?.startsWith("page:") == true ||
            (where.page != null && !where.home && origin == null)
        return when {
            fromFiles && Place.Files in tabs -> Place.Files
            origin == "home" && Place.Home in tabs -> Place.Home
            // A folder of My files (also below a shortcut that is no tab) belongs to Files.
            !where.home && !where.account && where.page == null && where.uri.startsWith(CrUri.MY_PREFIX) &&
                !CrUri.isSearch(where.uri) && Place.Files in tabs -> Place.Files
            last != null && last in tabs -> last
            else -> Place.Files
        }
    }

    /** True at the top of [tab] opened as a tab (not as a place below another one). */
    fun atTabRoot(where: Where, tab: Place, links: List<Place.Link>): Boolean {
        if (where.origin != null || placeOf(where, links) != tab) return false
        return when (tab) {
            Place.Home -> where.home
            Place.Favorites, Place.Tags -> true
            Place.Account, Place.Offline -> true
            Place.Files -> !where.home && where.page == null && where.uri == CrUri.MY_PREFIX
            Place.Shared -> where.uri == CrUri.SHARED_WITH_ME
            Place.Recent -> where.uri == RECENT_URI
            Place.Trash -> where.uri == TRASH_URI
            is Place.Category -> true
            is Place.Storage -> CrUri.isRoot(where.uri)
            is Place.Link -> where.uri == tab.uri
        }
    }

    /** Where the app opens: My files for `start_page = my_files`, else Home, else the first tab. */
    fun startTab(startPage: String?, tabs: List<Place>): Place = when {
        startPage == "my_files" && Place.Files in tabs -> Place.Files
        Place.Home in tabs -> Place.Home
        else -> tabs.firstOrNull() ?: Place.Files
    }

    /** What "back" does at the top of the active tab: go to the start tab, or leave the app. */
    fun backAtTabRoot(active: Place, start: Place): Place? = if (active == start) null else start
}
