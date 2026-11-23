package io.github.svenbuild.fylvo.android.data

import okhttp3.MediaType.Companion.toMediaTypeOrNull
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.CapacityResponse
import io.github.svenbuild.fylvo.android.api.CreateFileRequest
import io.github.svenbuild.fylvo.android.api.CreateUploadSessionRequest
import io.github.svenbuild.fylvo.android.api.DeleteFileRequest
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.FileUrlRequest
import io.github.svenbuild.fylvo.android.api.ListFileResponse
import io.github.svenbuild.fylvo.android.api.MoveFileRequest
import io.github.svenbuild.fylvo.android.api.RenameFileRequest
import io.github.svenbuild.fylvo.android.api.RestoreFileRequest
import io.github.svenbuild.fylvo.android.api.UploadSessionResponse
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.ensureActive
import io.github.svenbuild.fylvo.android.api.ApiService
import java.util.concurrent.ConcurrentHashMap
import retrofit2.Response
import java.io.File

/**
 * A refused request: the server's envelope code (or the HTTP status), its message for the log, and
 * for a batch answer (40081) the codes of the single failures. Never shown as it is; see util/ErrorText.
 */
class ApiException(
    val code: Int,
    message: String,
    val innerCodes: List<Int> = emptyList(),
    /** Fylvo: `data.reason` of the answer (connected storages, 40925). */
    val reason: String? = null,
    /** Fylvo: the addresses that failed in a batch answer (40081); the others worked. */
    val failedPaths: Set<String> = emptySet(),
) : Exception(message) {
    companion object {
        /** Server envelope code for "login required" (session expired or revoked). */
        const val CODE_LOGIN_REQUIRED = 401
        /** A file or folder with that name is already there (pkg/serializer/error.go). */
        const val CODE_OBJECT_EXISTS = 40004
        /** The upload session is gone (CodeUploadSessionExpired): the file starts again. */
        const val CODE_UPLOAD_SESSION_EXPIRED = 40011
        /** Another upload or edit holds the file (CodeLockConflict). */
        const val CODE_LOCK_CONFLICT = 40073
        /** No file or folder at that URI (CodeParentNotExist, "Path not exist"). */
        const val CODE_PATH_NOT_EXIST = 40016
    }

    /** Server trouble or an expired session, as opposed to a verdict on one request. */
    val isTransient: Boolean get() = code in 500..599 || code == 429 || code == CODE_LOGIN_REQUIRED ||
        code >= 50000 || code == CODE_UPLOAD_SESSION_EXPIRED
}

private const val CODE_OBJECT_EXISTS = ApiException.CODE_OBJECT_EXISTS
private const val MAX_CHUNK_BYTES = 64L * 1024 * 1024
/** Fylvo: tries of one chunk into a connected storage before the upload waits for a later run. */
private const val CHUNK_TRIES = 5

/** Fylvo: a chunk into a connected storage that may simply be sent again. */
internal fun chunkRetryable(e: Throwable): Boolean =
    e is java.io.IOException || (e is ApiException && (e.code == Connected.CODE_BUSY || e.code == 429 || e.code in 502..504))
/** Fylvo: the server's answer when a file has no preview ("Entity not exist"). */
private const val CODE_NO_THUMB = 40077

/** Signed thumbnail URLs are refetched after this; servers typically sign for ~1 h. */
const val THUMB_TTL_MS = 50L * 60 * 1000

class FileRepository(private val api: ApiClient) {

    /** Fylvo: connected storages (Google Drive, OneDrive through the server). */
    val connected = ConnectedRepository(api)

    suspend fun list(
        uri: String,
        pageToken: String? = null,
        orderBy: String? = null,
        orderDirection: String? = null,
        pageSize: Int = 50,
    ): ListFileResponse =
        readService(uri).listFiles(
            uri = uri,
            pageSize = pageSize,
            nextPageToken = pageToken,
            orderBy = orderBy,
            orderDirection = orderDirection,
        ).unwrap()

