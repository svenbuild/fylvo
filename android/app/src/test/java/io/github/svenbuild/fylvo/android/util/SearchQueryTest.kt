package io.github.svenbuild.fylvo.android.util

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo 1.6: the search address carries every filter (design/android-parity-plan.md, 10). */
class SearchQueryTest {
    private val my = "cloudreve://my"

    @Test fun wordsAndQuotes() {
        assertEquals(listOf("rechnung", "2026"), SearchQuery.words(" rechnung  2026 "))
        assertEquals(listOf("zug ticket", "pdf"), SearchQuery.words("\"zug ticket\" pdf"))
    }

    @Test fun allFiltersReachTheServerTogether() {
        val uri = SearchQuery.build(my, "Rechnung Mai", SearchFilters(type = "file", sizeMin = 1024, sizeMax = 2048, updatedAfter = 1_700_000_000, tags = setOf("Work", "a b")))
        assertEquals(
            "cloudreve://my?name=Rechnung&name=Mai&case_folding&type=file&size_gte=1024&size_lte=2048&updated_gte=1700000000" +
                "&meta_tag%3AWork=&meta_tag%3Aa%20b=",
            uri,
        )
    }

    @Test fun filtersAloneAreASearch() {
        assertEquals("cloudreve://my?size_gte=100", SearchQuery.build(my, "", SearchFilters(sizeMin = 100)))
        assertNull(SearchQuery.build(my, "  ", SearchFilters()))
    }

    @Test fun categoryIsSentAloneAndMatchedInTheApp() {
        assertEquals("cloudreve://my/Fotos?category=image", SearchQuery.build("cloudreve://my/Fotos", "ferien", SearchFilters(type = "file"), "image"))
        assertTrue(SearchQuery.matches("Ferien 2026.JPG", "ferien 2026"))
        assertFalse(SearchQuery.matches("Ferien.jpg", "ferien 2026"))
        assertTrue(SearchQuery.matches("x.jpg", ""))
    }

    @Test fun snippetMarks() {
        val (text, marks) = SearchQuery.snippet("…die <em>Rech</em><em>nung</em> vom\nMai <em>2026</em>")
        assertEquals("…die Rechnung vom Mai 2026", text)
        assertEquals(listOf("Rechnung", "2026"), marks.map { text.substring(it) })
    }
}
