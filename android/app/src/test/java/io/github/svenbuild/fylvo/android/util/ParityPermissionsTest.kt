package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.FileObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Base64

/** Fylvo 1.6: the per-action rules of design/android-parity-plan.md. */
class ParityPermissionsTest {
    private fun bits(vararg on: Int): String {
        val bytes = ByteArray(4)
        on.forEach { bytes[it / 8] = (bytes[it / 8].toInt() or (1 shl (it % 8))).toByte() }
        return Base64.getEncoder().encodeToString(bytes)
    }

    private fun file(path: String, capability: String? = null, owned: Boolean? = null, folder: Boolean = false) =
        FileObject(type = if (folder) 1 else 0, name = "a.txt", path = path, capability = capability, owned = owned)

    @Test fun shareNeedsOwnerGroupRightAndFileRight() {
        val own = file("cloudreve://my/a.txt", bits(18), owned = true)
        assertTrue(Permissions.canShare(own, groupShare = true))
        assertFalse(Permissions.canShare(own, groupShare = false))
        // Not known yet: not offered.
        assertFalse(Permissions.canShare(own, groupShare = null))
        assertFalse(Permissions.canShare(file("cloudreve://my/a.txt", bits(1), owned = true), groupShare = true))
        assertFalse(Permissions.canShare(file("cloudreve://x@share/a.txt", bits(18), owned = true), groupShare = true))
        assertFalse(Permissions.canShare(file("cloudreve://g1@connected/a.txt", bits(18), owned = true), groupShare = true))
    }

    @Test fun uploadLinkVisitorsMayNotReplaceContent() {
        // View + upload link: create, upload, download, but no rename.
        assertFalse(Permissions.canSaveContent(file("cloudreve://x@share/a.txt", bits(0, 6, 7))))
        assertTrue(Permissions.canSaveContent(file("cloudreve://x@share/a.txt", bits(1, 6, 7))))
        assertTrue(Permissions.canSaveContent(file("cloudreve://my/a.txt", owned = true)))
        assertFalse(Permissions.canSaveContent(file("cloudreve://my/a.txt", owned = false)))
        assertFalse(Permissions.canSaveContent(file("cloudreve://my/d", owned = true, folder = true)))
    }

    @Test fun connectedContentNeedsRenameUploadAndDownload() {
        assertTrue(Permissions.canSaveContent(file("cloudreve://g1@connected/a.txt", bits(1, 6, 7))))
        assertFalse(Permissions.canSaveContent(file("cloudreve://g1@connected/a.txt", bits(6, 7))))
        assertFalse(Permissions.canSaveContent(file("cloudreve://g1@connected/a.txt", bits(1, 6))))
    }

    @Test fun newTextFileFollowsTheFolder() {
        assertTrue(Permissions.canCreateTextIn(file("cloudreve://my/d", owned = true, folder = true)))
        assertFalse(Permissions.canCreateTextIn(file("cloudreve://x@share/d", bits(0, 6, 9), folder = true)))
        assertTrue(Permissions.canCreateTextIn(file("cloudreve://x@share/d", bits(0, 1, 6, 9), folder = true)))
        assertFalse(Permissions.canCreateTextIn(file("cloudreve://g1@connected/d", bits(0, 1, 6), folder = true)))
        assertFalse(Permissions.canCreateTextIn(null))
    }

    @Test fun versionsOnlyForOwnFiles() {
        assertTrue(Permissions.canManageVersions(file("cloudreve://my/a.txt", bits(20), owned = true)))
        assertFalse(Permissions.canManageVersions(file("cloudreve://my/a.txt", bits(1), owned = true)))
        assertFalse(Permissions.canManageVersions(file("cloudreve://x@share/a.txt", bits(20), owned = true)))
        assertFalse(Permissions.canManageVersions(file("cloudreve://my/a.txt", bits(20), owned = false)))
    }

    @Test fun archiveTasksNeverTouchSecretsOrConnectedStorages() {
        val own = file("cloudreve://my/a.zip", owned = true)
        assertTrue(Permissions.canArchiveFrom(listOf(own), groupArchive = true))
        assertFalse(Permissions.canArchiveFrom(listOf(own), groupArchive = null))
        assertFalse(Permissions.canArchiveFrom(listOf(file("cloudreve://x:pw1@share/a.zip", bits(7))), groupArchive = true))
        assertFalse(Permissions.canArchiveFrom(listOf(file("cloudreve://g1@connected/a.zip", bits(7))), groupArchive = true))
        assertFalse(Permissions.canArchiveFrom(listOf(file("cloudreve://x@share/a.zip", bits(9))), groupArchive = true))
        assertTrue(Permissions.canArchiveInto(file("cloudreve://my/d", owned = true, folder = true)))
        assertFalse(Permissions.canArchiveInto(file("cloudreve://x@share/d", bits(0), folder = true)))
        assertTrue(Permissions.canArchiveInto(file("cloudreve://x@share/d", bits(0, 6), folder = true)))
        assertFalse(Permissions.canArchiveInto(file("cloudreve://x:pw1@share/d", bits(0, 6), folder = true)))
    }

    @Test fun shareAddressesWithPasswordsAreRecognised() {
        assertTrue(CrUri.hasSecret("cloudreve://abc:pw12@share/Sub/a.txt"))
        assertTrue(CrUri.hasSecret("cloudreve://abc:pw12@share"))
        assertFalse(CrUri.hasSecret("cloudreve://abc@share/Sub"))
        assertFalse(CrUri.hasSecret("cloudreve://my/a:b.txt"))
        assertFalse(CrUri.hasSecret("cloudreve://my?name=a:b"))
        assertEquals("abc", CrUri.shareId("cloudreve://abc:pw12@share/Sub"))
        assertEquals("abc", CrUri.shareId("cloudreve://abc@share"))
        assertNull(CrUri.shareId("cloudreve://my/x"))
        assertEquals("cloudreve://abc:pw12@share", CrUri.shareRoot("abc", "pw12"))
        assertEquals("cloudreve://abc@share", CrUri.shareRoot("abc"))
    }
}
