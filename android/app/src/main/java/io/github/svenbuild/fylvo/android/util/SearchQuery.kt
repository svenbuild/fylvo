package io.github.svenbuild.fylvo.android.util

import java.net.URLEncoder

/**
 * Fylvo 1.6: filters of the file search (design/android-parity-plan.md, 10), as the web builds
 * them (`frontend/src/util/uri.ts`). [type] "file" or "folder"; sizes in bytes; [updatedAfter] in
 * Unix seconds; [tags] by name.
 */
data class SearchFilters(
    val type: String? = null,
    val sizeMin: Long? = null,
    val sizeMax: Long? = null,
    val updatedAfter: Long? = null,
    val tags: Set<String> = emptySet(),
) {
    val any: Boolean get() = type != null || sizeMin != null || sizeMax != null || updatedAfter != null || tags.isNotEmpty()
    val count: Int get() = listOf(type != null, sizeMin != null || sizeMax != null, updatedAfter != null).count { it } + tags.size
}

object SearchQuery {
    /** The words of a query: split at spaces, text in quotes stays together. */
    fun words(query: String): List<String> =
        Regex("\"([^\"]+)\"|(\\S+)").findAll(query).map { it.groupValues[1].ifEmpty { it.groupValues[2] } }.filter { it.isNotBlank() }.toList()

    /**
     * The search address below [base], or null when there is nothing to search for. A [category]
     * (`image`, `video`, `audio`, `document`) replaces the whole search on the server, so it is
     * sent alone; the name is then matched in the app ([matches]).
     */
    fun build(base: String, query: String, filters: SearchFilters, category: String? = null): String? {
        if (category != null) return "$base?category=$category"
        val words = words(query)
        if (words.isEmpty() && !filters.any) return null
        val params = mutableListOf<String>()
        words.forEach { params += "name=" + enc(it) }
        if (words.isNotEmpty()) params += "case_folding"
        filters.type?.let { params += "type=$it" }
        filters.sizeMin?.let { params += "size_gte=$it" }
        filters.sizeMax?.let { params += "size_lte=$it" }
        filters.updatedAfter?.let { params += "updated_gte=$it" }
        filters.tags.sorted().forEach { params += enc("meta_tag:$it") + "=" }
        return base + "?" + params.joinToString("&")
    }

    /** A name holds every word of [query] (case ignored); for category results. */
    fun matches(name: String, query: String): Boolean = words(query).all { name.contains(it, ignoreCase = true) }

    /** A full-text snippet without its `<em>` marks; the marked parts as index ranges. */
    fun snippet(content: String): Pair<String, List<IntRange>> {
        val text = StringBuilder()
        val marks = mutableListOf<IntRange>()
        var rest = content.replace("</em><em>", "")
        while (true) {
            val start = rest.indexOf("<em>")
            if (start < 0) break
            val end = rest.indexOf("</em>", start + 4)
            if (end < 0) break
            text.append(rest, 0, start)
            val from = text.length
            text.append(rest, start + 4, end)
            marks += from until text.length
            rest = rest.substring(end + 5)
        }
        text.append(rest.replace("<em>", "").replace("</em>", ""))
        // Line breaks become spaces one for one, so the marks stay where they are.
        val flat = String(CharArray(text.length) { i -> if (text[i].isWhitespace()) ' ' else text[i] })
        return flat to marks.filter { !it.isEmpty() }
    }

    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8").replace("+", "%20")
}
