package io.github.svenbuild.fylvo.android.work

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File
import java.util.UUID

/**
 * Fylvo: the files of one upload, kept in a small file of the app instead of the WorkManager
 * input. WorkManager allows only 10 KB of input, which about a hundred picked photos already
 * exceed (that crashed 1.0.0). The batch also remembers which files are done and the server
 * upload session of the file in progress, so a run that was stopped (app killed, no network)
 * goes on where it stopped and can cancel the half-finished session on the server.
 */
@Serializable
data class UploadBatch(
    val id: String,
    val parentUri: String,
    val overwrite: Boolean = false,
    val items: List<UploadItem>,
    /** The account it was queued for ("server email"); a batch never uploads into another. */
    val account: String? = null,
    /** Runs in a row that ended in server trouble without finishing a file; ends the batch at a limit. */
    val serverRetries: Int = 0,
) {
    /** Index of the next file to upload, or -1 when every file is done or failed. */
    fun next(): Int = items.indexOfFirst { it.state == UploadItem.State.Pending }
    val uploaded: Int get() = items.count { it.state == UploadItem.State.Done }
    val failed: Int get() = items.count { it.state == UploadItem.State.Failed }

    fun with(index: Int, change: (UploadItem) -> UploadItem): UploadBatch =
        copy(items = items.mapIndexed { i, item -> if (i == index) change(item) else item })
}

@Serializable
data class UploadItem(
    /** The content:// address the person picked. */
    val uri: String,
    /** The name the file gets on the server. */
    val name: String,
    val state: State = State.Pending,
    /** Upload session of this file on the server while it is being sent, to cancel it if needed. */
    val sessionId: String? = null,
    /** The file's address on the server, which the session belongs to. */
    val targetUri: String? = null,
    /** An earlier run was stopped while this file was being sent (its session was cancelled). */
    val interrupted: Boolean = false,
    /** Replace an existing file of this name; null means the batch's setting. */
    val overwrite: Boolean? = null,
    /** Bytes being sent with [sessionId]; a server file of this size means the upload finished. */
    val uploadSize: Long = 0,
    /** Runs this file stopped with server trouble (not a lost network); it is given up after a few. */
    val serverRetries: Int = 0,
) {
    @Serializable
    enum class State { Pending, Done, Failed }
}

/** Batches on disk, one JSON file each. Writes go to a temporary file first, then replace. */
class UploadQueue(private val dir: File) {

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    fun create(
        uris: List<String>,
        names: List<String>,
        parentUri: String,
        overwrite: Boolean,
        account: String? = null,
        id: String = UUID.randomUUID().toString(),
    ): UploadBatch {
        require(uris.size == names.size) { "every file needs a name" }
        val batch = UploadBatch(
            id = id,
            parentUri = parentUri,
            overwrite = overwrite,
            items = uris.zip(names) { uri, name -> UploadItem(uri, name) },
            account = account,
        )
        save(batch)
        return batch
    }

    fun load(id: String): UploadBatch? {
        if (!isValidId(id)) return null
        val file = File(dir, "$id.json")
        return runCatching { json.decodeFromString(UploadBatch.serializer(), file.readText()) }.getOrNull()
    }

    fun save(batch: UploadBatch) {
        require(isValidId(batch.id)) { "bad batch id" }
        dir.mkdirs()
        val tmp = File(dir, "${batch.id}.json.tmp")
        tmp.writeText(json.encodeToString(UploadBatch.serializer(), batch))
        if (!tmp.renameTo(File(dir, "${batch.id}.json"))) {
            File(dir, "${batch.id}.json").delete()
            check(tmp.renameTo(File(dir, "${batch.id}.json"))) { "could not store the upload list" }
        }
    }

    fun delete(id: String) {
        if (isValidId(id)) File(dir, "$id.json").delete()
    }

    /** Ids of all stored batches. */
    fun ids(): List<String> =
        dir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name.removeSuffix(".json") }.filter(::isValidId)

    /** Batches whose file is older than [maxAgeMillis], for clean-up of ones no job runs for. */
    fun idsOlderThan(maxAgeMillis: Long, now: Long = System.currentTimeMillis()): List<String> =
        dir.listFiles { f -> f.name.endsWith(".json") }.orEmpty()
            .filter { now - it.lastModified() > maxAgeMillis }
            .map { it.name.removeSuffix(".json") }
            .filter(::isValidId)

    private fun isValidId(id: String) = runCatching { UUID.fromString(id).toString() == id }.getOrDefault(false)
}
