package io.github.svenbuild.fylvo.android.data.offline

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class OfflineMovesTest {
    @get:Rule val tmp = TemporaryFolder()

    private val store by lazy { OfflineStore(tmp.root, "acc") }
    private val root = OfflineRoot(id = OfflineStore.newId(), uri = "cloudreve://my/R", name = "R", folder = true)

    private fun copy(path: String, text: String, id: String) =
        store.fileOf(root.id, path).apply { parentFile!!.mkdirs(); writeText(text) }.let {
            OfflineEntry(id = id, path = path, entity = "e$id", size = it.length(), localSize = it.length())
        }

    private fun move(from: String, to: String, id: String) =
        SyncAction.Move(from, RemoteFile(id, to, "e$id", 1, "cloudreve://my/R/$to"))

    @Test fun twoFilesMovingIntoEachOthersNames() {
        val a = copy("a", "A", "1")
        val b = copy("b", "B", "2")
        val writer = IndexWriter(store, OfflineIndex(root, listOf(a, b)))
        val failed = moveCopies(store, writer, root.id, listOf(move("a", "b/foto.jpg", "1"), move("b", "a/cover.jpg", "2")))
        assertEquals(emptyList<SyncAction.Move>(), failed)
        assertEquals("A", store.fileOf(root.id, "b/foto.jpg").readText())
        assertEquals("B", store.fileOf(root.id, "a/cover.jpg").readText())
        assertEquals(setOf("b/foto.jpg", "a/cover.jpg"), writer.current.entries.map { it.path }.toSet())
        assertEquals("1", writer.entry("b/foto.jpg")!!.id)
    }

    @Test fun aFileMovingIntoAFolderOfItsOwnName() {
        val a = copy("a", "A", "1")
        val writer = IndexWriter(store, OfflineIndex(root, listOf(a)))
        assertEquals(emptyList<SyncAction.Move>(), moveCopies(store, writer, root.id, listOf(move("a", "a/photo.jpg", "1"))))
        assertEquals("A", store.fileOf(root.id, "a/photo.jpg").readText())
        assertEquals(listOf("a/photo.jpg"), writer.current.entries.map { it.path })
    }

    @Test fun aBlockedMoveKeepsTheOldCopyAndRemovesTheFoldersItMade() {
        val a = copy("a", "A", "1")
        // A folder with something in it is already at the target.
        store.fileOf(root.id, "x/y/t.jpg").mkdirs()
        store.fileOf(root.id, "x/y/t.jpg/inner").writeText("I")
        val writer = IndexWriter(store, OfflineIndex(root, listOf(a)))
        val m = move("a", "x/y/t.jpg", "1")
        assertEquals(listOf(m), moveCopies(store, writer, root.id, listOf(m)))
        assertEquals("A", store.fileOf(root.id, "a").readText())
        assertEquals(a, writer.entry("a"))
        assertFalse(store.moveTmpOf(root.id, "a").exists())
    }

    @Test fun aBlockedMoveIntoANewFolderLeavesNoFolderBehind() {
        val a = copy("a", "A", "1")
        val b = copy("b", "B", "2")
        val writer = IndexWriter(store, OfflineIndex(root, listOf(a, b)))
        // `b` stays where it is (no move), so the move of `a` to `b` cannot happen.
        val m = move("a", "b", "1")
        assertEquals(listOf(m), moveCopies(store, writer, root.id, listOf(m)))
        assertEquals("A", store.fileOf(root.id, "a").readText())
        assertEquals("B", store.fileOf(root.id, "b").readText())
        assertTrue(writer.entry("a") == a && writer.entry("b") == b)
    }
}
