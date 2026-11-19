package io.github.svenbuild.fylvo.android.util

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CrUriTest {
    @Test fun myPathsKeepTheirRoot() {
        assertEquals("cloudreve://my", CrUri.parent("cloudreve://my/a"))
        assertEquals("cloudreve://my/a", CrUri.parent("cloudreve://my/a/b"))
        assertTrue(CrUri.isRoot("cloudreve://my/"))
        assertEquals("My files / a", CrUri.displayPath("cloudreve://my/a"))
    }

    @Test fun sharePathsStayInsideTheShare() {
        val root = "cloudreve://V6dUe@share"
        assertTrue(CrUri.isShare("$root/a"))
        assertTrue(CrUri.isRoot("$root/"))
        assertEquals(root, CrUri.parent("$root/a"))
        assertEquals(root, CrUri.parent(root))
        assertEquals("$root/a b", CrUri.canonical("$root/a%20b"))
        assertFalse(CrUri.isShare("cloudreve://my/a@share"))
    }

    @Test fun rootLabels() {
        assertEquals("Shared with me", CrUri.displayName("cloudreve://shared_with_me"))
        assertEquals("Trash", CrUri.displayName("cloudreve://trash"))
    }
}
