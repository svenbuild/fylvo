package io.github.svenbuild.fylvo.android.data.offline

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class EditStoreTest {
    @get:Rule val tmp = TemporaryFolder()

    private fun entry(id: String = EditStore.newId(), name: String = "a.txt", retiredAt: Long = 0, pending: EditMarker? = null, state: EditState = EditState.Editing) =
        EditEntry(
            id = id, fileId = "f-$id", uri = "cloudreve://my/$name", name = name, baseEntity = "e1",
            base = EditSnapshot(1, 2, "h"), state = state, retiredAt = retiredAt, pending = pending,
        )

    private fun EditStore.write(entry: EditEntry, text: String = "x"): File =
        fileOf(entry).apply { parentFile!!.mkdirs(); writeText(text) }

    @Test fun indexRoundTripWithoutLeftovers() {
        val store = EditStore(tmp.root, "acc")
        val marker = EditMarker(UploadKind.Conflict, EditSnapshot(3, 4, "s"), "cloudreve://my", name = "a (x).txt", sessionId = "sid", sessionUri = "u", entityId = "e", at = 9)
        val a = entry(pending = marker)
        val b = entry(name = "b.md", retiredAt = 7).copy(problem = EditProblem.ServerFull, conflict = "c", seen = EditSeen(1, 2, 3))
        store.put(a)
        store.put(b)
        assertEquals(EditIndex(listOf(a, b)), EditStore(tmp.root, "acc").load())
        assertFalse(File(tmp.root, "offline-edit/index/acc.json.tmp").exists())
        assertTrue(store.hasEntries())
    }

    @Test fun putReplacesAndRemoveDrops() {
        val store = EditStore(tmp.root, "acc")
        val a = entry()
        store.put(a)
        store.put(a.copy(state = EditState.Synced))
        assertEquals(listOf(a.copy(state = EditState.Synced)), store.load().entries)
        assertEquals(a.copy(state = EditState.Synced), store.entry(a.id))
        store.remove(a.id)
        assertNull(store.entry(a.id))
        assertFalse(store.hasEntries())
    }

    @Test fun aBrokenIndexIsNeverWrittenOver() {
        val store = EditStore(tmp.root, "acc")
        val a = entry()
        store.put(a)
        val file = File(tmp.root, "offline-edit/index/acc.json")
        file.writeText("{broken")
        assertTrue(runCatching { store.load() }.isFailure)
        assertTrue(runCatching { store.put(entry()) }.isFailure)
        assertEquals("{broken", file.readText())
        // It may hold entries: the account keeps its background work.
        assertTrue(store.hasEntries())
    }

    @Test fun noIndexYetIsEmpty() {
        assertEquals(EditIndex(), EditStore(tmp.root, "acc").load())
        assertFalse(EditStore(tmp.root, "acc").hasEntries())
    }

    @Test fun aStoppedSaveLeavesTheOldIndex() {
        val store = EditStore(tmp.root, "acc")
        val a = entry()
        store.put(a)
        // A crash after writing the temporary file, before the rename.
        File(tmp.root, "offline-edit/index/acc.json.tmp").writeText("{half")
        assertEquals(listOf(a), store.load().entries)
        // The next save goes through.
        store.put(a.copy(state = EditState.Synced))
        assertEquals(EditState.Synced, store.entry(a.id)?.state)
    }

    @Test fun unknownFieldsAreIgnored() {
        val store = EditStore(tmp.root, "acc")
        val a = entry()
        store.put(a)
        val file = File(tmp.root, "offline-edit/index/acc.json")
        file.writeText(file.readText().replace("{\"entries\":[{", "{\"future\":1,\"entries\":[{\"later\":2,"))
        assertEquals(listOf(a), store.load().entries)
    }

    @Test fun finishMovesTheFolderToDoneAndIsSeenAfterACrash() {
        val store = EditStore(tmp.root, "acc")
        val a = entry()
        store.put(a)
        store.write(a, "edited")
        assertFalse(store.isOnlyInDone(a))
        assertTrue(store.moveToDone(a))
        // Crash before the index says retired: the copy is in done/ only.
        assertTrue(store.isOnlyInDone(a))
        val retired = a.copy(retiredAt = 5)
        assertEquals("edited", store.fileOf(retired).readText())
        // Idempotent.
        assertTrue(store.moveToDone(a))
        assertTrue(store.orphans(store.load()).isEmpty())
    }

    @Test fun orphansAreAdoptedIntoDone() {
        val store = EditStore(tmp.root, "acc")
        val lost = File(tmp.root, "offline-edit/files/acc/some-id/lost.txt").apply { parentFile!!.mkdirs(); writeText("late") }
        val orphans = store.orphans(store.load())
        assertEquals(listOf(lost), orphans)
        val adopted = entry(name = "lost.txt", retiredAt = 1)
        assertTrue(store.adoptOrphan(lost, adopted))
        store.put(adopted)
        assertEquals("late", store.fileOf(adopted).readText())
        assertFalse(File(tmp.root, "offline-edit/files/acc/some-id").exists())
        assertTrue(store.orphans(store.load()).isEmpty())
    }

    @Test fun cleanTmpKeepsWhatAnEntryNeeds() {
        val store = EditStore(tmp.root, "acc")
        val withMarker = entry(pending = EditMarker(UploadKind.Version, EditSnapshot(1, 1, "s"), "t", at = 1))
        val creating = entry(state = EditState.Creating)
        val plain = entry()
        listOf(withMarker, creating, plain).forEach { store.put(it) }
        store.tmpFolder()
        val keep1 = store.snapshotOf(withMarker).apply { writeText("s") }
        val keep2 = store.creatingOf(creating).apply { writeText("c") }
        val drop1 = store.snapshotOf(plain).apply { writeText("old") }
        val drop2 = File(store.tmpFolder(), "creating-gone").apply { writeText("x") }
        store.cleanTmp(store.load())
        assertTrue(keep1.exists())
        assertTrue(keep2.exists())
        assertFalse(drop1.exists())
        assertFalse(drop2.exists())
    }

    @Test fun deleteFilesRemovesEveryPlace() {
        val store = EditStore(tmp.root, "acc")
        val a = entry()
        store.write(a)
        store.tmpFolder()
        store.snapshotOf(a).writeText("s")
        store.creatingOf(a).writeText("c")
        File(tmp.root, "offline-edit/done/acc/${a.id}").mkdirs()
        store.deleteFiles(a)
        assertFalse(store.activeFileOf(a).exists())
        assertFalse(store.snapshotOf(a).exists())
        assertFalse(store.creatingOf(a).exists())
        assertFalse(File(tmp.root, "offline-edit/done/acc/${a.id}").exists())
    }

    @Test fun accountsAreKeptApart() {
        val a = EditStore(tmp.root, "one")
        val b = EditStore(tmp.root, "two")
        a.put(entry())
        assertTrue(a.hasEntries())
        assertFalse(b.hasEntries())
    }

    @Test fun adoptionMovesItemByItemAndIsIdempotent() {
        val from = EditStore(tmp.root, "email")
        val to = EditStore(tmp.root, "id")
        val a = entry()
        val b = entry(name = "b.txt")
        from.put(a)
        from.put(b)
        from.write(a, "A")
        from.write(b, "B")
        // A move that stopped half way: b's folder already went, the index not yet.
        File(tmp.root, "offline-edit/files/email/${b.id}").renameTo(File(tmp.root, "offline-edit/files/id/${b.id}").apply { parentFile!!.mkdirs() })
        to.put(b)
        EditStore.adoptAccount(tmp.root, "email", "id")
        assertEquals(setOf(a.id, b.id), to.load().entries.map { it.id }.toSet())
        assertEquals(2, to.load().entries.size)
        assertEquals("A", to.fileOf(a).readText())
        assertEquals("B", to.fileOf(b).readText())
        assertFalse(from.hasEntries())
        // Once more changes nothing.
        EditStore.adoptAccount(tmp.root, "email", "id")
        assertEquals(2, to.load().entries.size)
    }

    @Test fun adoptionNeverOverwrites() {
        val from = EditStore(tmp.root, "email")
        val to = EditStore(tmp.root, "id")
        val a = entry()
        from.write(a, "old")
        to.write(a, "new")
        EditStore.adoptAccount(tmp.root, "email", "id")
        assertEquals("new", to.fileOf(a).readText())
        // The other one is not deleted.
        assertEquals("old", from.fileOf(a).readText())
    }

    @Test fun adoptionLeavesEverythingWhenAnIndexCannotBeRead() {
        val from = EditStore(tmp.root, "email")
        val to = EditStore(tmp.root, "id")
        val a = entry()
        from.put(a)
        from.write(a, "A")
        File(tmp.root, "offline-edit/index/id.json").apply { parentFile!!.mkdirs(); writeText("{broken") }
        EditStore.adoptAccount(tmp.root, "email", "id")
        assertEquals("A", from.fileOf(a).readText())
        assertEquals(listOf(a), from.load().entries)
    }

    @Test fun hashAndCopy() {
        val f = tmp.newFile("x").apply { writeText("abc") }
        assertEquals("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", EditStore.sha256(f))
        val out = java.io.ByteArrayOutputStream()
        assertEquals(EditStore.sha256(f), EditStore.copyHashing(f.inputStream(), out))
        assertEquals("abc", out.toString())
    }

    @Test fun safeNames() {
        assertEquals("a_b", EditStore.safeName("a/b"))
        assertEquals("file", EditStore.safeName(".."))
        assertEquals("file", EditStore.safeName(""))
        assertEquals("Bericht.docx", EditStore.safeName("Bericht.docx"))
    }
}
