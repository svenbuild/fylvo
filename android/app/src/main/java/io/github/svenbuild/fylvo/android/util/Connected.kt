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

    /**
     * Writing (upload, new folder, rename, move, copy, delete, share, offline, favourites, tags,
     * colours) stays off inside connected storages in this version, whatever the bits say: those
     * calls need the row ids (`ids`) the server asks for there. Switched on by Android 2.
     */
    @Suppress("UNUSED_PARAMETER")
    fun writesAllowed(uri: String): Boolean = false

    // ---- marks of a row ------------------------------------------------------

    /** The name exists more than once in its folder: shown, nothing can be done with it. */
    fun isUnusable(file: FileObject): Boolean = file.metadata?.containsKey(META_UNSUPPORTED) == true

    /** A shortcut (Drive) or an added shared item (OneDrive): read only. */
    fun isLink(file: FileObject): Boolean = file.metadata?.containsKey(META_LINK) == true

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
