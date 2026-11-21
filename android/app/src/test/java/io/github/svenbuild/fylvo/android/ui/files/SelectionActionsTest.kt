package io.github.svenbuild.fylvo.android.ui.files

import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.COPY
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.DELETE
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.DELETE_FOREVER
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.DETAILS
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.DOWNLOAD
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.MOVE
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.OFFLINE
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.RENAME
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.RESTORE
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.SELECT_ALL
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.SHARE
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction.SHOW_IN_FOLDER
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SelectionActionsTest {
    private fun own(name: String) = FileObject(type = 0, id = name, name = name, path = "cloudreve://my/$name", owned = true)
    private fun upload(name: String) = own(name).copy(metadata = mapOf(FileObject.METADATA_UPLOAD_SESSION_ID to "s"))
    /** A file in someone else's share; capability "" = no right to change it. */
    private fun readOnlyShare(name: String) = FileObject(type = 0, id = name, name = name, path = "cloudreve://abc@share/$name", capability = "")

    @Test
    fun oneOwnFile() {
        val a = selectionActions(listOf(own("a")), SelectionContext(offlineShown = true))
        assertEquals(listOf(SHARE, DOWNLOAD, DELETE), a.quick)
        assertEquals(listOf(SELECT_ALL, DETAILS, RENAME, COPY, MOVE, OFFLINE), a.menu)
    }

    @Test
    fun oneFileInAVirtualViewCanBeShownInItsFolder() {
        val a = selectionActions(listOf(own("a")), SelectionContext(virtual = true))
        assertEquals(listOf(SELECT_ALL, DETAILS, RENAME, COPY, MOVE, SHOW_IN_FOLDER), a.menu)
    }

    @Test
    fun oneReadOnlyShareFileHasNoShareDeleteOrMove() {
        val a = selectionActions(listOf(readOnlyShare("a")), SelectionContext())
        assertEquals(listOf(DOWNLOAD), a.quick)
        assertEquals(listOf(SELECT_ALL, DETAILS), a.menu)
    }

    @Test
    fun severalOwnFiles() {
        val a = selectionActions(listOf(own("a"), own("b")), SelectionContext())
        assertEquals(listOf(MOVE, DOWNLOAD, DELETE), a.quick)
        assertEquals(listOf(SELECT_ALL, COPY), a.menu)
    }

    @Test
    fun severalWithOneReadOnlyOnlyDownload() {
        val a = selectionActions(listOf(own("a"), readOnlyShare("b")), SelectionContext())
        assertEquals(listOf(DOWNLOAD), a.quick)
        assertEquals(listOf(SELECT_ALL), a.menu)
    }

    @Test
    fun trash() {
        val a = selectionActions(listOf(own("a"), own("b")), SelectionContext(inTrash = true))
        assertEquals(listOf(RESTORE, DELETE_FOREVER), a.quick)
        assertEquals(listOf(SELECT_ALL), a.menu)
    }

    @Test
    fun offlineModeHasNoServerActions() {
        val one = selectionActions(listOf(own("a")), SelectionContext(offlineMode = true))
        assertEquals(emptyList<SelectionAction>(), one.quick)
        assertEquals(listOf(SELECT_ALL, DETAILS), one.menu)
        val two = selectionActions(listOf(own("a"), own("b")), SelectionContext(offlineMode = true))
        assertEquals(listOf(SELECT_ALL), two.menu)
    }

    @Test
    fun anUnfinishedUploadAllowsOnlyLocalActions() {
        val one = selectionActions(listOf(upload("a")), SelectionContext(virtual = true))
        assertEquals(emptyList<SelectionAction>(), one.quick)
        assertEquals(listOf(SELECT_ALL, DETAILS, SHOW_IN_FOLDER), one.menu)
        val mixed = selectionActions(listOf(own("a"), upload("b")), SelectionContext())
        assertEquals(emptyList<SelectionAction>(), mixed.quick)
        assertEquals(listOf(SELECT_ALL), mixed.menu)
    }

    @Test
    fun pathsNotListedYetLeaveOnlySelectAll() {
        val none = selectionActions(emptyList(), SelectionContext(), count = 2)
        assertEquals(emptyList<SelectionAction>(), none.quick)
        assertEquals(listOf(SELECT_ALL), none.menu)
        // One of two paths listed: not treated as a single item (no Details, Rename).
        val part = selectionActions(listOf(own("a")), SelectionContext(), count = 2)
        assertEquals(listOf(SELECT_ALL, COPY), part.menu)
        assertEquals(SelectionActions(emptyList(), emptyList()), selectionActions(emptyList(), SelectionContext()))
    }

    @Test
    fun serverActionsWaitForTheListing() {
        assertFalse(selectionActionEnabled(DELETE, 1, busy = true, batchSaving = false))
        assertFalse(selectionActionEnabled(RENAME, 1, busy = true, batchSaving = false))
        assertTrue(selectionActionEnabled(SELECT_ALL, 1, busy = true, batchSaving = false))
        assertTrue(selectionActionEnabled(DETAILS, 1, busy = true, batchSaving = false))
        assertTrue(selectionActionEnabled(DELETE, 1, busy = false, batchSaving = false))
    }

    @Test
    fun onlyOneDownloadOfSeveralAtATime() {
        assertFalse(selectionActionEnabled(DOWNLOAD, 3, busy = false, batchSaving = true))
        assertTrue(selectionActionEnabled(DOWNLOAD, 1, busy = false, batchSaving = true))
        assertTrue(selectionActionEnabled(DOWNLOAD, 3, busy = false, batchSaving = false))
    }

    // ---- drag range ----------------------------------------------------------

    private val order = listOf("a", "b", "c", "d", "e").map(::own)
    private fun p(vararg names: String) = names.map { "cloudreve://my/$it" }.toSet()

    @Test
    fun dragDownSelectsTheRange() {
        assertEquals(p("b", "c", "d"), dragSelection(order, emptySet(), anchor = p("b").single(), current = p("d").single()))
    }

    @Test
    fun dragUpAcrossTheAnchorSelectsTheOtherSide() {
        assertEquals(p("a", "b", "c"), dragSelection(order, emptySet(), anchor = p("c").single(), current = p("a").single()))
        // Back to the anchor: only the anchor.
        assertEquals(p("c"), dragSelection(order, emptySet(), anchor = p("c").single(), current = p("c").single()))
    }

    @Test
    fun theSelectionBeforeTheDragStays() {
        assertEquals(p("e", "a", "b"), dragSelection(order, p("e"), anchor = p("a").single(), current = p("b").single()))
    }

    @Test
    fun uploadsInTheRangeAreSkippedButTheAnchorStays() {
        val withUploads = listOf(upload("a"), own("b"), upload("c"), own("d"))
        val up = withUploads.map { it.path }
        assertEquals(setOf(up[0], up[1], up[3]), dragSelection(withUploads, emptySet(), anchor = up[0], current = up[3]))
    }

    @Test
    fun pathsGoneFromTheListFallOut() {
        val gone = "cloudreve://my/old"
        assertEquals(p("a", "b"), dragSelection(order, setOf(gone), anchor = p("a").single(), current = p("b").single()))
        // An endpoint that is gone counts as the anchor.
        assertEquals(p("a"), dragSelection(order, emptySet(), anchor = p("a").single(), current = gone))
    }

    @Test
    fun theDragEndsWhenTheAnchorIsGone() {
        assertNull(dragSelection(order, emptySet(), anchor = "cloudreve://my/old", current = p("b").single()))
    }

    // ---- info bar ------------------------------------------------------------

    @Test
    fun infoBarSecondLine() {
        assertEquals("a · 2 kB", selectionDetail(listOf(own("a").copy(size = 2048)), folderLabel = "Folder"))
        assertEquals("f · Folder", selectionDetail(listOf(FileObject(type = 1, name = "f", path = "cloudreve://my/f")), folderLabel = "Folder"))
        val folder = FileObject(type = 1, name = "f", path = "cloudreve://my/f", size = 999999)
        assertEquals("3 kB", selectionDetail(listOf(own("a").copy(size = 1024), own("b").copy(size = 2048), folder), folderLabel = "Folder"))
        assertNull(selectionDetail(listOf(own("a"), folder), folderLabel = "Folder"))
    }
}