    /**
     * Fylvo: whether the folder at [uri] is still the one with [id]: true or false, or null when
     * the server cannot be asked now. Not for a root (its id is never stored, a root cannot be
     * replaced).
     */
    suspend fun folderIdMatches(uri: String, id: String): Boolean? = try {
        val folder = readService(uri).fileInfo(uri).unwrap()
        folder != null && folder.isFolder && folder.id == id
    } catch (e: kotlinx.coroutines.CancellationException) {
        throw e
    } catch (e: ApiException) {
        // Not found: gone. Login needed or server trouble: no answer now (asked again later).
        if (e.code == ApiException.CODE_PATH_NOT_EXIST) false else if (e.isTransient) null else false
    } catch (_: Exception) {
        null
    }

    /** Fylvo: one file or folder as the server describes it now. */
    suspend fun folderInfo(uri: String): FileObject = readService(uri).fileInfo(uri).unwrap()

    /**
     * Fylvo 1.6: requests whose address carries a share password go through the client without
     * redirects (a redirect could hand the address on to another server).
     */
    private suspend fun readService(uri: String): ApiService = if (CrUri.hasSecret(uri)) api.authService() else api.service()

    /** [entity] asks for that exact version of the file (empty = the current one). */
    suspend fun downloadUrl(uri: String, archive: Boolean = false, entity: String = ""): String =
        signedUrl(uri, download = true, archive = archive, entity = entity)

    /** Signed URL served inline (no attachment disposition), for streaming previews. Fylvo: [entity] as above. */
    suspend fun contentUrl(uri: String, entity: String = ""): String = signedUrl(uri, download = false, entity = entity)

    private suspend fun signedUrl(uri: String, download: Boolean, archive: Boolean = false, entity: String = ""): String {
        val url = readService(uri).fileUrls(FileUrlRequest(listOf(uri), download = download, archive = archive, entity = entity))
            .unwrap().urls.firstOrNull()?.url
            // Fylvo: no address in the text (it may carry a share password).
            ?: throw ApiException(-1, "No URL returned")
        return reachableUrl(url)
    }

    /** Fylvo: the server's file type icons (`icons` of the explorer settings), or null. */
    suspend fun explorerIcons(): kotlinx.serialization.json.JsonElement? =
        runCatching { api.service().explorerConfig().unwrap().icons }.getOrNull()

    /** Account storage usage; null when the server does not report it. */
    suspend fun capacity(): CapacityResponse? =
        runCatching { api.service().capacity().unwrap() }.getOrNull()

    /**
     * The server builds signed URLs from its `siteURL` setting. A siteURL of
     * `localhost` (the out-of-box default) is unreachable from any remote
     * client, so loopback URLs get their scheme+host rewritten to the address
     * the user actually connected to. Any other host is trusted — it may be a
     * CDN or a LAN-only name the client can legitimately reach.
     */
    private suspend fun reachableUrl(url: String): String {
        val parsed = runCatching { java.net.URI(url) }.getOrNull() ?: return url
        val loopback = parsed.host == null ||
            parsed.host.equals("localhost", true) ||
            parsed.host.startsWith("127.") ||
            parsed.host == "::1" || parsed.host == "0.0.0.0"
        if (!loopback) return url
        val base = runCatching { java.net.URI(api.serverBase()) }.getOrNull() ?: return url
        return java.net.URI(
            base.scheme, parsed.userInfo, base.host, base.port,
            parsed.path, parsed.query, parsed.fragment,
        ).toString()
    }

    suspend fun download(url: String) = api.service().download(url)

