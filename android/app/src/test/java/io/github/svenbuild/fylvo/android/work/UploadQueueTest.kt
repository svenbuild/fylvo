package io.github.svenbuild.fylvo.android.work

import androidx.work.Data
import androidx.work.workDataOf
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class UploadQueueTest {

    @get:Rule
    val tmp = TemporaryFolder()

    /** Addresses like the ones the system picker hands out for photos. */
    private fun pickedUris(n: Int) = (1..n).map { "content://com.android.providers.media.documents/document/image%3A${1000000000 + it}" }
    private fun names(n: Int) = (1..n).map { "IMG_%05d.jpg".format(it) }

    @Test
    fun aBatchOfHundredsOfFilesIsStoredAndReadBack() {
        val queue = UploadQueue(tmp.root)
        val batch = queue.create(pickedUris(500), names(500), "cloudreve://my/Photos", overwrite = false)

        val loaded = queue.load(batch.id)
        assertNotNull(loaded)
        assertEquals(500, loaded!!.items.size)
        assertEquals("IMG_00500.jpg", loaded.items[499].name)
        assertEquals("cloudreve://my/Photos", loaded.parentUri)
        assertEquals(0, loaded.next())
    }

    @Test
    fun theJobInputStaysSmallWhateverTheNumberOfFiles() {
        // 1.0.0 put every address into the WorkManager input, which allows 10 KB: about a
        // hundred photos crashed the app. Now the input holds only the batch id.
        val queue = UploadQueue(tmp.root)
        val batch = queue.create(pickedUris(1000), names(1000), "cloudreve://my/Photos", overwrite = false)
        val input = workDataOf(UploadWorker.KEY_BATCH_ID to batch.id, UploadWorker.KEY_PARENT_URI to batch.parentUri)
        assertTrue(input.toByteArray().size < 1024)
    }

    @Test
    fun theOldWayOverflowsTheJobInput() {
        // Documents the cause of the 1.0.0 crash with the same kind of addresses.
        try {
            Data.Builder()
                .putStringArray("uris", pickedUris(170).toTypedArray())
                .putStringArray("names", names(170).toTypedArray())
                .build()
            fail("170 files fit into the job input after all")
        } catch (e: IllegalStateException) {
            assertTrue(e.message!!.contains("10240"))
        }
    }

    @Test
    fun progressSurvivesAndTheNextFileIsTheFirstPendingOne() {
        val queue = UploadQueue(tmp.root)
        var batch = queue.create(pickedUris(3), names(3), "cloudreve://my", overwrite = true)
        batch = batch.with(0) { it.copy(state = UploadItem.State.Done) }
        batch = batch.with(1) { it.copy(sessionId = "s-1", targetUri = "cloudreve://my/IMG_00002.jpg") }
        queue.save(batch)

        // A new run (after the app was killed) sees what is done and the open session.
        val again = queue.load(batch.id)!!
        assertEquals(1, again.next())
        assertEquals("s-1", again.items[1].sessionId)
        assertEquals("cloudreve://my/IMG_00002.jpg", again.items[1].targetUri)
        assertEquals(1, again.uploaded)
        assertEquals(true, again.overwrite)

        val finished = again.with(1) { it.copy(state = UploadItem.State.Failed, sessionId = null) }
            .with(2) { it.copy(state = UploadItem.State.Done) }
        assertEquals(-1, finished.next())
        assertEquals(1, finished.failed)
        assertEquals(2, finished.uploaded)
    }

    @Test
    fun deleteAndBadIds() {
        val queue = UploadQueue(tmp.root)
        val batch = queue.create(pickedUris(1), names(1), "cloudreve://my", overwrite = false)
        queue.delete(batch.id)
        assertNull(queue.load(batch.id))
        // Ids come from the job input; anything that is not a plain UUID is ignored.
        assertNull(queue.load("../../shared_prefs/session"))
        queue.delete("../x")
    }

    @Test
    fun oldBatchesAreFoundForCleanUp() {
        val queue = UploadQueue(tmp.root)
        val old = queue.create(pickedUris(1), names(1), "cloudreve://my", overwrite = false)
        val fresh = queue.create(pickedUris(1), names(1), "cloudreve://my", overwrite = false)
        val now = System.currentTimeMillis()
        tmp.root.resolve("${old.id}.json").setLastModified(now - 8L * 24 * 3600 * 1000)
        assertEquals(listOf(old.id), queue.idsOlderThan(7L * 24 * 3600 * 1000, now))
        assertNotNull(queue.load(fresh.id))
    }

    @Test(expected = IllegalArgumentException::class)
    fun everyFileNeedsAName() {
        UploadQueue(tmp.root).create(pickedUris(2), names(1), "cloudreve://my", overwrite = false)
    }

    @Test
    fun idsListsEveryStoredBatch() {
        val queue = UploadQueue(tmp.root)
        val a = queue.create(pickedUris(1), names(1), "cloudreve://my", overwrite = false)
        val b = queue.create(pickedUris(2), names(2), "cloudreve://my", overwrite = false)
        assertEquals(setOf(a.id, b.id), queue.ids().toSet())
        queue.delete(a.id)
        assertEquals(listOf(b.id), queue.ids())
    }
}
