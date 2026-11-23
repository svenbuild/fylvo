package io.github.svenbuild.fylvo.android.ui.files

import io.github.svenbuild.fylvo.android.api.ActivityEntry
import io.github.svenbuild.fylvo.android.api.EntityDto
import io.github.svenbuild.fylvo.android.api.ExtendedInfo
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.HistoryRepository
import org.junit.Assert.assertEquals
import org.junit.Test

/** Fylvo 1.6: versions and history (design/android-parity-plan.md, 6 and 7). */
class HistoryTest {
    @Test fun onlyStoredVersionsNewestFirst() {
        val f = FileObject(extendedInfo = ExtendedInfo(entities = listOf(
            EntityDto("a", 1, 0, "2026-11-20T10:00:00Z"),
            EntityDto("t", 1, 1, "2026-11-23T10:00:00Z"),
            EntityDto("b", 2, 0, "2026-11-22T10:00:00Z"),
        )))
        assertEquals(listOf("b", "a"), HistoryRepository.versions(f).map { it.id })
    }

    @Test fun exposureReadsLikeACamera() {
        assertEquals("1/250 s", exposure("0.004"))
        assertEquals("2 s", exposure("2"))
        assertEquals("1.5 s", exposure("1.5"))
        assertEquals("abc", exposure("abc"))
        assertEquals("1/250 s", exposure("1/250"))
        assertEquals("2.8", trimNumber("2.800000"))
        assertEquals("35", trimNumber("35"))
    }

    @Test fun deviceFromUserAgent() {
        fun d(ua: String, via: String = "") = ActivityText.device(ActivityEntry(userAgent = ua, via = via), "Windows app", "Android app", " on ")
        assertEquals("Chrome on Windows", d("Mozilla/5.0 (Windows NT 10.0) AppleWebKit Chrome/130 Safari/537"))
        assertEquals("Android app", d("Fylvo-Android/1.6.0"))
        assertEquals("Windows app", d("fylvo-desktop/0.3.0"))
        assertEquals("WebDAV", d("", via = "webdav"))
        assertEquals("", d(""))
    }
}
