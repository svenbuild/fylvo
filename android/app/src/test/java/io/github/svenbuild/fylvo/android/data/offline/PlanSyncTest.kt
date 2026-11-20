package io.github.svenbuild.fylvo.android.data.offline

import io.github.svenbuild.fylvo.android.api.FileObject
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

    @Test fun emptyFileWithoutEntityIsCurrentOnceThere() {
        val index = listOf(entry("1", "empty.txt", "", size = 0))
        val file = remote("1", "empty.txt", "", size = 0)
        assertEquals(emptyList<SyncAction>(), planSync(RemoteListing(listOf(file), emptyList(), true), index, allLocal(index)))
        assertEquals(listOf(SyncAction.Download(file)), planSync(RemoteListing(listOf(file), emptyList(), true), emptyList()) { null })
    }

    @Test fun lookupConfirmsGoneOnTypeChangeOrOtherId() {
        val file = FileObject(type = 0, id = "1")
        val folder = FileObject(type = 1, id = "9")
        assertTrue(confirmsGone(null, expectFolder = false, expectedId = "1"))
        assertTrue(!confirmsGone(file, expectFolder = false, expectedId = "1"))
        // a -> a/ : the file became a folder.
        assertTrue(confirmsGone(folder, expectFolder = false, expectedId = "1"))
        // Reports/ -> Reports : the folder became a file.
        assertTrue(confirmsGone(file, expectFolder = true, expectedId = null))
        assertTrue(!confirmsGone(folder, expectFolder = true, expectedId = null))
        // Another file under the same name.
        assertTrue(confirmsGone(FileObject(type = 0, id = "2"), expectFolder = false, expectedId = "1"))
        assertTrue(!confirmsGone(FileObject(type = 0, id = "2"), expectFolder = false, expectedId = ""))
    }

    private fun index(uri: String, folder: Boolean, entries: List<OfflineEntry>, state: OfflineState = OfflineState.Synced) =
        OfflineIndex(OfflineRoot(id = uri, uri = uri, name = uri, folder = folder, state = state), entries)

    @Test fun nestedRootGoesOnlyWhenTheFolderHoldsThatExactFile() {
        val single = index("cloudreve://my/A/b/c.pdf", false, listOf(entry("7", "c.pdf", "e1")))
        val holds = index("cloudreve://my/A", true, listOf(entry("7", "b/c.pdf", "e1")))
        assertTrue(isCoveredBy(single, holds))
        // Not yet downloaded in the folder.
        assertTrue(!isCoveredBy(single, index("cloudreve://my/A", true, listOf(entry("7", "b/c.pdf", "e1").copy(localSize = -1)))))
        assertTrue(!isCoveredBy(single, index("cloudreve://my/A", true, emptyList())))
        // Another version or another file.
        assertTrue(!isCoveredBy(single, index("cloudreve://my/A", true, listOf(entry("7", "b/c.pdf", "e2")))))
        assertTrue(!isCoveredBy(single, index("cloudreve://my/A", true, listOf(entry("8", "b/c.pdf", "e1")))))
        // Gone from the server: stays its own root.
        assertTrue(!isCoveredBy(single.copy(root = single.root.copy(state = OfflineState.Gone)), holds))
        // Not inside.
        assertTrue(!isCoveredBy(single, index("cloudreve://my/B", true, listOf(entry("7", "b/c.pdf", "e1")))))
    }

    @Test fun nestedFolderRootNeedsAllItsFiles() {
        val inner = index("cloudreve://my/A/b", true, listOf(entry("1", "x.txt", "e1"), entry("2", "y/z.txt", "e2")))
        assertTrue(isCoveredBy(inner, index("cloudreve://my/A", true, listOf(entry("1", "b/x.txt", "e1"), entry("2", "b/y/z.txt", "e2")))))
        assertTrue(!isCoveredBy(inner, index("cloudreve://my/A", true, listOf(entry("1", "b/x.txt", "e1")))))
    }

    @Test fun parallelDownloadsShareTheReserve() {
        val gb = OFFLINE_RESERVE_BYTES
        val space = SpaceReservation()
        val free = gb + 100
        assertTrue(space.tryReserve(60, free))
        // 60 already promised: 60 more would leave less than 1 GB.
        assertTrue(!space.tryReserve(60, free))
        assertTrue(space.tryReserve(40, free))
        space.release(60)
        assertTrue(space.tryReserve(60, free))
    }
}
