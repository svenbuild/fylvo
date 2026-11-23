package io.github.svenbuild.fylvo.android.data.prefs

import io.github.svenbuild.fylvo.android.data.SortBy
import io.github.svenbuild.fylvo.android.data.SortOrder
import io.github.svenbuild.fylvo.android.data.ViewMode
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull

/**
 * Fylvo: a folder's stored view (`PATCH /file/view`) changed the way the web app does it: only
 * the changed fields are replaced, fields the server requires are filled like the web app fills
 * them, everything else (columns, thumbnails, gallery width) stays (unit-tested).
 */
object MobileView {
    /** The web app's defaults for fields the stored view may lack. */
    private const val DEFAULT_PAGE_SIZE = 100
    private const val MIN_PAGE_SIZE = 50
    private const val DEFAULT_GALLERY_WIDTH = 110

    private fun JsonObject.string(key: String): String? = (this[key] as? JsonPrimitive)?.contentOrNull
    private fun JsonObject.int(key: String): Int? = (this[key] as? JsonPrimitive)?.intOrNull

    private fun complete(view: JsonObject, changes: Map<String, JsonPrimitive>): JsonObject {
        val map = LinkedHashMap(view)
        map.putAll(changes)
        if ((view.int("page_size") ?: 0) < MIN_PAGE_SIZE) map["page_size"] = JsonPrimitive(DEFAULT_PAGE_SIZE)
        val width = view.int("gallery_width")
        if (width == null || width !in 50..500) map["gallery_width"] = JsonPrimitive(DEFAULT_GALLERY_WIDTH)
        if (map["order_direction"] == null) map["order_direction"] = JsonPrimitive("asc")
        if (map["view"] == null) map["view"] = JsonPrimitive("grid")
        return JsonObject(map)
    }

    /** List or grid; a gallery stays a gallery unless the choice is the list. */
    fun withLayout(view: JsonObject, layout: String): JsonObject {
        val current = view.string("view")
        val next = if (layout == "grid" && current == "gallery") "gallery" else layout
        return complete(view, mapOf("view" to JsonPrimitive(next)))
    }

    fun withSort(view: JsonObject, sortBy: String, ascending: Boolean): JsonObject =
        complete(view, mapOf("order" to JsonPrimitive(sortBy), "order_direction" to JsonPrimitive(if (ascending) "asc" else "desc")))

    /** How the phone shows a stored view: list or grid (gallery = grid) and its sort. */
    fun modeOf(layout: String?): ViewMode = if (layout == "list") ViewMode.LIST else ViewMode.GRID

    fun sortOf(sortBy: String?, direction: String?): SortOrder = SortOrder(
        by = SortBy.entries.firstOrNull { it.apiName == sortBy } ?: SortBy.CREATED,
        ascending = direction != "desc",
    )
}
