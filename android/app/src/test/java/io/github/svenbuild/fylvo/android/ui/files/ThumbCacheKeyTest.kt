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

    @Test fun connectedPreviewIsKeyedByStorageFileAndVersion() {
        val file = io.github.svenbuild.fylvo.android.api.FileObject(
            id = "cs7a62", name = "photo.png", path = "cloudreve://pd7niu@connected/photo.png", primaryEntity = "vb2c",
        )
        val today = "https://cloud.example.org/api/v4/fylvo/connected/thumb/Al1t/thumb.jpg"
        val tomorrow = "https://cloud.example.org/api/v4/fylvo/connected/thumb/Zq9x/thumb.jpg"
        assertEquals("cthumb:pd7niu:cs7a62:vb2c", thumbCacheKey(file, today))
        // The address of the next day keeps the key; a new version gets a new one.
        assertEquals(thumbCacheKey(file, today), thumbCacheKey(file, tomorrow))
        assertEquals("cthumb:pd7niu:cs7a62:v9", thumbCacheKey(file.copy(primaryEntity = "v9"), today))
        // The same file id in another storage is another picture.
        assertEquals("cthumb:other:cs7a62:vb2c", thumbCacheKey(file.copy(path = "cloudreve://other@connected/photo.png"), today))
        assertEquals("pd7niu", io.github.svenbuild.fylvo.android.data.ConnectedThumbs.storageOf("cthumb:pd7niu:cs7a62:vb2c"))
        assertNull(io.github.svenbuild.fylvo.android.data.ConnectedThumbs.storageOf("thumb:Ab3x"))
        // Without a version no key (Coil then keys by the address of the day).
        assertNull(thumbCacheKey(file.copy(primaryEntity = ""), today))
        assertNull(thumbCacheKey(file, null))
    }

    @Test fun connectedPictureAddressAndBusyWait() {
        val t = io.github.svenbuild.fylvo.android.data.ConnectedThumbs
        assertEquals(true, t.isPicture("/api/v4/fylvo/connected/thumb/Al1t/thumb.jpg"))
        assertEquals(false, t.isPicture("/api/v4/file/content/Ab3x/0/photo.jpg.jpg"))
        // Retry-After in seconds, within 1 to 30; 5 when missing or not a number.
        assertEquals(5_000L, t.retryAfterMs("5"))
        assertEquals(5_000L, t.retryAfterMs(null))
        assertEquals(5_000L, t.retryAfterMs("Wed, 21 Oct 2026 07:28:00 GMT"))
        assertEquals(30_000L, t.retryAfterMs("600"))
        assertEquals(1_000L, t.retryAfterMs("0"))
    }
}
