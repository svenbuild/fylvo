package io.github.svenbuild.fylvo.android.data.offline

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PlanSyncTest {

    private fun remote(id: String, path: String, entity: String, size: Long = 10) =
        RemoteFile(id, path, entity, size, "cloudreve://my/Root/$path")

    private fun entry(id: String, path: String, entity: String, size: Long = 10) =
        OfflineEntry(id = id, path = path, entity = entity, size = size, localSize = size, localMtime = 1)

    /** Every indexed file is on the phone with its recorded size. */
    private fun allLocal(index: List<OfflineEntry>): (String) -> LocalStat? {
        val sizes = index.associate { it.path to it.localSize }
        return { path -> sizes[path]?.let { LocalStat(it, 1) } }
    }

    @Test fun newRemoteFileIsDownloaded() {
        val file = remote("1", "a.txt", "e1")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), emptyList()) { null }
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun unchangedFileNeedsNothing() {
        val index = listOf(entry("1", "a.txt", "e1"))
        val actions = planSync(RemoteListing(listOf(remote("1", "a.txt", "e1")), emptyList(), true), index, allLocal(index))
        assertEquals(emptyList<SyncAction>(), actions)
    }

    @Test fun changedEntityIsDownloaded() {
        val index = listOf(entry("1", "a.txt", "e1"))
        val file = remote("1", "a.txt", "e2", size = 12)
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), index, allLocal(index))
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun sameIdAtNewPathIsAMove() {
        val index = listOf(entry("1", "old/a.txt", "e1"))
        val file = remote("1", "new/a.txt", "e1")
        val actions = planSync(RemoteListing(listOf(file), listOf("new"), true), index, allLocal(index))
        assertEquals(listOf(SyncAction.Move("old/a.txt", file)), actions)
    }

    @Test fun movedAndChangedIsMovedThenDownloaded() {
        val index = listOf(entry("1", "a.txt", "e1"))
        val file = remote("1", "b.txt", "e2")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), index, allLocal(index))
        assertEquals(listOf(SyncAction.Move("a.txt", file), SyncAction.Download(file)), actions)
    }

    @Test fun deletedFromCompleteListingIsDeletedAfterOneLookupOfTheTopmostFolder() {
        val index = listOf(entry("1", "gone/x/a.txt", "e1"), entry("2", "gone/b.txt", "e2"), entry("3", "keep/c.txt", "e3"))
        val listing = RemoteListing(listOf(remote("3", "keep/c.txt", "e3")), listOf("keep"), true)
        val actions = planSync(listing, index, allLocal(index))
        assertEquals(listOf(SyncAction.Delete("gone", listOf("gone/x/a.txt", "gone/b.txt"))), actions)
    }

    @Test fun deletedFileInAFolderThatStaysIsConfirmedByItself() {
        val index = listOf(entry("1", "keep/a.txt", "e1"))
        val actions = planSync(RemoteListing(emptyList(), listOf("keep"), true), index, allLocal(index))
        assertEquals(listOf(SyncAction.Delete("keep/a.txt", listOf("keep/a.txt"))), actions)
    }

    @Test fun incompleteListingNeverDeletes() {
        val index = listOf(entry("1", "a.txt", "e1"), entry("2", "b.txt", "e2"))
        val file = remote("2", "b.txt", "e3")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), complete = false), index, allLocal(index))
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun rootGoneOrFailedListingDoesNothing() {
        val index = listOf(entry("1", "a.txt", "e1"))
        assertEquals(emptyList<SyncAction>(), planSync(null, index, allLocal(index)))
    }

    @Test fun missingLocalCopyIsDownloadedAgain() {
        val index = listOf(entry("1", "a.txt", "e1"))
        val file = remote("1", "a.txt", "e1")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), index) { null }
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun localCopyOfOtherSizeIsDownloadedAgain() {
        val index = listOf(entry("1", "a.txt", "e1", size = 10))
        val file = remote("1", "a.txt", "e1", size = 10)
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), index) { LocalStat(3, 1) }
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun unknownEntityIsVerifiedByADownloadNeverTreatedAsLocalChange() {
        val index = listOf(OfflineEntry(id = "", path = "a.txt", entity = "", size = 10, state = EntryState.Pending))
        val file = remote("1", "a.txt", "e1")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), index) { LocalStat(10, 1) }
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun localFileWithoutIndexEntryIsDownloadedNotTrusted() {
        val file = remote("1", "a.txt", "e1")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), emptyList()) { LocalStat(10, 1) }
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun relativePathRejectsUnsafeSegments() {
        assertEquals("a/b.txt", relativePath("cloudreve://my/R", "cloudreve://my/R/a/b.txt"))
        assertEquals(null, relativePath("cloudreve://my/R", "cloudreve://my/Rx/a.txt"))
        assertEquals(null, relativePath("cloudreve://my/R", "cloudreve://my/R/../a.txt"))
        assertEquals(null, relativePath("cloudreve://my/R", "cloudreve://my/R"))
    }

    @Test fun freeSpaceKeepsOneGigabyte() {
        val gb = OFFLINE_RESERVE_BYTES
        assertTrue(hasRoomFor(size = gb, freeBytes = 2 * gb))
        assertTrue(!hasRoomFor(size = gb + 1, freeBytes = 2 * gb))
        assertTrue(!hasRoomFor(size = 0, freeBytes = gb - 1))
    }

    @Test fun accountKeysSeparateAccounts() {
        val a = accountKey("https://cloud.example", "u1", "a@example.org")
        assertEquals(a, accountKey("https://cloud.example/", "u1", "other@example.org"))
        assertTrue(a != accountKey("https://cloud.example", "u2", "a@example.org"))
        assertTrue(a != accountKey("https://other.example", "u1", "a@example.org"))
        assertEquals(accountKey("https://c", "", "A@Example.org"), accountKey("https://c", "", "a@example.org"))
        assertTrue(accountKey("https://c", "", "a@example.org") != accountKey("https://c", "", "b@example.org"))
    }
}
