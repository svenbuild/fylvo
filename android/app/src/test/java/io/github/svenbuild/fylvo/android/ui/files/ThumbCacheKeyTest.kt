package io.github.svenbuild.fylvo.android.ui.files

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ThumbCacheKeyTest {

    @Test fun storedThumbnailIsKeyedByItsId() {
        val today = "https://cloud.example.org/api/v4/file/content/Ab3x/0/photo.jpg.jpg?sign=abc%3D%3A1795600000"
        val tomorrow = "https://cloud.example.org/api/v4/file/content/Ab3x/0/photo.jpg.jpg?sign=xyz%3D%3A1795686400"
        assertEquals("thumb:Ab3x", thumbCacheKey(today))
        // A new signature (next day) keeps the key.
        assertEquals(thumbCacheKey(today), thumbCacheKey(tomorrow))
    }

    @Test fun otherAddressesHaveNoKey() {
        assertNull(thumbCacheKey(null))
        assertNull(thumbCacheKey("https://bucket.example.org/thumb/photo.jpg?X-Amz-Signature=1"))
    }
}
