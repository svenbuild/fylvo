package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.FileObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Capability values as a Fylvo server sends them (local test server, 2026-11-19). */
class PermissionsTest {
    private val editorCap = "w8aZAQ==" // member with edit right
    private val readerCap = "gIaYAQ==" // member with read right only
    private val ownerCap = "w8edAQ==" // own files under cloudreve://my

    private fun file(path: String, capability: String?, owned: Boolean?) =
        FileObject(type = 1, name = "x", path = path, capability = capability, owned = owned)

    @Test fun editorMayAddAndModifyInsideShare() {
        val f = file("cloudreve://V6dUe@share/Unterordner", editorCap, null)
        assertTrue(Permissions.canAddTo(f))
        assertTrue(Permissions.canModifyFile(f))
        assertFalse(Permissions.canShareLink(f))
    }

    @Test fun readerMayNeitherAddNorModifyInsideShare() {
        val f = file("cloudreve://V6dUe@share/", readerCap, null)
        assertFalse(Permissions.canAddTo(f))
        assertFalse(Permissions.canModifyFile(f))
    }

    @Test fun ownershipDecidesOutsideShares() {
        assertTrue(Permissions.canModifyFile(file("cloudreve://my/a", ownerCap, true)))
        assertTrue(Permissions.canShareLink(file("cloudreve://my/a", ownerCap, true)))
        // A "Shared with me" entry belongs to someone else.
        assertFalse(Permissions.canModifyFile(file("cloudreve://shared_with_me/6rVFN", "gAKA", null)))
        assertFalse(Permissions.canShareLink(file("cloudreve://shared_with_me/6rVFN", "gAKA", null)))
    }

    @Test fun missingOrBrokenCapabilityFailsClosed() {
        assertFalse(Permissions.canAddTo(file("cloudreve://V6dUe@share/", null, true)))
        assertFalse(Permissions.canModifyFile(file("cloudreve://V6dUe@share/", "%%%", true)))
        assertFalse(Permissions.canAddTo(null))
    }

    @Test fun bitOrderMatchesBoolsetTs() {
        // byte 0 = 0b11000011: bits 0, 1, 6, 7
        assertEquals(listOf(0, 1, 6, 7), (0 until 8).filter { Permissions.enabled(editorCap, it) })
        assertFalse(Permissions.enabled(editorCap, 64))
    }
}
