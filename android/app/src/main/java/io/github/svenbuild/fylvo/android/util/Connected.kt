package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.ConnectedStorage
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.SortBy
import java.util.concurrent.ConcurrentHashMap

/**
 * Fylvo: rules for connected storages (Google Drive, OneDrive shown live through the server),
 * file system roots `cloudreve://<id>@connected`. The app offers there only what the row's
 * capability bits allow and what this version can do (show, open, download).
 */
object Connected {
    // Bits of the server's capability set (see Permissions.enabled).
    const val BIT_DOWNLOAD = 7
    const val BIT_LIST = 9
    const val BIT_INFO = 19
    const val BIT_ENTER = 23

    // Codes of the server for connected storages (pkg/serializer/fylvo_codes.go).
    const val CODE_UNSUPPORTED = 40920
    const val CODE_RECONNECT = 40921
    const val CODE_UNAVAILABLE = 40922
    const val CODE_BUSY = 40923
    const val CODE_ITEM = 40925
    const val CODE_LISTING_EXPIRED = 40926
    /** The outcome of an upload is not known: keep the data, never send it again by itself. */
    const val CODE_OUTCOME_UNKNOWN = 40927
    /** The provider's storage is full. */
    const val CODE_FULL = 40924
    /** The version a request names is not the current one (also for connected rows). */
    const val CODE_VERSION_GONE = 40077

    const val META_UNSUPPORTED = "fylvo:unsupported"
    const val META_LINK = "fylvo:link"
    const val META_EXPORT = "fylvo:export"
    const val META_EXTERNAL_URL = "fylvo:external_url"
    const val META_SIZE_UNKNOWN = "fylvo:size_unknown"

    /** Extension the server gives a Google document, by its default export format (replaced on download). */
    private val NATIVE_EXTENSION = mapOf("docx" to ".gdoc", "xlsx" to ".gsheet", "pptx" to ".gslides", "svg" to ".gdraw")

    fun isConnected(uri: String): Boolean = CrUri.root(uri).endsWith("@connected")

    /** Id of the storage of [uri], or null outside connected storages. */
    fun storageId(uri: String): String? =
        if (isConnected(uri)) CrUri.root(uri).removePrefix("cloudreve://").substringBefore('@') else null

    // ---- what may be written (only the row's bits decide; a server that does not write
    // into connected storages sends none of them) ----------------------------------------

    const val BIT_CREATE = 0
    const val BIT_RENAME = 1
    const val BIT_UPLOAD = 6
    const val BIT_DELETE = 14

    /** New folder in [folder]. */
    fun canCreateIn(folder: FileObject?): Boolean = folder != null && folder.isFolder && writable(folder, BIT_CREATE)

    /** Upload into [folder] (bit 6 on a file means "replace its content", which this app does not offer). */
    fun canUploadTo(folder: FileObject?): Boolean = folder != null && folder.isFolder && writable(folder, BIT_UPLOAD)

    /** Rename, and move inside the same storage. */
    fun canRename(file: FileObject): Boolean = writable(file, BIT_RENAME)

    /** Delete: into the trash of the provider. */
    fun canDelete(file: FileObject): Boolean = writable(file, BIT_DELETE)

    private fun writable(file: FileObject, bit: Int): Boolean = !isUnusable(file) && Permissions.enabled(file.capability, bit)

    /** The `ids` the server needs for rename, move and delete: listed path to row id. */
    fun idsOf(rows: Collection<FileObject>): Map<String, String> =
        rows.filter { isConnected(it.path) && it.id.isNotEmpty() }.associate { it.path to it.id }

    /** At most this many addresses go into one request. */
    const val BATCH_LIMIT = 100

    /** Delay before sending a chunk again: the server's Retry-After, else 2 s doubling up to 60 s. */
    fun retryDelayMs(attempt: Int, retryAfterSeconds: Long? = null): Long =
        retryAfterSeconds?.takeIf { it > 0 }?.let { minOf(it, 60) * 1000 }
            ?: (2000L shl attempt.coerceIn(0, 5)).coerceAtMost(60_000)

    // ---- marks of a row ------------------------------------------------------

    /** The name exists more than once in its folder: shown, nothing can be done with it. */
    fun isUnusable(file: FileObject): Boolean = file.metadata?.containsKey(META_UNSUPPORTED) == true

    /**
     * The row is itself a shortcut (Drive) or an added shared item (OneDrive). Says nothing about
     * writing: that comes from the capability bits ([readOnly]). Rows reached through a shortcut
     * further up carry `fylvo:via_link` instead and are shown like any other row.
     */
    fun isLink(file: FileObject): Boolean = file.metadata?.containsKey(META_LINK) == true

    /** No write bit of the row is set (create 0, rename 1, upload 6, delete 14). */
    fun readOnly(file: FileObject): Boolean = WRITE_BITS.none { Permissions.enabled(file.capability, it) }

    private val WRITE_BITS = listOf(0, 1, 6, 14)

    /** A Google document: downloads as this Office format. */
    fun exportExt(file: FileObject): String? = file.metadata?.get(META_EXPORT)?.takeIf { it.isNotBlank() }

    /** Where the provider shows the item itself; only https. */
    fun externalUrl(file: FileObject): String? =
        file.metadata?.get(META_EXTERNAL_URL)?.takeIf { it.startsWith("https://", ignoreCase = true) }

    /** No size to show: the server does not know it, or the row stands for several items (duplicate). */
    fun sizeUnknown(file: FileObject): Boolean = file.metadata?.containsKey(META_SIZE_UNKNOWN) == true || isUnusable(file)

