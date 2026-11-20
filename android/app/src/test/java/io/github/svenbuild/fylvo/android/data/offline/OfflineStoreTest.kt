package io.github.svenbuild.fylvo.android.data.offline

import io.github.svenbuild.fylvo.android.data.FavoriteEntry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class OfflineStoreTest {
    @get:Rule val tmp = TemporaryFolder()

    private fun root(id: String = OfflineStore.newId(), uri: String = "cloudreve://my/Photos") =
        OfflineRoot(id = id, uri = uri, name = "Photos", folder = true, createdAt = 1)

    @Test fun indexRoundTrip() {
        val store = OfflineStore(tmp.root, "acc")
        val index = OfflineIndex(root(), listOf(OfflineEntry("1", "a/b.jpg", "e1", 5, 5, 9)), listOf("a"))
        store.save(index)
        assertEquals(index, store.load(index.root.id))
        assertEquals(listOf(index), store.indexes())
        assertFalse(File(tmp.root, "offline-index/acc/${index.root.id}.json.tmp").exists())
    }

    @Test fun unknownFieldsAreIgnoredForLaterVersions() {
        val store = OfflineStore(tmp.root, "acc")
        val r = root()
        store.save(OfflineIndex(r))
        val file = File(tmp.root, "offline-index/acc/${r.id}.json")
        file.writeText(file.readText().replace("{\"root\":{", "{\"future\":1,\"root\":{\"workingCopy\":\"x\","))
        assertEquals(r, store.load(r.id)?.root)
    }

    @Test fun deleteRemovesIndexAndCopies() {
        val store = OfflineStore(tmp.root, "acc")
        val r = root()
        store.save(OfflineIndex(r))
        store.fileOf(r.id, "a.txt").apply { parentFile!!.mkdirs(); writeText("x") }
        store.delete(r.id)
        assertNull(store.load(r.id))
        assertFalse(store.dirOf(r.id).exists())
    }

    @Test fun accountsDoNotSeeEachOther() {
        val a = OfflineStore(tmp.root, accountKey("https://c", "1", ""))
        val b = OfflineStore(tmp.root, accountKey("https://c", "2", ""))
        a.save(OfflineIndex(root()))
        assertEquals(1, a.indexes().size)
        assertEquals(0, b.indexes().size)
    }

    @Test fun writerBatchesWrites() {
        val store = OfflineStore(tmp.root, "acc")
        val r = root()
        var now = 0L
        val saved = mutableListOf<Int>()
        val writer = IndexWriter(store, OfflineIndex(r), onSaved = { saved += it.entries.size }, clock = { now }, batchSize = 3, intervalMs = 5_000)
        writer.put(OfflineEntry("1", "a", "e", 1, 1))
        writer.put(OfflineEntry("2", "b", "e", 1, 1))
        assertNull(store.load(r.id))
        writer.put(OfflineEntry("3", "c", "e", 1, 1))
        assertEquals(3, store.load(r.id)!!.entries.size)
        writer.put(OfflineEntry("4", "d", "e", 1, 1))
        now = 5_000
        writer.put(OfflineEntry("5", "e", "e", 1, 1))
        assertEquals(5, store.load(r.id)!!.entries.size)
        writer.remove("a")
        writer.flush()
        assertEquals(4, store.load(r.id)!!.entries.size)
        assertEquals(listOf(3, 5, 4), saved)
    }

    @Test fun writerDoesNotWriteARootSwitchedOff() {
        val store = OfflineStore(tmp.root, "acc")
        val r = root()
        var on = true
        val writer = IndexWriter(store, OfflineIndex(r), canWrite = { on })
        writer.put(OfflineEntry("1", "a", "e", 1, 1))
        on = false
        writer.flush()
        assertNull(store.load(r.id))
    }

    @Test fun crashResumeRedownloadsUnindexedCopy() {
        // A run stopped after the copy was renamed into place but before the index was written.
        val store = OfflineStore(tmp.root, "acc")
        val r = root()
        store.save(OfflineIndex(r))
        val copy = store.fileOf(r.id, "a.txt").apply { parentFile!!.mkdirs(); writeText("0123456789") }
        val file = RemoteFile("1", "a.txt", "e1", 10, "cloudreve://my/Photos/a.txt")
        val actions = planSync(RemoteListing(listOf(file), emptyList(), true), store.load(r.id)!!.entries) {
            LocalStat(copy.length(), copy.lastModified())
        }
        assertEquals(listOf(SyncAction.Download(file)), actions)
    }

    @Test fun migrationIsIdempotent() {
        val store = OfflineStore(tmp.root, "acc")
        val legacy = listOf(
            FavoriteEntry("cloudreve://my/Docs/a%20b.pdf", "a b.pdf", 7, "/data/offline/1_a b.pdf", 1),
            FavoriteEntry("cloudreve://my/c.txt", "c.txt", 3, "/data/offline/2_c.txt", 1),
        )
        assertEquals(2, migrateFavorites(legacy, store))
        // Stopped before the old list was cleared: the next start adds nothing twice.
        assertEquals(0, migrateFavorites(legacy, store))
        val roots = store.indexes()
        assertEquals(2, roots.size)
        val pdf = roots.first { it.root.name == "a b.pdf" }
        assertFalse(pdf.root.folder)
        assertEquals("/data/offline/1_a b.pdf", pdf.root.legacyFile)
        assertEquals(listOf("a b.pdf"), pdf.entries.map { it.path })
        assertEquals("", pdf.entries.single().entity)
        assertTrue(pdf.entries.single().localSize < 0)
    }

    @Test fun offlineListingShowsDirectChildrenWithEmptyFolders() {
        val index = OfflineIndex(
            root(),
            listOf(OfflineEntry("1", "a.jpg", "e", 1, 1), OfflineEntry("2", "sub/b.jpg", "e", 1, 1), OfflineEntry("3", "c.jpg", "e", 1, -1)),
            listOf("sub", "empty", "sub/deeper"),
        )
        val top = listOffline(index, "", "cloudreve://my/Photos")
        assertEquals(listOf("empty", "sub", "a.jpg"), top.map { it.name })
        assertEquals("cloudreve://my/Photos/sub", top[1].path)
        assertEquals(listOf("deeper", "b.jpg"), listOffline(index, "sub", "cloudreve://my/Photos/sub").map { it.name })
    }

    @Test fun orphanCopiesAreFound() {
        val store = OfflineStore(tmp.root, "acc")
        val kept = root()
        store.save(OfflineIndex(kept))
        store.dirOf(kept.id).mkdirs()
        val orphan = store.dirOf(OfflineStore.newId()).apply { mkdirs() }
        assertEquals(listOf(orphan), store.orphanDirs())
    }

    @Test fun dataOfAnOldSessionMovesToTheIdKeyAndOtherAccountsGo() {
        val emailKey = accountKey("https://c", "", "a@example.org")
        val idKey = accountKey("https://c", "42", "a@example.org")
        val old = OfflineStore(tmp.root, emailKey)
        val r = root()
        old.save(OfflineIndex(r))
        old.fileOf(r.id, "a.txt").apply { parentFile!!.mkdirs(); writeText("x") }
        val other = OfflineStore(tmp.root, "someone-else")
        other.save(OfflineIndex(root()))

        OfflineStore.adoptAccount(tmp.root, emailKey, idKey)
        OfflineStore.deleteOtherAccounts(tmp.root, idKey)

        val now = OfflineStore(tmp.root, idKey)
        assertEquals(listOf(r.id), now.indexes().map { it.root.id })
        assertEquals("x", now.fileOf(r.id, "a.txt").readText())
        assertEquals(0, old.indexes().size)
        assertEquals(0, other.indexes().size)
        assertEquals(listOf(idKey), File(tmp.root, "offline/files").list()!!.toList())
    }

    @Test fun existingIdKeyDataIsNotOverwritten() {
        val idStore = OfflineStore(tmp.root, "id")
        val mine = root()
        idStore.save(OfflineIndex(mine))
        OfflineStore(tmp.root, "email").save(OfflineIndex(root()))
        OfflineStore.adoptAccount(tmp.root, "email", "id")
        assertEquals(listOf(mine.id), idStore.indexes().map { it.root.id })
    }

    @Test fun unindexedLeftoversAreRemoved() {
        val store = OfflineStore(tmp.root, "acc")
        val r = root()
        fun put(rel: String) = store.fileOf(r.id, rel).apply { parentFile!!.mkdirs(); writeText("x") }
        put("keep.txt")
        put("sub/keep.jpg")
        put("a/child.jpg") // written before a stop, never indexed
        put("sub/stray.tmp")
        store.fileOf(r.id, "empty").mkdirs()
        val deleted = store.removeUnindexed(r.id, setOf("keep.txt", "sub/keep.jpg"))
        assertEquals(setOf("a", "sub/stray.tmp", "empty"), deleted.toSet())
        assertTrue(store.fileOf(r.id, "keep.txt").isFile)
        assertTrue(store.fileOf(r.id, "sub/keep.jpg").isFile)
        // The path `a` is free again, e.g. for a file the server put there.
        assertFalse(store.fileOf(r.id, "a").exists())
    }
}
