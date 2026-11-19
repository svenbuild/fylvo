package io.github.svenbuild.fylvo.android.work

import android.net.Uri
import io.github.svenbuild.fylvo.android.ui.files.transfers.PendingUpload
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35]) // Robolectric 4.14 tops out below targetSdk 36
class UploadNamingTest {

    @Test
    fun `unique names count up and keep the extension`() {
        val taken = setOf("report.pdf", "report (1).pdf", "notes", "notes (1)")
        assertEquals("report.pdf", UploadWorker.uniqueName("report.pdf", emptySet()))
        assertEquals("report (2).pdf", UploadWorker.uniqueName("report.pdf", taken))
        assertEquals("notes (2)", UploadWorker.uniqueName("notes", taken))
        assertEquals(".bashrc (1)", UploadWorker.uniqueName(".bashrc", setOf(".bashrc")))
    }

    @Test
    fun `conflict resolutions`() {
        val uris = listOf(Uri.parse("content://a"), Uri.parse("content://b"), Uri.parse("content://c"))
        val pending = PendingUpload(uris, listOf("a.txt", "b.txt", "c.txt"), "cloudreve://my", existing = setOf("a.txt", "c.txt"))
        assertEquals(listOf("a.txt", "c.txt"), pending.conflicts)

        val replace = pending.replace()
        assertTrue(replace.overwrite)
        assertEquals(3, replace.uris.size)

        val keepBoth = pending.keepBoth()
        assertFalse(keepBoth.overwrite)
        assertEquals(listOf("a (1).txt", "b.txt", "c (1).txt"), keepBoth.names)

        val skip = pending.skip()
        assertEquals(listOf("b.txt"), skip.names)
        assertEquals(listOf(uris[1]), skip.uris)
    }

    @Test
    fun `keep both avoids clashing with earlier renames in the same batch`() {
        val uris = List(2) { Uri.parse("content://$it") }
        val pending = PendingUpload(uris, listOf("x.txt", "x.txt"), "cloudreve://my", existing = setOf("x.txt"))
        assertEquals(listOf("x (1).txt", "x (2).txt"), pending.keepBoth().names)
    }

    @Test
    fun `only content uris of other apps are uploaded`() {
        val context = org.robolectric.RuntimeEnvironment.getApplication()
        val own = context.packageName
        assertTrue(UploadWorker.isForeignContentUri(context, Uri.parse("content://com.android.providers.downloads.documents/document/1")))
        assertFalse(UploadWorker.isForeignContentUri(context, Uri.parse("file:///data/data/$own/files/datastore/session.preferences_pb")))
        assertFalse(UploadWorker.isForeignContentUri(context, Uri.parse("content://$own.fileprovider/offline/a")))
        assertFalse(UploadWorker.isForeignContentUri(context, Uri.parse("content://0@$own.fileprovider/offline/a")))
    }
}
