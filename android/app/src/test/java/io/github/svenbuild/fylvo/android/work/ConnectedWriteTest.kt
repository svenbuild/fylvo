package io.github.svenbuild.fylvo.android.work

import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ApiResponse
import io.github.svenbuild.fylvo.android.api.ConnectedUploadState
import io.github.svenbuild.fylvo.android.api.DeleteFileRequest
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.ListFileResponse
import io.github.svenbuild.fylvo.android.api.MoveFileRequest
import io.github.svenbuild.fylvo.android.api.RenameFileRequest
import io.github.svenbuild.fylvo.android.api.UploadSessionResponse
import io.github.svenbuild.fylvo.android.api.decodeApiResponse
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.chunkRetryable
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction
import io.github.svenbuild.fylvo.android.ui.files.SelectionContext
import io.github.svenbuild.fylvo.android.ui.files.selectionActions
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.ErrorText
import io.github.svenbuild.fylvo.android.util.UiText
import io.github.svenbuild.fylvo.android.util.connectedBatches
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.serializer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo: writing in connected storages, with the server's example answers (resources `connected-write/`). */
class ConnectedWriteTest {
    // The app's own settings (ApiClient): defaults are not written.
    private val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }

    private inline fun <reified T> example(name: String): ApiResponse<T> {
        val text = javaClass.classLoader!!.getResource("connected-write/$name.json")!!.readText()
        return decodeApiResponse(json, serializer<T>(), text)
    }

    private val listing by lazy { example<ListFileResponse>("write-list-root").data!! }
    private fun row(name: String) = listing.files.single { it.name == name }

    @Test
    fun bitsOfTheWritableExamples() {
        assertFalse(listing.connected!!.readOnly)
        val root = listing.parent!!
        assertTrue(Connected.canCreateIn(root))
        assertTrue(Connected.canUploadTo(root))
        val notes = row("notes.txt")
        assertTrue(Connected.canRename(notes))
        assertTrue(Connected.canDelete(notes))
        val docs = row("Docs")
        assertTrue(Connected.canCreateIn(docs))
        assertTrue(Connected.canRename(docs))
        // A Google document: nothing to write.
        val sheet = row("Budget.gsheet")
        assertFalse(Connected.canRename(sheet))
        assertFalse(Connected.canDelete(sheet))
    }

    @Test
    fun selectionOffersWhatEveryRowAllows() {
        val ctx = SelectionContext(connected = true)
        val notes = row("notes.txt")
        val one = selectionActions(listOf(notes), ctx)
        assertEquals(listOf(SelectionAction.MOVE, SelectionAction.DOWNLOAD, SelectionAction.DELETE), one.quick)
        assertEquals(listOf(SelectionAction.SELECT_ALL, SelectionAction.DETAILS, SelectionAction.RENAME), one.menu)
        // With the Google document in it: no move, no delete (and no download of the folder).
        val mixed = selectionActions(listOf(notes, row("Budget.gsheet")), ctx)
        assertTrue(mixed.quick.none { it == SelectionAction.MOVE || it == SelectionAction.DELETE })
        assertFalse(SelectionAction.COPY in mixed.quick + mixed.menu)
    }

    @Test
    fun idsAreKeyedByTheListedPath() {
        val ids = Connected.idsOf(listOf(row("notes.txt"), row("Docs")))
        assertEquals(row("notes.txt").id, ids[row("notes.txt").path])
        assertEquals(2, ids.size)
        // Rows of own files never carry ids.
        assertTrue(Connected.idsOf(listOf(FileObject(id = "x", path = "cloudreve://my/a.txt"))).isEmpty())
    }

    @Test
    fun idsAreSentOnlyWhenGiven() {
        val plain = json.encodeToString(RenameFileRequest.serializer(), RenameFileRequest("cloudreve://my/a.txt", "b.txt"))
        assertFalse("ids" in plain)
        val path = "cloudreve://abc@connected/Trial%20x/note.txt"
        val connected = json.encodeToString(MoveFileRequest.serializer(), MoveFileRequest(listOf(path), "cloudreve://abc@connected/Docs", ids = mapOf(path to "cs1")))
        assertTrue(connected, connected.contains("\"ids\":{\"$path\":\"cs1\"}"))
        assertFalse("copy" in connected)
        val delete = json.encodeToString(DeleteFileRequest.serializer(), DeleteFileRequest(listOf(path)))
        assertFalse("ids" in delete)
    }

    @Test
    fun batchesOfAHundredStopAtTheFirstFailure() = runBlocking {
        val paths = (1..250).map { "cloudreve://abc@connected/f$it.txt" }
        val sent = mutableListOf<Int>()
        val all = connectedBatches(paths) { sent += it.size }
        assertEquals(listOf(100, 100, 50), sent)
        assertEquals(250, all.done.size)
        assertNull(all.error)

        // The second request fails for one address (40081): its others and the first request are done.
        var call = 0
        val partly = connectedBatches(paths) { chunk ->
            if (++call == 2) throw ApiException(40081, "batch", listOf(40004), failedPaths = setOf(chunk[3]))
        }
        assertEquals(199, partly.done.size)
        assertFalse(paths[103] in partly.done)
        assertTrue(paths.drop(200).none { it in partly.done }) // never sent
        assertEquals(40081, (partly.error as ApiException).code)

        // Any other failure: nothing of that request counts.
        call = 0
        val failed = connectedBatches(paths) { if (++call == 1) throw ApiException(40925, "x", reason = "changed") }
        assertTrue(failed.done.isEmpty())
    }

    @Test
    fun partialFailureNamesTheAddressAndItsCode() {
        val r = example<Unit>("write-move-partly-failed")
        assertEquals(40081, r.code)
        assertEquals(listOf(40004), r.innerCodes)
        assertEquals(1, r.failedPaths.size)
        assertTrue(r.failedPaths.single().endsWith("/taken.txt"))
        val changed = example<Unit>("error-write-changed")
        assertEquals("changed", changed.reason)
    }

    @Test
    fun uploadAnswers() {
        val session = example<UploadSessionResponse>("upload-session").data!!
        assertTrue(session.sessionId.startsWith("cu-"))
        assertEquals(10485760L, session.chunkSize)
        assertEquals("connected", session.storagePolicy?.id)
        assertTrue(session.uploadUrls.isNullOrEmpty()) // chunks go to the server, one after the other
        val last = example<FileObject>("upload-last-chunk").data!!
        assertTrue(last.primaryEntity.isNotEmpty())
        assertEquals(Connected.CODE_OUTCOME_UNKNOWN, example<Unit>("error-upload-outcome-unknown").code)
    }

    @Test
    fun resumeDecidesFromTheStateRoute() {
        val done = example<ConnectedUploadState>("upload-state-done").data!!
        assertEquals(UploadWorker.Resume.Done, connectedResume(done))
        // Still receiving: cancel first, then decide.
        assertNull(connectedResume(example<ConnectedUploadState>("upload-state-receiving").data!!))
        // Nothing stored: send again.
        assertEquals(UploadWorker.Resume.Resend, connectedResume(ConnectedUploadState("failed", code = 40011)))
        assertEquals(UploadWorker.Resume.Resend, connectedResume(ConnectedUploadState("failed", code = 40923)))
        // A refusal that would come again: its text.
        assertEquals(UploadWorker.Resume.Failed(40004), connectedResume(ConnectedUploadState("failed", code = 40004)))
        assertEquals(UploadWorker.Resume.Failed(40924), connectedResume(ConnectedUploadState("failed", code = 40924)))
        // Not known, or a session the server forgot: never sent again.
        assertEquals(UploadWorker.Resume.Unknown, connectedResume(ConnectedUploadState("unknown")))
        assertEquals(UploadWorker.Resume.Unknown, connectedResume(ConnectedUploadState("failed", code = 40927)))
        assertEquals(UploadWorker.Resume.Unknown, connectedResume(ConnectedUploadState("")))
    }

    @Test
    fun chunkRetries() {
        assertTrue(chunkRetryable(java.io.IOException("lost")))
        assertTrue(chunkRetryable(ApiException(40923, "busy")))
        assertTrue(chunkRetryable(ApiException(503, "HTTP 503")))
        assertFalse(chunkRetryable(ApiException(40927, "unknown")))
        assertFalse(chunkRetryable(ApiException(40011, "gone")))
        assertFalse(chunkRetryable(ApiException(40013, "length")))
        // Retry-After wins (at most 60 s); else 2 s doubling up to 60 s.
        assertEquals(7000L, Connected.retryDelayMs(0, 7))
        assertEquals(60_000L, Connected.retryDelayMs(0, 600))
        assertEquals(listOf(2000L, 4000L, 8000L, 16000L, 32000L, 60000L), (0..5).map { Connected.retryDelayMs(it) })
    }

    @Test
    fun textsOfTheWriteCodes() {
        fun id(code: Int, reason: String? = null) = (ErrorText.of(ApiException(code, "x", reason = reason)) as UiText.Res).id
        assertEquals(R.string.error_connected_full, id(40924))
        assertEquals(R.string.error_connected_outcome_unknown, id(40927))
        assertEquals(R.string.error_connected_changed, id(40925, "changed"))
        assertEquals(R.string.error_exists, id(40004))
        assertEquals(R.string.error_bad_name, id(40052))
    }
}