    /** A time of `0001-01-01` means the provider gave none (the root of a storage): not shown. */
    fun knownTime(iso: String): Boolean = iso.isNotEmpty() && !iso.startsWith("0001-")

    // ---- what a row allows ----------------------------------------------------

    fun canEnter(file: FileObject): Boolean =
        file.isFolder && !isUnusable(file) && Permissions.enabled(file.capability, BIT_ENTER)

    fun canDownload(file: FileObject): Boolean =
        !file.isFolder && !isUnusable(file) && Permissions.enabled(file.capability, BIT_DOWNLOAD)

    fun canInfo(file: FileObject): Boolean = Permissions.enabled(file.capability, BIT_INFO)

    /** What a tap on a row does inside a connected storage. */
    enum class Tap { ENTER, BROWSER, OPEN, SHEET }

    /**
     * A folder opens when it may be entered, a Google document (or a folder that cannot be
     * entered) at the provider, a file when it may be downloaded; anything else shows its sheet.
     */
    fun tapAction(file: FileObject): Tap = when {
        isUnusable(file) -> Tap.SHEET
        canEnter(file) -> Tap.ENTER
        externalUrl(file) != null -> Tap.BROWSER
        canDownload(file) -> Tap.OPEN
        else -> Tap.SHEET
    }

    /**
     * Name of the downloaded file: a Google document `Budget.gsheet` with export `xlsx` becomes
     * `Budget.xlsx`, the way the server names the download.
     */
    fun downloadName(file: FileObject): String {
        val ext = exportExt(file) ?: return file.name
        // Exactly as the server: only the extension of this document type, case as it is.
        return file.name.removeSuffix(NATIVE_EXTENSION[ext].orEmpty()) + ".$ext"
    }

    /** The server sorts connected listings by name, size and modification time only. */
    fun sortFor(by: SortBy): SortBy = if (by == SortBy.CREATED) SortBy.UPDATED else by

    // ---- errors ------------------------------------------------------------------

    /** The code that counts: a batch answer (40081) of one failure stands for that failure. */
    fun codeOf(e: Throwable): Int? {
        val api = e as? ApiException ?: return null
        return if (api.code == ErrorText.CODE_BATCH_NOT_COMPLETED && api.innerCodes.size == 1) api.innerCodes.single() else api.code
    }

    /** The file changed since it was listed (version gone, or HTTP 409 of a content address). */
    fun isChanged(e: Throwable): Boolean = codeOf(e).let { it == CODE_VERSION_GONE || it == 409 } ||
        ((e as? ApiException)?.reason == REASON_CHANGED && codeOf(e) == CODE_ITEM)

    /** The storage itself is gone or not usable: the overview may say more. */
    fun isStorageTrouble(e: Throwable): Boolean =
        codeOf(e).let { it == CODE_RECONNECT || it == CODE_UNAVAILABLE || it == ApiException.CODE_PATH_NOT_EXIST || it == 404 }

    /**
     * Content addresses answer with HTTP status codes; inside connected storages they mean the
     * provider: 429 and 503 busy, 409 the file changed. Other failures stay as they are.
     */
    fun normalize(e: Throwable): Throwable {
        val code = (e as? ApiException)?.code ?: return e
        return when {
            code == 429 || code == 503 -> ApiException(CODE_BUSY, e.message.orEmpty())
            isChanged(e) -> ApiException(CODE_ITEM, e.message.orEmpty(), reason = REASON_CHANGED)
            else -> e
        }
    }

    const val REASON_CHANGED = "changed"

    // ---- names of the roots ----------------------------------------------------

    private val names = ConcurrentHashMap<String, String>()

    /**
     * The storages of the account, from the overview. Two with the same name are told apart by
     * their account: "Google Drive (alex@example.com)".
     */
    fun nameStorages(storages: List<ConnectedStorage>) {
        val counts = storages.groupingBy { it.name }.eachCount()
        val fresh = storages.filter { it.uri.isNotEmpty() && it.name.isNotEmpty() }.associate { s ->
            val label = if ((counts[s.name] ?: 0) > 1 && s.account.isNotEmpty()) "${s.name} (${s.account})" else s.name
            CrUri.root(s.uri) to label
        }
        names.keys.retainAll(fresh.keys)
        names.putAll(fresh)
    }

    /** A root listing names its storage too (`parent.name`); used until the overview is there. */
    fun nameFromListing(uri: String, name: String?) {
        if (isConnected(uri) && CrUri.isRoot(uri) && !name.isNullOrEmpty()) names.putIfAbsent(CrUri.root(uri), name)
    }

    fun rootName(root: String): String? = names[root]

    fun forgetNames() = names.clear()
}

/** Fylvo: what a batched write in a connected storage did: [done] paths, and the first [error]. */
data class BatchResult(val done: List<String>, val error: Exception?)

/**
 * Fylvo: sends [paths] in requests of at most [Connected.BATCH_LIMIT] and stops at the first
 * failure. A batch answer (40081) names the addresses that failed; the others of that request
 * count as done. Unit-tested.
 */
suspend fun connectedBatches(paths: List<String>, send: suspend (List<String>) -> Unit): BatchResult {
    val done = mutableListOf<String>()
    for (chunk in paths.chunked(Connected.BATCH_LIMIT)) {
        try {
            send(chunk)
            done += chunk
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            if (e is ApiException && e.code == ErrorText.CODE_BATCH_NOT_COMPLETED) {
                // The server names the addresses as it reads them: compared in one spelling.
                val failed = e.failedPaths.mapTo(HashSet(), CrUri::canonical)
                done += chunk.filter { CrUri.canonical(it) !in failed }
            }
            return BatchResult(done, e)
        }
    }
    return BatchResult(done, null)
}
