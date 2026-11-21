package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.coroutines.runBlocking
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.util.CrUri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException

/** Fylvo: the upload pass against a fake server (uploads, conflicts, recovery). */
class EditPassTest {
    @get:Rule val tmp = TemporaryFolder()

    private var now = 1_800_000_000_000L
    private val uri = "cloudreve://my/Docs/a.txt"

    /** A server that holds files by address and answers uploads as each test says. */
    private class FakeServer : EditServer {
        val files = HashMap<String, FileObject>()
        /** What each upload sent: folder/name, previous, bytes. */
        val sent = ArrayList<Triple<String, String, String>>()
        var onUpload: (target: String, previous: String, session: (String, String, String) -> Unit) -> Unit = { _, _, _ -> }
        var cancelCode: Int? = 0
        var cancels = 0
        /** Where cancels went; a cancel at an address without a file answers 40016. */
        val cancelledAt = ArrayList<String>()

        override suspend fun lookup(uri: String): FileObject? = files[uri]

        override suspend fun upload(file: File, folder: String, name: String, lastModified: Long, previous: String, onSession: (String, String, String) -> Unit) {
            val target = CrUri.join(folder, name)
            sent += Triple(target, previous, file.readText())
            onUpload(target, previous, onSession)
        }

        override suspend fun cancel(sessionId: String, uri: String): Int? {
            cancels++
            cancelledAt += uri
            return if (uri in files) cancelCode else 40016
        }
    }

    private val server = FakeServer()
    private val store by lazy { EditStore(tmp.root, "acc") }
    private val published = ArrayList<EditIndex>()

    private fun pass(phase: EditPhase = EditPhase.End) =
        EditPass(server, store, { null }, phase, "L", { true }, { published += it }, { now })

    /** A working copy made from "v1" (version e1 of file f1), now holding [content], quiet for 10 min. */
    private fun edited(content: String = "v2", pending: EditMarker? = null): EditEntry {
        val entry = EditEntry(
            id = EditStore.newId(), fileId = "f1", uri = uri, name = "a.txt", baseEntity = "e1",
            base = EditSnapshot(2, 0, sha("v1")), state = EditState.Editing, pending = pending,
        )
        store.put(entry)
        store.fileOf(entry).apply {
            parentFile!!.mkdirs()
            writeText(content)
            setLastModified(now - 10 * 60_000)
        }
        return entry
    }

    private fun sha(text: String) = EditStore.copyHashing(text.byteInputStream(), null)

    private fun file(id: String, path: String, primary: String) = FileObject(id = id, path = path, primaryEntity = primary)

    /** Answers a session for [entityId] and then finishes it on the fake server. */
    private fun commits(entityId: String, fileId: String = "f1") { server.onUpload = { target, _, session ->
        session("s-$entityId", target, entityId)
        server.files[target] = file(fileId, target, entityId)
    } }

