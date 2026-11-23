package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.api.SessionTag
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.ByteArrayOutputStream
import java.io.File
import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction
import java.time.LocalDate
import java.time.format.DateTimeFormatter

/**
 * Fylvo 1.6: text and Markdown files edited in the app (design/android-parity-plan.md, 4 and 5).
 * The editor works on exactly the version it loaded: saving sends it as `previous` with
 * `previous_required`, so a newer version or a deleted file is never overwritten unnoticed.
 */
class TextRepository(
    private val api: ApiClient,
    private val files: FileRepository,
    private val cacheDir: File,
    private val session: SessionManager,
) {
    /** Fylvo: a step of an edit whose session ended is not sent any more. */
    private fun ensure(tag: SessionTag) {
        if (!session.isCurrent(tag)) throw kotlinx.coroutines.CancellationException("Session ended")
    }


    /** A loaded text: [entity] is the version it came from, [bom] a UTF-8 byte order mark to keep. */
    data class Loaded(val text: String, val entity: String, val fileId: String, val bom: Boolean)

    class NotText : Exception("Not UTF-8 text")
    class TooLarge : Exception("Too large to edit")

    /** Reads exactly the version [file] names; never an older copy kept on the phone. */
    suspend fun load(file: FileObject, tag: SessionTag): Loaded {
        if (file.size > MAX_BYTES) throw TooLarge()
        if (file.size == 0L) return Loaded("", file.primaryEntity, file.id, bom = false)
        ensure(tag)
        val url = files.contentUrl(file.path, entity = file.primaryEntity)
        ensure(tag)
        // The body is streamed: read it off the main thread.
        val bytes = withContext(Dispatchers.IO) {
            val resp = files.download(url)
            try {
                if (!resp.isSuccessful) throw ApiException(resp.code(), "HTTP ${resp.code()}")
                val body = resp.body() ?: throw ApiException(-1, "Empty response")
                readLimited(body.byteStream())
            } finally {
                resp.body()?.close()
                resp.errorBody()?.close()
            }
        }
        val (text, bom) = decode(bytes)
        return Loaded(text, file.primaryEntity, file.id, bom)
    }

    /**
     * Saves [text] over the file at [uri], based on version [previous] of file [fileId]. Throws the
     * server's answer: 40076 (newer version), 40904 (file gone), 40073 (locked), ...
     */
    suspend fun save(uri: String, fileId: String, text: String, bom: Boolean, previous: String, tag: SessionTag): FileObject {
        ensure(tag)
        // A file without a version: its address must still hold the same file (see the plan).
        if (previous.isEmpty()) {
            val now = runCatching { files.folderInfo(uri) }.getOrElse { e ->
                if (e is ApiException && e.code in GONE) throw ApiException(CODE_GONE, "File gone") else throw e
            }
            if (now.id != fileId) throw ApiException(CODE_GONE, "File replaced")
            if (now.primaryEntity.isNotEmpty()) throw ApiException(CODE_STALE, "File changed")
        }
        val svc = if (Connected.isConnected(uri)) api.editService() else api.service()
        val body = encode(text, bom).toRequestBody(OCTET)
        return svc.putContent(uri, previous.ifEmpty { null }, if (previous.isEmpty()) null else true, body, tag).unwrap()
    }

    /**
     * "Overwrite anyway" after a conflict: only when the address still holds file [fileId]; then
     * based on its current version.
     */
    suspend fun overwrite(uri: String, fileId: String, text: String, bom: Boolean, tag: SessionTag): FileObject {
        ensure(tag)
        val now = runCatching { files.folderInfo(uri) }.getOrElse { e ->
            if (e is ApiException && e.code in GONE) throw ApiException(CODE_GONE, "File gone") else throw e
        }
        if (now.id != fileId) throw ApiException(CODE_GONE, "File replaced")
        return save(uri, fileId, text, bom, now.primaryEntity, tag)
    }

    /**
     * Writes [text] as a new file [name] in [parentUri]; never replaces anything (a taken name
     * answers 40004 or 40905). Returns the new file.
     */
    suspend fun createNew(parentUri: String, name: String, text: String, bom: Boolean = false, tag: SessionTag): FileObject {
        ensure(tag)
        val tmp = withContext(Dispatchers.IO) {
            File(cacheDir, "text-new").apply { mkdirs() }.let { File.createTempFile("new", ".tmp", it) }.also { it.writeBytes(encode(text, bom)) }
        }
        val made = try {
            files.uploadFile(tmp, parentUri, name, overwrite = false)
        } finally {
            tmp.delete()
        }
        // The server answers with the address it stored the file at.
        return files.folderInfo(made.uri.ifEmpty { CrUri.join(parentUri, name) })
    }

    /** [createNew] under the first free name of [copyNames]. */
    suspend fun saveCopy(parentUri: String, name: String, text: String, bom: Boolean, today: LocalDate = LocalDate.now(), tag: SessionTag): FileObject {
        var last: Exception? = null
        for (candidate in copyNames(name, today)) {
            try {
                return createNew(parentUri, candidate, text, bom, tag)
            } catch (e: ApiException) {
                if (e.code != ApiException.CODE_OBJECT_EXISTS && e.code != CODE_NAME_CASE_TAKEN) throw e
                last = e
            }
        }
        throw last ?: ApiException(ApiException.CODE_OBJECT_EXISTS, "No free name")
    }

    companion object {
        /** The largest file the app edits (a bigger text makes the editor slow on a phone). */
        const val MAX_BYTES = 256L * 1024
        const val CODE_STALE = 40076
        const val CODE_GONE = 40904
        const val CODE_NAME_CASE_TAKEN = 40905
        private val GONE = setOf(404, 40016, 40044)
        private val OCTET = "application/octet-stream".toMediaType()
        private val BOM = byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte())

        /** Strict UTF-8 (a broken byte means the file is not plain text); a BOM is noted, not shown. */
        fun decode(bytes: ByteArray): Pair<String, Boolean> {
            val bom = bytes.size >= 3 && bytes[0] == BOM[0] && bytes[1] == BOM[1] && bytes[2] == BOM[2]
            val start = if (bom) 3 else 0
            val decoder = Charsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
            val text = try {
                decoder.decode(ByteBuffer.wrap(bytes, start, bytes.size - start)).toString()
            } catch (_: CharacterCodingException) {
                throw NotText()
            }
            if (text.any { it == '\u0000' }) throw NotText()
            return text to bom
        }

        fun encode(text: String, bom: Boolean): ByteArray = if (bom) BOM + text.toByteArray(Charsets.UTF_8) else text.toByteArray(Charsets.UTF_8)

        /** "notes (copy 2026-11-23).md", then "... 2", "... 3" up to ten tries. */
        fun copyNames(name: String, today: LocalDate): List<String> {
            val dot = name.lastIndexOf('.').takeIf { it > 0 }
            val base = if (dot != null) name.substring(0, dot) else name
            val ext = if (dot != null) name.substring(dot) else ""
            val stamp = today.format(DateTimeFormatter.ISO_LOCAL_DATE)
            return (1..10).map { n -> if (n == 1) "$base (copy $stamp)$ext" else "$base (copy $stamp $n)$ext" }
        }

        private fun readLimited(input: java.io.InputStream): ByteArray {
            val out = ByteArrayOutputStream()
            val buf = ByteArray(16 * 1024)
            input.use {
                while (true) {
                    val n = it.read(buf)
                    if (n < 0) break
                    out.write(buf, 0, n)
                    if (out.size() > MAX_BYTES) throw TooLarge()
                }
            }
            return out.toByteArray()
        }
    }
}
