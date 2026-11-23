package io.github.svenbuild.fylvo.android.data.offline

import io.github.svenbuild.fylvo.android.work.FolderUpload
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo 1.6: "Edit" without keeping a file offline, and folder uploads (design/android-parity-plan.md, 13 and 15). */
class StandaloneEditTest {
    private val day = 24 * 60 * 60_000L
    private val now = 100 * day

    private fun entry(standalone: Boolean = true, confirmed: EditSnapshot? = null, retiredAt: Long = 0) = EditEntry(
        id = "e1", fileId = "f1", uri = "cloudreve://my/a.docx", name = "a.docx",
        base = EditSnapshot(3, 0, "base"), confirmed = confirmed, state = EditState.Editing,
        createdAt = now - 3 * day, grantedAt = now - 2 * day, retiredAt = retiredAt, standalone = standalone,
    )

    @Test fun idleUploadedStandaloneCopiesAreFinished() {
        val stat = LocalStat(3, now - 2 * day)
        assertTrue(idleStandalone(entry(), stat, "base", now))
        // Changed since the last upload: never.
        assertFalse(idleStandalone(entry(), stat, "other", now))
        // Touched within the day: not yet.
        assertFalse(idleStandalone(entry(), LocalStat(3, now - 60_000), "base", now))
        assertFalse(idleStandalone(entry().copy(grantedAt = now - 60_000), stat, "base", now))
        // Offline working copies keep the old rule (finished by hand).
        assertFalse(idleStandalone(entry(standalone = false), stat, "base", now))
        // Something still on its way or refused: never.
        assertFalse(idleStandalone(entry().copy(pending = EditMarker(UploadKind.Version, EditSnapshot(3, 0, "x"), "u", at = now)), stat, "base", now))
        assertFalse(idleStandalone(entry().copy(refusedSnapshots = listOf(EditMarker(UploadKind.Version, EditSnapshot(3, 0, "x"), "u", at = now))), stat, "base", now))
    }

    @Test fun finishedStandaloneCopiesGoAfterADay() {
        val stat = LocalStat(3, now - 3 * day)
        assertEquals(EditStep.Expire, planEdit(entry(retiredAt = now - day - 1), stat, "base", now, explicit = false, retryErrors = true))
        assertEquals(EditStep.Unchanged, planEdit(entry(retiredAt = now - day + 60_000), stat, "base", now, explicit = false, retryErrors = true))
        // Offline working copies are kept a week.
        assertEquals(EditStep.Unchanged, planEdit(entry(standalone = false, retiredAt = now - 2 * day), stat, "base", now, explicit = false, retryErrors = true))
        // A late write to a finished copy still goes up (as a conflict copy, see planTarget).
        assertEquals(EditStep.Upload, planEdit(entry(retiredAt = now - 2 * day), stat, "late", now, explicit = false, retryErrors = true))
    }

    @Test fun treeRightIsKeptWhileABatchNeedsIt() {
        val tree = "content://com.android.externalstorage.documents/tree/primary%3AFotos"
        val batch = """{"items":[{"uri":"content://com.android.externalstorage.documents/tree/primary%3AFotos/document/primary%3AFotos%2Fa.jpg"}]}"""
        assertTrue(FolderUpload.needed(tree, listOf(batch)))
        assertFalse(FolderUpload.needed(tree, listOf("""{"items":[{"uri":"content://media/external/images/1"}]}""")))
        // Another tree that only starts with the same text.
        assertFalse(FolderUpload.needed("content://com.android.externalstorage.documents/tree/primary%3AFo", listOf(batch)))
    }
}