    @Test fun aQuietChangeBecomesTheNextVersion() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        commits("e2")
        val entry = edited()
        pass().run()
        val after = store.entry(entry.id)!!
        assertEquals(EditState.Synced, after.state)
        assertEquals("e2", after.baseEntity)
        assertEquals(sha("v2"), after.confirmed?.sha256)
        assertNull(after.pending)
        assertEquals(listOf(Triple(uri, "e1", "v2")), server.sent)
        assertFalse(store.snapshotOf(entry).exists())
    }

    @Test fun theIndexIsWrittenBeforeTheSnapshotGoes() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        commits("e2")
        val entry = edited()
        var snapshotAtCommit: Boolean? = null
        val pass = EditPass(server, store, { null }, EditPhase.End, "L", { true }, { index ->
            val e = index.entries.first()
            if (e.state == EditState.Synced && snapshotAtCommit == null) snapshotAtCommit = store.snapshotOf(entry).exists()
        }, { now })
        pass.run()
        assertEquals(true, snapshotAtCommit)
        assertFalse(store.snapshotOf(entry).exists())
    }

    @Test fun aStaleVersionGoesUpAsAConflictCopyAndTheCopyIsRebased() = runBlocking {
        server.files[uri] = file("f1", uri, "e5")
        server.onUpload = { target, previous, session ->
            if (previous.isNotEmpty()) throw ApiException(40076, "stale")
            session("s2", target, "c1")
            server.files[target] = file("f9", target, "c1")
        }
        val entry = edited()
        pass().run()
        val after = store.entry(entry.id)!!
        val copy = server.sent.last().first
        assertTrue(copy, CrUri.fileName(copy).startsWith("a (L "))
        assertEquals("", server.sent.last().second)
        assertEquals("v2", server.sent.last().third)
        assertEquals("f9", after.fileId)
        assertEquals(copy, after.uri)
        assertEquals("c1", after.baseEntity)
        assertEquals(CrUri.fileName(copy), after.conflict)
        assertEquals(EditState.Synced, after.state)
        // The local file keeps its place.
        assertEquals("v2", store.fileOf(after).readText())
    }

    @Test fun aTakenConflictNameTriesTheNextCounter() = runBlocking {
        server.files[uri] = file("f1", uri, "e5")
        var tries = 0
        server.onUpload = { target, previous, session ->
            if (previous.isNotEmpty()) throw ApiException(40076, "stale")
            if (tries++ == 0) throw ApiException(40905, "case")
            session("s2", target, "c1")
            server.files[target] = file("f9", target, "c1")
        }
        val entry = edited()
        pass().run()
        assertTrue(CrUri.fileName(server.sent.last().first).endsWith(" 2).txt"))
        assertEquals("f9", store.entry(entry.id)!!.fileId)
    }

    @Test fun networkTroubleKeepsTheMarkerAndTheSnapshot() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        server.onUpload = { _, _, _ -> throw IOException("offline") }
        val entry = edited()
        val result = pass().run()
        val after = store.entry(entry.id)!!
        assertNotNull(after.pending)
        assertEquals("", after.pending!!.sessionId)
        assertTrue(store.snapshotOf(entry).exists())
        assertTrue(result.again)
        // Later, the kept snapshot goes up, even if the working copy was cut short meanwhile.
        store.fileOf(entry).writeText("")
        now += EDIT_RECOVERY_DELAY_MS
        commits("e2")
        pass().run()
        assertEquals("v2", server.sent.last().third)
        assertEquals(sha("v2"), store.entry(entry.id)!!.confirmed?.sha256)
    }

    private fun recovering(sessionId: String = "s1", sessionUri: String = uri, attempts: Int = 0): EditEntry {
        val marker = EditMarker(
            UploadKind.Version, EditSnapshot(2, now - 10 * 60_000, sha("v2")), target = uri,
            sessionId = sessionId, sessionUri = sessionUri, entityId = "e2", at = now - EDIT_RECOVERY_DELAY_MS,
        )
        val entry = edited(pending = marker).copy(attempts = attempts).also { store.put(it) }
        store.tmpFolder()
        store.snapshotOf(entry).writeText("v2")
        return entry
    }

    @Test fun recoveryConfirmsOnlyTheCurrentVersion() = runBlocking {
        server.files[uri] = file("f1", uri, "e2")
        val entry = recovering()
        pass().run()
        val after = store.entry(entry.id)!!
        assertEquals(1, server.cancels)
        assertEquals("e2", after.baseEntity)
        assertNull(after.pending)
        assertTrue(server.sent.isEmpty())
    }

    @Test fun recoveryOfAnUploadThatDidNotGetInSendsTheKeptSnapshot() = runBlocking {
        // The server still has e1 (our e2 may be listed as an unfinished upload: it proves nothing).
        server.files[uri] = file("f1", uri, "e1")
        val entry = recovering()
        // The working copy was cut short by an editor meanwhile: the snapshot is the whole copy.
        store.fileOf(entry).writeText("v")
        commits("e3")
        pass().run()
        assertEquals(listOf(Triple(uri, "e1", "v2")), server.sent)
        val after = store.entry(entry.id)!!
        assertEquals("e3", after.baseEntity)
        assertEquals(sha("v2"), after.confirmed?.sha256)
        // The shorter working copy is a newer change that waits for its own upload.
        assertEquals(EditState.Uploading, after.state)
    }

    @Test fun recoveryWithoutAClearCancelKeepsEverything() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        server.cancelCode = null
        val entry = recovering()
        val result = pass().run()
        val after = store.entry(entry.id)!!
        assertNotNull(after.pending)
        assertEquals(1, after.attempts)
        assertTrue(store.snapshotOf(entry).exists())
        assertTrue(server.sent.isEmpty())
        assertTrue(result.again)
    }

    @Test fun recoveryWaitsForTheListingWhenTheFileMoved() = runBlocking {
        // Another file at the old address, before this run's listing: wait, never guess.
        server.files[uri] = file("other", uri, "x")
        val entry = recovering()
        val result = pass(EditPhase.Start).run()
        assertNotNull(store.entry(entry.id)!!.pending)
        assertTrue(result.syncWanted)
        assertEquals(0, server.cancels)
        // After the listing it is a conflict copy of the kept snapshot.
        server.onUpload = { target, _, session ->
            session("s9", target, "c1")
            server.files[target] = file("f9", target, "c1")
        }
        pass(EditPhase.End).run()
        assertEquals("", server.sent.last().second)
        assertEquals("v2", server.sent.last().third)
        assertEquals("f9", store.entry(entry.id)!!.fileId)
    }

    @Test fun aLocalFailureDoesNotStopThePass() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        commits("e2")
        val broken = edited()
        val fine = edited()
        // The snapshot of the first cannot be written (a pass for these two, without the tmp clean-up).
        File(store.tmpFolder(), "snap-${broken.id}").mkdirs()
        val result = pass().run(only = setOf(broken.id, fine.id))
        assertEquals(EditState.Synced, store.entry(fine.id)!!.state)
        assertTrue(result.again)
    }

    @Test fun nothingIsSentOnceTheAccountChanged() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        edited()
        EditPass(server, store, { null }, EditPhase.End, "L", { false }, {}, { now }).run()
        assertTrue(server.sent.isEmpty())
    }

    /** Conflict copies (no previous) are finished as file f9; versions as [version]. */
    private fun commitsConflict(version: (String, String, (String, String, String) -> Unit) -> Unit = { _, _, _ -> throw ApiException(40076, "stale") }) {
        server.onUpload = { target, previous, session ->
            if (previous.isNotEmpty()) version(target, previous, session)
            else {
                session("s9", target, "c1")
                server.files[target] = file("f9", target, "c1")
            }
        }
    }

    @Test fun aKeptSnapshotSentAgainNeverGoesToAnotherFileAtTheAddress() = runBlocking {
        // Another file with the same version took the address meanwhile.
        server.files[uri] = file("other", uri, "e1")
        commitsConflict { _, _, _ -> error("must not be a version") }
        val entry = recovering(sessionId = "")
        pass().run()
        assertTrue(server.sent.none { it.second.isNotEmpty() })
        assertEquals("v2", server.sent.single().third)
        assertEquals("f9", store.entry(entry.id)!!.fileId)
    }

    @Test fun aClearRefusalKeepsMarkerAndSnapshotUntilDiscarded() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        server.onUpload = { _, _, _ -> throw ApiException(40049, "too large") }
        val entry = edited()
        pass().run()
        val after = store.entry(entry.id)!!
        assertEquals(EditState.Error, after.state)
        assertEquals(EditProblem.Refused, after.problem)
        assertEquals(40049, after.errorCode)
        assertNotNull(after.pending)
        // The next pass (with its tmp clean-up) keeps the snapshot.
        pass(EditPhase.Check).run()
        assertTrue(store.snapshotOf(entry).exists())
        assertNotNull(store.entry(entry.id)!!.pending)
    }

    @Test fun aRenamedFileIsCancelledAndCheckedAtItsNewAddress() = runBlocking {
        val renamed = "cloudreve://my/Docs/b.txt"
        server.files[renamed] = file("f1", renamed, "e2")
        val entry = recovering(sessionUri = uri)
        EditPass(server, store, { id -> renamed.takeIf { id == "f1" } }, EditPhase.End, "L", { true }, {}, { now }).run()
        assertEquals(listOf(renamed), server.cancelledAt)
        val after = store.entry(entry.id)!!
        assertEquals(renamed, after.uri)
        assertEquals("e2", after.baseEntity)
        assertNull(after.pending)
    }

    @Test fun aDamagedSnapshotIsDroppedAndTheWorkingCopyPlannedAnew() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        val entry = recovering()
        store.snapshotOf(entry).writeText("vX")
        commits("e3")
        pass().run()
        assertEquals(0, server.cancels)
        assertEquals("v2", server.sent.single().third)
        // Only the hash of what was sent counts as confirmed.
        assertEquals(sha("v2"), store.entry(entry.id)!!.confirmed?.sha256)
    }

    @Test fun afterFiveUnclearAnswersTheSnapshotGoesUpAsAConflictCopy() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        server.cancelCode = null
        commitsConflict()
        val entry = recovering(attempts = EDIT_MAX_UNCLEAR - 1)
        pass().run()
        assertEquals("", server.sent.single().second)
        assertEquals("f9", store.entry(entry.id)!!.fileId)
    }

    @Test fun aStopAskedForByANewNetworkSettingIsNoStall() = runBlocking {
        server.files[uri] = file("f1", uri, "e1")
        server.onUpload = { target, _, session ->
            session("s1", target, "e2")
            throw kotlinx.coroutines.CancellationException("stopped")
        }
        val counted = edited()
        runCatching { pass().run(only = setOf(counted.id)) }
        assertEquals(1, store.entry(counted.id)!!.stalls)
        val notCounted = edited()
        runCatching {
            EditPass(server, store, { null }, EditPhase.End, "L", { true }, {}, { now }, countStall = { false }).run(only = setOf(notCounted.id))
        }
        assertEquals(0, store.entry(notCounted.id)!!.stalls)
        assertNotNull(store.entry(notCounted.id)!!.pending)
    }
}