    /**
     * Streams [url] into [dest] through a `.part` sibling, reporting
     * (bytes, total) as it goes; total is -1 when the server sends no length.
     * Cooperative with coroutine cancellation between buffers.
     */
    suspend fun downloadToFile(url: String, dest: File, onProgress: (Long, Long) -> Unit = { _, _ -> }): Unit =
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
        val resp = api.service().download(url)
        if (!resp.isSuccessful) {
            resp.errorBody()?.close()
            throw ApiException(resp.code(), "Download failed: HTTP ${resp.code()}")
        }
        val body = resp.body() ?: throw ApiException(-1, "Empty download")
        val total = body.contentLength()
        var part: File? = null
        try {
            body.byteStream().use { input ->
                dest.parentFile?.mkdirs()
                // Fylvo: a fresh, uniquely named temporary file (created atomically), so it can never
                // be a file someone saved, nor the temporary file of another download running at the
                // same time. Made inside use {}, so the response is closed when that fails.
                val tmp = File.createTempFile("fylvo-", ".part", dest.parentFile).also { part = it }
                java.io.FileOutputStream(tmp).use { out ->
                    val buffer = ByteArray(256 * 1024)
                    var copied = 0L
                    while (true) {
                        kotlinx.coroutines.currentCoroutineContext().ensureActive()
                        val n = input.read(buffer)
                        if (n < 0) break
                        out.write(buffer, 0, n)
                        copied += n
                        onProgress(copied, total)
                    }
                }
            }
            if (part?.renameTo(dest) != true) throw ApiException(-1, "Could not save ${dest.name}")
        } finally {
            part?.delete()
        }
    }

    /** Fylvo: preview requests that outlive the screen that asked first (see [thumbUrl]). */
    private val thumbScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val thumbInFlight = ConcurrentHashMap<String, Deferred<String?>>()
    /** Resolved preview URLs with fetch time, by file id and version (a new version has a new preview). */
    private val thumbCache = ConcurrentHashMap<String, Pair<String, Long>>()
    private val noThumb = ConcurrentHashMap.newKeySet<String>()
    /** The account [thumbCache] and [noThumb] belong to; another one starts them afresh. */
    @Volatile private var thumbAccount = ""

    /**
     * Resolves a thumbnail URL for the file, or null when the server says there is none.
     * Fylvo: any other failure (network, a busy server) throws, so it can be asked again later.
     * One request per file at a time, for the file list and the system file picker together: the
     * server makes a missing preview on the first request; two at once for the same file made it
     * twice, and the losing one deleted the winner's preview file.
     */
    suspend fun thumbUrl(uri: String): String? {
        // Per account: the same address of another account is another file.
        val key = api.accountTag() + "|" + CrUri.canonical(uri)
        val request = thumbInFlight.computeIfAbsent(key) {
            thumbScope.async(start = CoroutineStart.LAZY) {
                try {
                    try {
                        readService(uri).thumb(uri).unwrap().url
                    } catch (e: ApiException) {
                        if (e.code == CODE_NO_THUMB) null else throw e
                    }
                } finally {
                    thumbInFlight.remove(key)
                }
            }
        }
        request.start()
        return request.await()
    }

    /**
     * Fylvo: [thumbUrl] of [file], remembered per file and version for [THUMB_TTL_MS]. Only the
     * server's clear "no preview" is remembered for good; a failure gives null and is asked again.
     */
    suspend fun cachedThumbUrl(file: FileObject): String? {
        val account = api.accountTag()
        if (account != thumbAccount) {
            forgetThumbs()
            thumbAccount = account
        }
        if (hasNoThumb(file)) return null
        val key = thumbKey(file)
        thumbCache[key]?.let { (url, fetchedAt) ->
            if (System.currentTimeMillis() - fetchedAt < THUMB_TTL_MS) return url
        }
        val url = try {
            thumbUrl(file.path)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            return null
        }
        if (url == null) noThumb.add(key) else thumbCache[key] = url to System.currentTimeMillis()
        return url
    }

    /** Fylvo: true once the server said this version of the file has no preview. */
    fun hasNoThumb(file: FileObject): Boolean = !file.mayHaveThumbnail || thumbKey(file) in noThumb

    /** Fylvo: the session ended; previews of the old account are not kept. */
    fun forgetThumbs() {
        thumbCache.clear()
        noThumb.clear()
    }

    private fun thumbKey(file: FileObject) = "${file.id}:${file.primaryEntity}"

    /** Fylvo: inside a connected storage a taken name is refused instead of numbered. */
    suspend fun mkdir(uri: String) {
        writeService(uri).createFile(CreateFileRequest(uri = uri, type = "folder", errOnConflict = Connected.isConnected(uri))).unwrapEmpty()
    }

    /**
     * Fylvo: [ids] (row ids by address) are required inside connected storages and only sent
     * there. Writes there use the client without transport replay, so a lost answer is seen.
     */
    suspend fun rename(uri: String, newName: String, ids: Map<String, String>? = null) {
        writeService(uri).renameFile(RenameFileRequest(uri = uri, newName = newName, ids = ids?.takeIf { it.isNotEmpty() })).unwrapEmpty()
    }

    /** Moves (or copies) [uris] into the folder [dst]. Fylvo: [ids] as for [rename]. */
    suspend fun move(uris: List<String>, dst: String, copy: Boolean, ids: Map<String, String>? = null) {
        writeService(dst).moveFiles(MoveFileRequest(uris = uris, dst = dst, copy = copy, ids = ids?.takeIf { it.isNotEmpty() })).unwrapEmpty()
    }

    private suspend fun writeService(uri: String): ApiService =
        if (Connected.isConnected(uri)) api.editService() else api.service()

    /** Subfolders of [uri] only, for the destination picker. */
    suspend fun listFolders(uri: String): List<FileObject> = listFoldersWithParent(uri).first

    /** Fylvo: [listFolders] with the listed folder itself. A listing that expired (40926) starts again once. */
    suspend fun listFoldersWithParent(uri: String): Pair<List<FileObject>, FileObject?> {
        var restarted = false
        while (true) {
            val out = mutableListOf<FileObject>()
            var parent: FileObject? = null
            var token: String? = null
            try {
                do {
                    val page = list(uri, pageToken = token, orderBy = "name", orderDirection = "asc")
                    if (parent == null) parent = page.parent
                    out += page.files.filter { it.isFolder }
                    token = page.pagination.nextToken
                } while (!token.isNullOrEmpty())
                return out to parent
            } catch (e: ApiException) {
                if (e.code != Connected.CODE_LISTING_EXPIRED || restarted) throw e
                restarted = true
            }
        }
    }

    suspend fun delete(uris: List<String>, permanently: Boolean = false, ids: Map<String, String>? = null) {
        writeService(uris.firstOrNull().orEmpty()).deleteFiles(
            DeleteFileRequest(uris = uris, skipSoftDelete = permanently, ids = ids?.takeIf { it.isNotEmpty() })
        ).unwrapEmpty()
    }

    /** Puts trashed items (cloudreve://trash/… URIs) back where they came from. */
    suspend fun restore(uris: List<String>) {
        api.service().restoreFiles(RestoreFileRequest(uris)).unwrapEmpty()
    }

    suspend fun createUploadSession(
        uri: String,
        size: Long,
        lastModified: Long,
        mimeType: String,
        overwrite: Boolean = false,
        previous: String = "",
        previousRequired: Boolean = false,
        service: ApiService? = null,
    ): UploadSessionResponse =
        (service ?: api.service()).createUploadSession(
            CreateUploadSessionRequest(
                uri = uri,
                size = size,
                lastModified = lastModified,
                mimeType = mimeType,
                // "version" tells the server to add a new version to the existing
                // file instead of failing with 40004; it is what the web uploader sends.
                entityType = if (overwrite) "version" else "",
                previous = previous,
                previousRequired = previousRequired,
            )
        ).unwrap()

    /** Creates [uri] and any missing ancestors; folders that already exist are fine. */
    suspend fun mkdirs(uri: String) {
        if (CrUri.isRoot(uri)) return
        mkdirs(CrUri.parent(uri))
        try {
            mkdir(uri)
        } catch (e: ApiException) {
            if (e.code != CODE_OBJECT_EXISTS) throw e
        }
    }

    /**
     * The file at exactly [uri], or null when there is none. One request to
     * `/file/info`; the listing API's `name` filter is a case-insensitive substring
     * match, so it is not a substitute for an exact lookup.
     */
    suspend fun stat(uri: String): FileObject? {
        val response = api.service().fileInfo(uri)
        if (response.isSuccessful) {
            val body = response.body()
            if (body != null && body.code == ApiException.CODE_PATH_NOT_EXIST) return null
        }
        return response.unwrap().takeUnless { it.isFolder }
    }

    /** Names of everything directly in the folder [folderUri], over all pages of the listing. */
    suspend fun namesIn(folderUri: String): Set<String> {
        val out = HashSet<String>()
        var token: String? = null
        do {
            val page = list(folderUri, pageToken = token, pageSize = 500)
            page.files.forEach { out += it.name }
            token = page.pagination.nextToken
        } while (!token.isNullOrEmpty())
        return out
    }

    /**
     * Every file (or with [type] "folder" every folder) under [rootUri], recursively. The `type`
     * search walks the whole subtree server-side, so this is one request per 500 entries rather
     * than one per folder. The entries carry id, path, size, type and `primary_entity`.
     * [IndexResult.complete] is false when the server stopped early (too many folders).
     */
    suspend fun indexFiles(rootUri: String, type: String = "file"): IndexResult {
        val out = ArrayList<FileObject>()
        var complete = true
        var token: String? = null
        do {
            val page = list("$rootUri?type=$type", pageToken = token, pageSize = 500)
            if (page.recursionLimitReached) complete = false
            for (f in page.files) if (f.path.isNotEmpty() && f.isFolder == (type == "folder")) out += f
            token = page.pagination.nextToken
        } while (!token.isNullOrEmpty())
        return IndexResult(out, complete)
    }

    class IndexResult(val entries: List<FileObject>, val complete: Boolean)

    /** Fylvo: files and folders of the whole subtree of [rootUri], for offline folders. */
    suspend fun listTree(rootUri: String): TreeListing {
        val files = indexFiles(rootUri, "file")
        val folders = indexFiles(rootUri, "folder")
        return TreeListing(files.entries, folders.entries, files.complete && folders.complete)
    }

    class TreeListing(val files: List<FileObject>, val folders: List<FileObject>, val complete: Boolean)

    /**
     * Fylvo: the file or folder at exactly [uri]; null only when the server clearly says there is
     * none (40016). Every other answer (network trouble, a non-JSON page, another code) throws,
     * so nothing is ever deleted on unclear data.
     */
    suspend fun lookup(uri: String): FileObject? {
        val resp = api.service().fileInfo(uri)
        // Fylvo: refusals carry their code, so an edit upload can tell "no access" from trouble.
        if (!resp.isSuccessful) throw ApiException(resp.code(), "server answered ${resp.code()}")
        val body = resp.body() ?: throw java.io.IOException("no answer from the server")
        return when (body.code) {
            0 -> body.data ?: throw java.io.IOException("empty answer")
            ApiException.CODE_PATH_NOT_EXIST -> null
            else -> throw ApiException(body.code, "unclear answer ${body.code}")
        }
    }

    /**
     * Streams a local file through the upload-session flow: create session,
     * PUT/POST each chunk to its resolved URL, then call completeURL when the
     * policy requires an explicit finalize step (e.g. S3 multipart).
     *
     * Fylvo: [previous] and [previousRequired] make it a conditional update (see
     * CreateUploadSessionRequest). [strict] is for edited offline files: the client never repeats
     * a request by itself, and a completion answer must be the server's envelope with code 0.
     */
    suspend fun uploadFile(
        localFile: File,
        remoteParentUri: String,
        remoteName: String = localFile.name,
        overwrite: Boolean = false,
        mimeType: String? = null,
        lastModified: Long = localFile.lastModified(),
        previous: String = "",
        previousRequired: Boolean = false,
        strict: Boolean = false,
        onSession: (sessionId: String, targetUri: String, entityId: String) -> Unit = { _, _, _ -> },
        onProgress: (sent: Long, total: Long) -> Unit = { _, _ -> },
    ): UploadSessionResponse {
        val targetUri = CrUri.join(remoteParentUri, remoteName)
        val mime = mimeType?.takeIf { it.isNotBlank() }
            ?: java.net.URLConnection.guessContentTypeFromName(remoteName)
            ?: "application/octet-stream"
        // Fylvo: connected storages get the client without transport replay (a lost answer must be
        // seen as such) and send a chunk again after busy or a lost answer (see sendChunk).
        val connected = Connected.isConnected(remoteParentUri)
        val svc = if (strict || connected) api.editService() else api.service()
        val session = createUploadSession(
            uri = targetUri,
            size = localFile.length(),
            lastModified = lastModified,
            mimeType = mime,
            overwrite = overwrite,
            previous = previous,
            previousRequired = previousRequired,
            service = svc,
        )

        if (session.rapidUploaded) return session
        // Fylvo: the caller remembers the session, so an upload that stops half way can be
        // cancelled on the server instead of leaving a locked 0 B placeholder.
        // The server may store the file under another name (e.g. in a shared folder where names
        // are made unique); the session belongs to the address it answers with.
        onSession(session.sessionId, session.uri.ifEmpty { targetUri }, session.entityId)

        val urls = session.uploadUrls
        // Local/slave policies return no presigned URLs: chunks go to the
        // conventional POST /api/v4/file/upload/{session}/{index} endpoint and
        // the server completes the session once every chunk has landed.
        val conventional = urls.isNullOrEmpty()
        val chunkSize = if (session.chunkSize > 0) session.chunkSize else localFile.length().coerceAtLeast(1)
        // The read buffer below is capped at 64 MB; a larger advertised chunk
        // size would send truncated chunks the server rejects.
        if (chunkSize > MAX_CHUNK_BYTES) {
            throw ApiException(-1, "Unsupported upload chunk size: $chunkSize")
        }
        val totalChunks = ((localFile.length() + chunkSize - 1) / chunkSize).toInt().coerceAtLeast(1)
        var sent = 0L
        localFile.inputStream().buffered().use { input ->
            // Never larger than the file: several uploads run at once, a 2 MB photo needs no
            // buffer of the server's full chunk size.
            val buffer = ByteArray(chunkSize.coerceAtMost(64L * 1024 * 1024).coerceAtMost(localFile.length().coerceAtLeast(1)).toInt())
            for (index in 0 until totalChunks) {
                val read = input.readFully(buffer)
                if (index == 0 && read == 0 && localFile.length() > 0) break
                val target = if (conventional) {
                    session_serverBase() + "/api/v4/file/upload/${session.sessionId}/$index"
                } else {
                    if (index >= urls!!.size) break
                    resolveUrl(urls[index])
                }
                val chunkStart = sent
                var attempt = 0
                while (true) {
                    val body = ProgressRequestBody(buffer, read, mime.toMediaTypeOrNull()) { written ->
                        onProgress(chunkStart + written, localFile.length())
                    }
                    var retryAfter: Long? = null
                    try {
                        val resp = if (target.contains("/file/upload/")) {
                            svc.uploadChunk(target, body)
                        } else {
                            // Presigned third-party URLs are conventionally PUT.
                            svc.uploadChunkPut(target, body)
                        }
                        retryAfter = resp.headers()["Retry-After"]?.trim()?.toLongOrNull()
                        if (!resp.isSuccessful) {
                            resp.body()?.close()
                            throw ApiException(resp.code(), "Chunk $index upload failed: HTTP ${resp.code()}")
                        }
                        // Fylvo answers HTTP 200 with an error code in the JSON envelope.
                        if (conventional) checkEnvelope(resp.body()) else resp.body()?.close()
                        break
                    } catch (e: Exception) {
                        // Fylvo: a connected storage takes the same chunk again: one it already has is
                        // confirmed, a repeated last chunk returns the file. Busy, a gateway error and a
                        // lost answer are tried again after a pause, a few times.
                        if (!connected || attempt + 1 >= CHUNK_TRIES || !chunkRetryable(e)) throw e
                        kotlinx.coroutines.delay(Connected.retryDelayMs(attempt, retryAfter))
                        attempt++
                    }
                }
                sent += read
                onProgress(sent, localFile.length())
                if (read <= 0) break
            }
        }

        if (session.completeUrl.isNotEmpty()) {
            val resp = svc.completeUpload(resolveUrl(session.completeUrl))
            if (!resp.isSuccessful) {
                throw ApiException(resp.code(), "Upload completion failed: HTTP ${resp.code()}")
            }
            if (strict) checkEnvelope(resp.body()) else resp.body()?.close()
        }
        return session
    }

    /**
     * Fylvo: cancels an upload session like [deleteUploadSession], for edited offline files: the
     * server's parsed code (0 = done), or null when there was no clear answer (network, no JSON).
     */
    suspend fun cancelUploadStrict(sessionId: String, targetUri: String): Int? = try {
        val resp = api.editService().deleteUploadSession(
            io.github.svenbuild.fylvo.android.api.DeleteUploadSessionRequest(id = sessionId, uri = targetUri)
        )
        if (!resp.isSuccessful) {
            resp.body()?.close()
            resp.code()
        } else {
            resp.body()?.use { body ->
                runCatching {
                    ((api.json.parseToJsonElement(body.string()) as? kotlinx.serialization.json.JsonObject)
                        ?.get("code") as? kotlinx.serialization.json.JsonPrimitive)?.content?.toIntOrNull()
                }.getOrNull()
            }
        }
    } catch (_: java.io.IOException) {
        null
    }

    /**
     * Fylvo: cancels the upload session [sessionId] of the file [targetUri] on the server, the call
     * the web uploader makes when a person cancels. The placeholder file and its lock go away.
     * Returns false when it did not work (no network, session already gone); the server also
     * removes abandoned placeholders by itself after 48 hours.
     */
    suspend fun deleteUploadSession(sessionId: String, targetUri: String): Boolean = try {
        val resp = api.service().deleteUploadSession(
            io.github.svenbuild.fylvo.android.api.DeleteUploadSessionRequest(id = sessionId, uri = targetUri)
        )
        // A clear answer of the server (done, or "no such session / placeholder") leaves nothing
        // to wait for; only server trouble does. Fylvo answers errors as HTTP 200 with a code.
        val code = resp.body()?.use { body ->
            runCatching {
                (api.json.parseToJsonElement(body.string()) as? kotlinx.serialization.json.JsonObject)
                    ?.get("code")?.let { (it as? kotlinx.serialization.json.JsonPrimitive)?.content?.toIntOrNull() }
            }.getOrNull()
        } ?: 0
        resp.code() !in 500..599 && code != 401 && code < 50000 && code != ApiException.CODE_LOCK_CONFLICT
    } catch (e: java.io.IOException) {
        false // no network: try again later
    }

    /**
     * Size of the file at [uri] on the server, or null when there is none (or no clear answer).
     * Network trouble throws an IOException, so the caller can try again later.
     */
    suspend fun sizeOf(uri: String): Long? {
        val resp = api.service().fileInfo(uri)
        if (resp.code() in 500..599) throw java.io.IOException("server answered ${resp.code()}")
        val body = resp.body() ?: throw java.io.IOException("no answer from the server")
        return when (body.code) {
            0 -> body.data?.size
            // Clearly not there.
            404, ApiException.CODE_PATH_NOT_EXIST -> null
            // Anything else is unclear: ask again later instead of guessing.
            else -> throw java.io.IOException("unclear answer ${body.code}")
        }
    }

    /** Throws when a raw response body is a JSON envelope with a non-zero code. */
    private fun checkEnvelope(body: okhttp3.ResponseBody?) {
        // Fylvo always answers with its JSON envelope. Anything else (a Wi-Fi login page, a proxy
        // page) means the part did not reach the server: never count it as sent.
        val text = body?.use { it.string() } ?: throw java.io.IOException("no answer from the server")
        val obj = runCatching { api.json.parseToJsonElement(text) as? kotlinx.serialization.json.JsonObject }.getOrNull()
            ?: throw java.io.IOException("unexpected answer instead of the server's")
        val code = (obj["code"] as? kotlinx.serialization.json.JsonPrimitive)?.content?.toIntOrNull()
            ?: throw java.io.IOException("unexpected answer instead of the server's")
        if (code != 0) {
            val msg = (obj["msg"] as? kotlinx.serialization.json.JsonPrimitive)?.content.orEmpty()
            throw ApiException(code, msg.ifEmpty { "Upload failed (code $code)" })
        }
    }

    /** Fills [buffer] as far as the stream allows; `InputStream.readNBytes` needs API 33. */
    private fun java.io.InputStream.readFully(buffer: ByteArray): Int {
        var filled = 0
        while (filled < buffer.size) {
            val n = read(buffer, filled, buffer.size - filled)
            if (n < 0) break
            filled += n
        }
        return filled
    }

    /**
     * Request body over a byte range that reports bytes written as OkHttp
     * drains it, so a single large chunk still yields smooth progress.
     * Reports on every [STEP] bytes to keep callbacks cheap.
     */
    private class ProgressRequestBody(
        private val bytes: ByteArray,
        private val length: Int,
        private val mediaType: okhttp3.MediaType?,
        private val onWritten: (Long) -> Unit,
    ) : okhttp3.RequestBody() {
        override fun contentType() = mediaType
        override fun contentLength(): Long = length.toLong()
        override fun writeTo(sink: okio.BufferedSink) {
            var offset = 0
            while (offset < length) {
                val n = minOf(STEP, length - offset)
                sink.write(bytes, offset, n)
                offset += n
                onWritten(offset.toLong())
            }
        }

        private companion object {
            const val STEP = 256 * 1024
        }
    }

    private suspend fun resolveUrl(url: String): String {
        if (url.startsWith("http")) return url
        return session_serverBase() + url
    }

    private suspend fun session_serverBase(): String {
        val base = api.serverBase()
        return if (base.endsWith('/')) base.dropLast(1) else base
    }
}
