package io.github.svenbuild.fylvo.android.ui.files

import android.content.ContentValues
import android.content.Context
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.annotation.StringRes
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.WorkInfo
import androidx.work.WorkManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.withPermit
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ConnectedListing
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.data.offline.EditEntry
import io.github.svenbuild.fylvo.android.data.offline.OfflineCoverage
import io.github.svenbuild.fylvo.android.data.offline.OfflineEdits
import io.github.svenbuild.fylvo.android.data.offline.refusalCode
import io.github.svenbuild.fylvo.android.data.offline.OfflineManager
import io.github.svenbuild.fylvo.android.data.offline.OfflineRootInfo
import io.github.svenbuild.fylvo.android.data.offline.hasRoomFor
import io.github.svenbuild.fylvo.android.data.SortBy
import io.github.svenbuild.fylvo.android.data.SortOrder
import io.github.svenbuild.fylvo.android.data.ViewMode
import io.github.svenbuild.fylvo.android.data.ViewPrefs
import io.github.svenbuild.fylvo.android.data.ViewSettings
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.connectedBatches
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.ErrorText
import io.github.svenbuild.fylvo.android.util.Permissions
import io.github.svenbuild.fylvo.android.util.UiText
import io.github.svenbuild.fylvo.android.util.userError
import io.github.svenbuild.fylvo.android.work.BackgroundSync
import io.github.svenbuild.fylvo.android.work.UploadStatus
import io.github.svenbuild.fylvo.android.work.UploadWorker
import java.io.File
import java.io.OutputStream
import java.util.concurrent.ConcurrentHashMap

/** Files-app style categories, each a server-side search over the whole drive. */
enum class Category(val uri: String, @StringRes val label: Int, val filter: String? = null) {
    RECENT("${CrUri.MY_PREFIX}?type=file", R.string.category_recent, "type=file"),
    IMAGES("${CrUri.MY_PREFIX}?category=image", R.string.category_images, "category=image"),
    VIDEOS("${CrUri.MY_PREFIX}?category=video", R.string.category_videos, "category=video"),
    AUDIO("${CrUri.MY_PREFIX}?category=audio", R.string.category_audio, "category=audio"),
    DOCUMENTS("${CrUri.MY_PREFIX}?category=document", R.string.category_documents, "category=document"),
    TRASH("cloudreve://trash", R.string.trash),
    /** Fylvo: folders other people shared with this account. */
    SHARED_WITH_ME(CrUri.SHARED_WITH_ME, R.string.shared_with_me);

    val gallery: Boolean get() = this == IMAGES || this == VIDEOS

    companion object {
        fun fromUri(uri: String): Category? = entries.firstOrNull { it.uri == uri }
    }
}

/** Fylvo: the server address a folder or file is kept offline under (a shared folder's own share). */
fun offlineUriOf(file: FileObject): String = file.sharedRedirect ?: file.path

/** Fylvo: offline is offered in My files and in shared folders, not in the trash. */
fun canKeepOffline(file: FileObject): Boolean {
    val uri = offlineUriOf(file)
    val place = (CrUri.root(uri) == CrUri.MY_PREFIX && !CrUri.isRoot(uri)) || CrUri.isShare(uri)
    return place && !file.isUploading
}

/**
 * Fylvo: the question before a folder is kept offline: how much it is and what stays free.
 * [loading] while the folder is counted; [refusal] set when it cannot be kept offline.
 */
data class OfflinePrompt(
    val uri: String,
    val name: String,
    val loading: Boolean = true,
    val files: Int = 0,
    val bytes: Long = 0,
    val freeBytes: Long = 0,
    val refusal: Refusal? = null,
) {
    enum class Refusal { NoRoom, TooManyFolders }
}

/** Fylvo: "Bearbeitung abschliessen" could not upload the changes of [name]: ask what to do. */
data class EditPrompt(
    val entryId: String,
    val name: String,
    val reason: OfflineEdits.NotUploaded,
    /** A kept snapshot (bytes not confirmed on the server) goes too when it is discarded. */
    val unsent: Boolean = false,
    /** The server's code of a refusal. */
    val code: Int = 0,
)

/** Fylvo: switching root [rootId] off while [entries] hold changes the server does not have. */
data class RootEditPrompt(val rootId: String, val name: String, val entries: List<EditEntry>, val busy: Boolean = false)

/** One-off message for the snackbar, optionally with an action such as Undo. [long]: stays longer (hints). */
data class UiMessage(val text: UiText, val actionLabel: UiText? = null, val action: (() -> Unit)? = null, val long: Boolean = false)

data class FilesUiState(
    /** Landing page with Recent and Categories; false while browsing a folder or category. */
    val home: Boolean = true,
    val recent: List<FileObject> = emptyList(),
    val recentLoading: Boolean = false,
    val currentUri: String = CrUri.MY_PREFIX,
    val files: List<FileObject> = emptyList(),
    /** No usable listing yet: show a spinner instead of files. */
    val loading: Boolean = false,
    /** A cached listing is on screen while a fresh one is fetched. */
    val revalidating: Boolean = false,
    val loadingMore: Boolean = false,
    val nextToken: String? = null,
    val error: UiText? = null,
    /** Fylvo: the code that caused [error] (connected storages show a way to the web app for 40921). */
    val errorCode: Int? = null,
    val snackbar: UiMessage? = null,
    val view: ViewPrefs = ViewPrefs(),
    /** Paths of files picked in selection mode; empty means not selecting. */
    val selected: Set<String> = emptySet(),
    /** Upload batches still running, newest last. */
    val uploads: List<UploadStatus> = emptyList(),
    /** Periodic folder mirror runs in progress. */
    val backgroundSyncs: List<BackgroundSync.Status> = emptyList(),
    /** Files being fetched for open/download: path to fraction (null = size unknown). */
    val downloading: Map<String, Float?> = emptyMap(),
    /** Fylvo: several selected files are being saved one after another. */
    val batchSaving: Boolean = false,
    /** Non-null while the search UI is open (even with an empty query). */
    val searchQuery: String? = null,
    /** Folder the search is limited to; null = whole drive. */
    val searchScope: String? = null,
    val searchType: Category? = null,
    val searchResults: List<FileObject> = emptyList(),
    val searchNextToken: String? = null,
    val searchLoading: Boolean = false,
    val searchLoadingMore: Boolean = false,
    val searchError: UiText? = null,
    /** Fylvo: offline folders and files of this account. */
    val offlineRoots: List<OfflineRootInfo> = emptyList(),
    val offlineCoverage: OfflineCoverage = OfflineCoverage(),
    /** Id of the offline root being synced right now. */
    val offlineSyncing: String? = null,
    /** The folder on screen comes from the copies on this phone (no connection). */
    val offlineMode: Boolean = false,
    val offlinePrompt: OfflinePrompt? = null,
    /** Paths opened from the copy on this phone because the server failed: maybe outdated. */
    val staleCopies: Set<String> = emptySet(),
    /** Fylvo: the folder on screen as the server describes it, with its capability set. */
    val parent: FileObject? = null,
    /** Fylvo: the `connected` block of the listing on screen (inside a connected storage). */
    val connected: ConnectedListing? = null,
    /** Fylvo: every working copy of this account (also finished ones), see OfflineEdits. */
    val editEntries: List<EditEntry> = emptyList(),
    val editPrompt: EditPrompt? = null,
    val rootPrompt: RootEditPrompt? = null,
) {
    /** Fylvo: working copies in use, by server file id. */
    private val editsById: Map<String, EditEntry> by lazy { editEntries.filter { it.active }.associateBy { it.fileId } }
    /** Fylvo: the working copy in use of [file], or null. */
    fun editOf(file: FileObject): EditEntry? = if (file.isFolder || file.id.isEmpty()) null else editsById[file.id]
    /**
     * Fylvo: "Bearbeiten" is offered for a file of an offline root (reopens a working copy that
     * exists): in the person's own files, in a share with the right to change it (online only).
     */
    fun canEdit(file: FileObject): Boolean = !file.isFolder && !file.isUploading && !inTrash && file.id.isNotEmpty() &&
        (editOf(file) != null || (isOffline(file) && if (offlineMode) !CrUri.isShare(file.path) else Permissions.canModifyFile(file)))
    /** Fylvo: kept on this phone, as a root or inside an offline folder (shown with a pin). */
    fun isOffline(file: FileObject): Boolean = offlineCoverage.isOffline(offlineUriOf(file))
    /** Fylvo: kept offline as its own root. */
    fun offlineOn(file: FileObject): Boolean = offlineCoverage.rootAt(offlineUriOf(file)) != null
    /** Fylvo: inside an offline folder; uses the folder's copy and cannot be switched separately. */
    fun offlineCovered(file: FileObject): Boolean = offlineCoverage.coveringFolder(offlineUriOf(file)) != null
    /** Fylvo: offline can be switched here (My files, shared folders, with a connection). */
    fun offlineAllowed(file: FileObject): Boolean = canKeepOffline(file) && !offlineMode
    val selecting: Boolean get() = selected.isNotEmpty()
    /** Fylvo: the folder on screen is inside a connected storage. */
    val inConnected: Boolean get() = !home && Connected.isConnected(currentUri)
    /** Uploads landing in the folder on screen; shown inline above the files. */
    val uploadsHere: List<UploadStatus> get() = uploads.filter { it.parentUri == currentUri }
    val category: Category? get() = Category.fromUri(currentUri)
    /** Category listings mix folders; the row subtitle names each file's folder. */
    val virtual: Boolean get() = category != null
    val inTrash: Boolean get() = category == Category.TRASH
    /** Fylvo: may upload and create folders here (share capability or ownership). */
    val canAddHere: Boolean get() = !home && category == null && !offlineMode && Permissions.canAddTo(parent)
    /** Effective display mode: galleries keep their own preference. */
    val effectiveMode: ViewMode get() = if (category?.gallery == true) view.galleryMode else view.mode
}

class FilesViewModel(
    private val repo: FileRepository,
    private val offline: OfflineManager,
    private val edits: OfflineEdits,
    private val viewSettings: ViewSettings,
    private val workManager: WorkManager,
    /** Invoked when the server no longer accepts our session; the caller returns to login. */
    private val onSessionExpired: suspend () -> Unit,
) : ViewModel() {

    private val _state = MutableStateFlow(FilesUiState())
    val state: StateFlow<FilesUiState> = _state

    /** Last progress emission per path; download callbacks fire every 256 KB. */
    private val lastProgressAt = ConcurrentHashMap<String, Long>()

    private companion object {
        const val RECENT_COUNT = 12
        const val PREFETCH_THUMBS = 40
        const val PREFETCH_PARALLELISM = 6
        const val PROGRESS_INTERVAL_MS = 200L
        const val UNDO_MAX_PAGES = 10
        const val BATCH_SAVE_KEY = "save-batch"
    }

    private val downloadJobs = ConcurrentHashMap<String, Job>()

    /** Fylvo: the account's connected storages (Home section, names, status). */
    val connected = ConnectedStorages(repo.connected, viewModelScope) { onSessionExpired() }
    /** Finished upload ids already reported, so a replayed WorkInfo is not announced twice. */
    private val announcedUploads = HashSet<java.util.UUID>()

    init {
        loadRecent()
        observeUploads()
        viewModelScope.launch {
            runCatching { offline.start() }
        }
        // Fylvo: the web app's file type colours, once per app start.
        if (FileIcons.claimFetch()) viewModelScope.launch {
            val icons = repo.explorerIcons()
            if (icons == null) FileIcons.fetchFailed() else FileIcons.update(icons)
        }
        viewModelScope.launch {
            offline.roots.collect { list ->
                _state.value = _state.value.copy(offlineRoots = list, offlineCoverage = OfflineCoverage(list.map { it.root }))
            }
        }
        viewModelScope.launch {
            offline.syncing.collect { _state.value = _state.value.copy(offlineSyncing = it) }
        }
        viewModelScope.launch {
            edits.entries.collect { list -> _state.update { it.copy(editEntries = list) } }
        }
        viewModelScope.launch {
            var first = true
            viewSettings.prefs.collect { prefs ->
                val sortChanged = prefs.sort != _state.value.view.sort
                _state.value = _state.value.copy(view = prefs)
                // Sorting is done server-side, so a new order means a new listing.
                if (!first && sortChanged) refresh()
                first = false
            }
        }
    }

    /** Last good listing per folder, so going back (or returning) paints instantly. */
    private val listingCache = HashMap<String, Listing>()
    private data class Listing(
        val files: List<FileObject>,
        val nextToken: String?,
        val sortKey: SortOrder,
        val parent: FileObject?,
        val connected: ConnectedListing? = null,
    )

    /** Fylvo: counts refreshes; only the newest one may change the listing. */
    private var refreshGeneration = 0
    /** Fylvo: the refresh that restarted a connected listing after 40926; it is not restarted again. */
    private var restartedGeneration = -1

    /** [restart]: Fylvo, a connected listing starts again after its cursor expired (40926). */
    fun refresh(restart: Boolean = false) {
        val uri = _state.value.currentUri
        // Fylvo: the rows on screen before the placeholder replaces them (ids for keepListed).
        val shownBefore = _state.value.files
        val generation = ++refreshGeneration
        if (restart) restartedGeneration = generation
        val cached = listingCache[uri]?.takeIf { it.sortKey == _state.value.view.sort }
        _state.value = _state.value.copy(
            loading = cached == null,
            revalidating = cached != null,
            error = null,
            errorCode = null,
            files = cached?.files ?: emptyList(),
            nextToken = cached?.nextToken,
            parent = cached?.parent,
            connected = cached?.connected,
            loadingMore = false,
            offlineMode = false,
        )
        viewModelScope.launch {
            try {
                val res = list(uri)
                // The user may have moved on while this was in flight, or a newer refresh started.
                if (_state.value.currentUri != uri || generation != refreshGeneration) return@launch
                rememberShareNames(uri, res)
                Connected.nameFromListing(uri, res.parent?.name)
                val files = sortFiles(res.files)
                listingCache[uri] = Listing(files, res.pagination.nextToken, _state.value.view.sort, res.parent, res.connected)
                _state.value = _state.value.copy(
                    loading = false,
                    revalidating = false,
                    files = files,
                    nextToken = res.pagination.nextToken,
                    parent = res.parent,
                    connected = res.connected,
                    selected = keepListed(files, shownBefore),
                )
                prefetchThumbs(files)
            } catch (e: Exception) {
                if (_state.value.currentUri != uri || generation != refreshGeneration) return@launch
                if (handleSessionExpiry(e)) return@launch
                // Fylvo: a connected storage that is gone or must be connected again shows its error,
                // never rows of it from before; the Home section learns the new state.
                if (Connected.isConnected(uri) && Connected.isStorageTrouble(e)) {
                    showStorageTrouble(uri, e)
                    return@launch
                }
                // Fylvo: no connection inside an offline folder: show the copies on this phone.
                val local = if (e is CancellationException || (e is ApiException && !e.isTransient)) null else offline.browse(uri)
                if (local != null && _state.value.currentUri == uri && generation == refreshGeneration) {
                    _state.value = _state.value.copy(
                        loading = false, revalidating = false, files = local, nextToken = null, parent = null, offlineMode = true,
                        selected = keepListed(local, shownBefore),
                    )
                    return@launch
                }
                // Fylvo: the offline lookup may have taken a while; the user may be elsewhere now.
                if (_state.value.currentUri != uri || generation != refreshGeneration) return@launch
                // Fylvo: the cursor of a cached connected listing may have expired; never use it.
                val dropToken = cached != null && Connected.isConnected(uri)
                if (dropToken) listingCache[uri] = cached!!.copy(nextToken = null)
                // Keep showing the cached files if we had them; only surface the error otherwise.
                _state.value = _state.value.copy(
                    loading = false,
                    revalidating = false,
                    nextToken = if (dropToken) null else _state.value.nextToken,
                    error = if (cached == null) userError(e) else null,
                    errorCode = if (cached == null) Connected.codeOf(e) else null,
                    // Fylvo: a message still waiting (an upload's result) is not dropped.
                    snackbar = if (cached != null) failure(e) else _state.value.snackbar,
                    // Fylvo: nothing listed to act on behind the error screen.
                    selected = if (cached == null) emptySet() else _state.value.selected,
                )
            }
        }
    }

    /**
     * Fylvo: the selection after a real listing landed: only paths that are still listed. The
     * placeholder a refresh shows first never prunes it, so a selection survives a refresh.
     */
    private fun keepListed(files: List<FileObject>, shownBefore: List<FileObject>): Set<String> {
        val selected = _state.value.selected
        if (selected.isEmpty()) return selected
        val listed = files.associateBy { it.path }
        // Fylvo: in a connected storage only the same row stays selected (same id as on screen when
        // the refresh started), never a file that took its place under the same name.
        val before = shownBefore.associateBy { it.path }
        return selected.filterTo(LinkedHashSet()) { path ->
            val row = listed[path] ?: return@filterTo false
            !Connected.isConnected(path) || before[path]?.id == row.id
        }
    }

    fun loadMore() {
        val s = _state.value
        val token = s.nextToken ?: return
        val uri = s.currentUri
        // Fylvo: while a refresh runs, the token belongs to the cached listing it will replace.
        if (s.loadingMore || s.revalidating) return
        val generation = refreshGeneration
        _state.value = s.copy(loadingMore = true)
        viewModelScope.launch {
            try {
                val res = list(uri, token)
                // The user may have navigated away; the page belongs to the old folder. Fylvo: a
                // refresh since then has a newer first page; this one would bring back old rows.
                if (_state.value.currentUri != uri || generation != refreshGeneration) return@launch
                val files = sortFiles(_state.value.files + res.files)
                listingCache[uri] = Listing(files, res.pagination.nextToken, _state.value.view.sort, _state.value.parent, _state.value.connected)
                _state.value = _state.value.copy(
                    loadingMore = false,
                    files = files,
                    nextToken = res.pagination.nextToken,
                )
            } catch (e: Exception) {
                if (_state.value.currentUri != uri || generation != refreshGeneration) return@launch
                if (handleSessionExpiry(e)) return@launch
                if (Connected.isConnected(uri)) {
                    when {
                        // Fylvo: the cursor expired: the listing starts again, once.
                        Connected.codeOf(e) == Connected.CODE_LISTING_EXPIRED && generation != restartedGeneration -> {
                            listingCache.remove(uri)
                            refresh(restart = true)
                            return@launch
                        }
                        Connected.isStorageTrouble(e) -> {
                            showStorageTrouble(uri, e)
                            return@launch
                        }
                        // A cursor that failed is not sent again; pull to refresh starts anew.
                        Connected.codeOf(e) == Connected.CODE_LISTING_EXPIRED -> {
                            listingCache[uri]?.let { listingCache[uri] = it.copy(nextToken = null) }
                            _state.value = _state.value.copy(loadingMore = false, nextToken = null, snackbar = failure(e))
                            return@launch
                        }
                    }
                }
                _state.value = _state.value.copy(loadingMore = false, snackbar = failure(e))
            }
        }
    }

    /** Fylvo: shared folders are shown by their name, also in paths and the folder picker. */
    private fun rememberShareNames(uri: String, res: io.github.svenbuild.fylvo.android.api.ListFileResponse) {
        res.files.forEach { f -> f.sharedRedirect?.let { CrUri.nameShare(it, f.name) } }
        if (CrUri.isShare(uri) && CrUri.isRoot(uri)) res.parent?.let { CrUri.nameShare(uri, it.name) }
    }

    private suspend fun list(uri: String, token: String? = null) =
        if (Category.fromUri(uri) != null) {
            // Categories read like a timeline, as in the Files app.
            repo.list(uri, pageToken = token, orderBy = "updated_at", orderDirection = "desc")
        } else {
            val by = _state.value.view.sort.by
            repo.list(
                uri,
                pageToken = token,
                // Fylvo: connected storages sort by name, size and modification time only.
                orderBy = (if (Connected.isConnected(uri)) Connected.sortFor(by) else by).apiName,
                orderDirection = _state.value.view.sort.apiDirection,
            )
        }

    /** True (and session torn down) when the error means we must log in again. */
    private suspend fun handleSessionExpiry(e: Exception): Boolean {
        if (e is ApiException && e.code == ApiException.CODE_LOGIN_REQUIRED) {
            onSessionExpired()
            return true
        }
        return false
    }

    fun navigateTo(file: FileObject) {
        if (!file.isFolder) return
        // Fylvo: a "Shared with me" entry opens the share it points to.
        file.sharedRedirect?.let { CrUri.nameShare(it, file.name) }
        val target = file.sharedRedirect ?: file.path
        _state.value = _state.value.copy(currentUri = target, home = false, selected = emptySet())
        refresh()
    }

    /** Up one folder; from the root folder or a category back to the home page. */
    fun navigateUp(): Boolean {
        val s = _state.value
        if (s.home) return false
        // Fylvo: from the top of a shared folder back to the list of shared folders.
        if (CrUri.isShare(s.currentUri) && CrUri.isRoot(s.currentUri)) {
            openCategory(Category.SHARED_WITH_ME)
            return true
        }
        if (s.category == Category.TRASH) {
            openMyFiles()
            return true
        }
        if (CrUri.isRoot(s.currentUri) || s.virtual) {
            goHome()
            return true
        }
        _state.value = s.copy(currentUri = CrUri.parent(s.currentUri), selected = emptySet())
        refresh()
        return true
    }

    fun goHome() {
        _state.value = _state.value.copy(home = true, currentUri = CrUri.MY_PREFIX, selected = emptySet())
        loadRecent()
    }

    fun openMyFiles() {
        _state.value = _state.value.copy(home = false, currentUri = CrUri.MY_PREFIX, selected = emptySet())
        refresh()
    }

    fun openCategory(category: Category) {
        _state.value = _state.value.copy(home = false, currentUri = category.uri, selected = emptySet())
        refresh()
    }

    /** Newest files across the drive for the home page's Recent strip. */
    fun loadRecent() {
        connected.load()
        if (_state.value.recentLoading) return
        _state.value = _state.value.copy(recentLoading = true)
        viewModelScope.launch {
            runCatching {
                repo.list(Category.RECENT.uri, orderBy = "updated_at", orderDirection = "desc", pageSize = RECENT_COUNT)
            }.onSuccess { res ->
                _state.value = _state.value.copy(recent = res.files.filter { !it.isFolder }, recentLoading = false)
            }.onFailure { e ->
                _state.value = _state.value.copy(recentLoading = false)
                if (!handleSessionExpiry(e as? Exception ?: Exception(e))) {
                    // Fylvo: a waiting message (an upload's result) is not replaced.
                    _state.update { it.copy(snackbar = it.snackbar ?: failure(e)) }
                }
            }
        }
    }

    // ---- transfers --------------------------------------------------------

    private fun observeUploads() {
        viewModelScope.launch {
            workManager.getWorkInfosByTagFlow(BackgroundSync.TAG).collect { infos ->
                val syncs = infos.mapNotNull(BackgroundSync::statusOf)
                _state.value = _state.value.copy(backgroundSyncs = syncs)
            }
        }
        viewModelScope.launch {
            workManager.getWorkInfosByTagFlow(UploadWorker.TAG).collect { infos ->
                val statuses = infos.map(UploadStatus::from)
                _state.value = _state.value.copy(uploads = statuses.filter { it.active })
                statuses.filter { !it.active && announcedUploads.add(it.id) }.forEach(::announceUpload)
                if (statuses.any { !it.active }) workManager.pruneWork()
            }
        }
    }

    private fun announceUpload(u: UploadStatus) {
        val message = when {
            u.state == WorkInfo.State.CANCELLED -> UiText.Res(R.string.upload_cancelled)
            u.failed == 0 -> UiText.Plural(R.plurals.uploaded_files, u.uploaded, u.uploaded)
            u.uploaded == 0 -> u.error?.let { UiText.Res(R.string.upload_failed_reason, it) }
                ?: UiText.Res(R.string.upload_failed)
            // Fylvo: in a connected folder the reason is told too (an unknown outcome must not get lost).
            Connected.isConnected(u.parentUri) && u.error != null ->
                UiText.Res(R.string.uploaded_some_failed_reason, u.uploaded, u.failed, u.error)
            else -> UiText.Res(R.string.uploaded_some_failed, u.uploaded, u.failed)
        }
        _state.value = _state.value.copy(snackbar = UiMessage(message, long = Connected.isConnected(u.parentUri) && u.failed > 0))
        // Fylvo: a connected folder is listed again after any upload into it (an unknown outcome too).
        if (Connected.isConnected(u.parentUri)) listingCache.remove(u.parentUri)
        if (u.uploaded > 0 || Connected.isConnected(u.parentUri)) {
            if (_state.value.home) loadRecent() else if (u.parentUri == _state.value.currentUri) refresh()
        }
    }

    /**
     * Every name in [folderUri] for the upload conflict check, not only the loaded pages; falls
     * back to the loaded ones when the server cannot be asked.
     */
    suspend fun namesInFolder(folderUri: String): Set<String> =
        runCatching { repo.namesIn(folderUri) }.getOrElse { e ->
            if (e is kotlinx.coroutines.CancellationException) throw e
            // Fylvo: the loaded names only when they are of that folder.
            if (_state.value.currentUri == folderUri) _state.value.files.map { it.name }.toSet() else emptySet()
        }

    fun cancelUpload(context: android.content.Context, id: java.util.UUID) {
        // Off the main thread and beyond this screen; a waiting batch also gets its server
        // session cleaned up.
        @OptIn(kotlinx.coroutines.DelicateCoroutinesApi::class)
        kotlinx.coroutines.GlobalScope.launch(kotlinx.coroutines.Dispatchers.IO) {
            runCatching { UploadWorker.cancel(context, id) }
        }
    }

    /**
     * Fetches [file] into the app cache, reusing a previous copy when the size
     * still matches, and reports progress in [FilesUiState.downloading]. A
     * second call for a file already in flight cancels that download.
     */
    fun fetchForOpen(context: Context, file: FileObject, onReady: (File) -> Unit) {
        downloadJobs.remove(file.path)?.let {
            it.cancel()
            return
        }
        // Fylvo: already being saved by a download of several files.
        if (file.path in batchPaths) return
        // Fylvo: a file kept offline opens from its copy on this phone when that is the current
        // version (or there is no connection); otherwise from the server like any file. An edited
        // file opens from its working copy.
        if (state.value.isOffline(file) || state.value.editOf(file) != null) {
            viewModelScope.launch {
                val local = currentOfflineCopy(file)
                if (local != null) onReady(local) else fetchFromServer(context, file, onReady)
            }
            return
        }
        fetchFromServer(context, file, onReady)
    }

    private fun fetchFromServer(context: Context, file: FileObject, onReady: (File) -> Unit) {
        val dest = cacheFileFor(context, file)
        if (cachedCopy(dest, file)) {
            onReady(dest)
            return
        }
        // Fylvo: a download of several files may have claimed it while the offline copy was checked.
        if (file.path in batchPaths) return
        setDownloading(file.path, null)
        val job = viewModelScope.launch {
            try {
                // The exact version, so the cache under its version key holds that version.
                val url = repo.downloadUrl(file.path, entity = file.primaryEntity)
                repo.downloadToFile(url, dest) { done, total ->
                    setDownloading(file.path, if (total > 0) done.toFloat() / total else null)
                }
                markFresh(file)
                onReady(dest)
            } catch (_: CancellationException) {
                // user cancelled; nothing to say
            } catch (e: Exception) {
                // Fylvo: no connection: an older copy kept offline is better than nothing.
                val local = anyOfflineCopy(file)
                if (local != null) onReady(local)
                else {
                    val message = downloadFailure(file, e)
                    _state.update { it.copy(snackbar = message) }
                }
            } finally {
                releaseSingle(file.path, file.path, coroutineContext[Job])
            }
        }
        downloadJobs[file.path] = job
    }

    private fun setDownloading(path: String, fraction: Float?) {
        // Progress callbacks arrive every 256 KB; emitting each one recomposes
        // the whole file list. Throttle to ~5 updates a second; null (start)
        // and 1.0 (done) always pass through.
        val now = System.currentTimeMillis()
        val last = lastProgressAt[path] ?: 0L
        if (fraction != null && fraction < 1f && now - last < PROGRESS_INTERVAL_MS) return
        lastProgressAt[path] = now
        // Fylvo: atomic, progress arrives from download threads.
        _state.update { it.copy(downloading = it.downloading + (path to fraction)) }
    }

    private fun clearDownloading(path: String) {
        lastProgressAt.remove(path)
        _state.update { it.copy(downloading = it.downloading - path) }
    }

    /**
     * Fylvo: keyed by the file's version too, so an older copy of the same size is never reused.
     * A Google document is stored under its export name (Budget.xlsx), so apps know the type.
     */
    private fun cacheFileFor(context: Context, file: FileObject): File =
        File(File(context.cacheDir, "downloads"), file.id).resolve(file.primaryEntity.ifEmpty { "unknown" }).resolve(Connected.downloadName(file))

    /**
     * A cached download of exactly this version (none when the version is unknown). Fylvo: never
     * when the size is unknown (an export of a connected storage): a short copy could not be told.
     */
    private fun cachedCopy(dest: File, file: FileObject): Boolean =
        file.primaryEntity.isNotEmpty() && !Connected.sizeUnknown(file) && dest.exists() && dest.length() == file.size

    /**
     * Fylvo: the message for a failed download. Inside a connected storage a changed file
     * refreshes the listing, and trouble with the storage reloads the overview first, so a
     * storage that must be connected again says so.
     */
    private suspend fun downloadFailure(file: FileObject, e: Exception): UiMessage =
        failure(connectedError(file, e), fallback = R.string.download_failed)

    /**
     * Fylvo: [e] of a request for [file] as the app shows it. Outside connected storages it stays
     * as it is. Inside, content answers get their meaning (busy, changed); a changed file refreshes
     * the listing on screen, so opening it again asks for the new version; trouble with the storage
     * reloads the overview first, so a storage that must be connected again says so.
     */
    private suspend fun connectedError(file: FileObject, e: Throwable): Throwable {
        if (!Connected.isConnected(file.path)) return e
        val normalized = Connected.normalize(e)
        if (Connected.isChanged(normalized)) {
            refreshIfShowing(file)
        } else if (Connected.isStorageTrouble(e)) {
            connected.reload()
            if (connected.storageOf(file.path)?.needsReconnect == true) return ApiException(Connected.CODE_RECONNECT, e.message.orEmpty())
        }
        return normalized
    }

    /**
     * Fylvo: a viewer (image, video) got HTTP [status] for the content of [file]. Inside a
     * connected storage the text says what it means, and a changed file refreshes the listing;
     * null elsewhere (the viewer keeps its own text).
     */
    suspend fun contentFailed(file: FileObject, status: Int): UiText? {
        if (!Connected.isConnected(file.path)) return null
        return userError(connectedError(file, ApiException(status, "HTTP $status")))
    }

    /** Fylvo: refreshes the listing when the folder of [file] is on screen. */
    private fun refreshIfShowing(file: FileObject) {
        val s = _state.value
        // One refresh is enough (several viewer pages can report a change at once).
        if (s.loading || s.revalidating) return
        if (!s.home && s.searchQuery == null && CrUri.canonical(s.currentUri) == CrUri.canonical(CrUri.parent(file.path))) refresh()
    }

    /** Fylvo: the version to ask for when saving: pinned inside connected storages (the listed file). */
    private fun saveEntity(file: FileObject): String =
        if (Connected.isConnected(file.path)) file.primaryEntity else ""

    /**
     * Downloads [file] somewhere the user can actually find it: the MediaStore
     * Downloads collection on API 29+, the app's external files dir below that.
     * Folders come down as a server-built zip named "<folder>.zip". Progress
     * shows in [FilesUiState.downloading]; the snackbar reports the saved name.
     */
    fun saveToDevice(context: Context, file: FileObject) {
        val key = "save:${file.path}"
        downloadJobs.remove(key)?.cancel()
        // Fylvo: already being saved by a download of several files.
        if (file.path in batchPaths) return
        setDownloading(file.path, null)
        val job = viewModelScope.launch {
            try {
                val savedName = withContext(Dispatchers.IO) { saveToDeviceIo(context, file) }
                _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.saved_file, savedName)))
            } catch (_: CancellationException) {
                // user cancelled; nothing to say
            } catch (e: Exception) {
                val message = downloadFailure(file, e)
                _state.update { it.copy(snackbar = message) }
            } finally {
                releaseSingle(key, file.path, coroutineContext[Job])
            }
        }
        downloadJobs[key] = job
    }

    /**
     * Fylvo: end of a single open or save job [me]. A replaced (cancelled) job must not drop the
     * entry or the progress mark of the job that took over the same file.
     */
    private fun releaseSingle(key: String, path: String, me: Job?) {
        if (downloadJobs[key] === me) downloadJobs.remove(key)
        val busy = downloadJobs[path] != null || downloadJobs["save:$path"] != null || path in batchPaths
        if (!busy) clearDownloading(path)
    }

    /**
     * Fylvo: paths claimed by the running download of several files (main thread only). Each is
     * released when its file is done; single open/save leave them alone meanwhile.
     */
    private val batchPaths = HashSet<String>()

    /**
     * Fylvo: saves several selected [files] one after another like [saveToDevice], with one
     * message at the end. One such download at a time; files already being fetched are skipped.
     */
    fun saveAllToDevice(context: Context, files: List<FileObject>) {
        if (_state.value.batchSaving) return
        val mine = files.filter { it.path !in _state.value.downloading }
        if (mine.isEmpty()) return
        // Claimed right away, before anything suspends, so a single open/save sees them.
        batchPaths += mine.map { it.path }
        _state.update { s -> s.copy(batchSaving = true, downloading = s.downloading + mine.map { it.path to null }) }
        val job = viewModelScope.launch {
            var saved = 0
            var lastError: Exception? = null
            var lastFailed: FileObject? = null
            var changedFile: FileObject? = null
            var changedError: Exception? = null
            try {
                for (file in mine) {
                    try {
                        withContext(Dispatchers.IO) { saveToDeviceIo(context, file) }
                        saved++
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        lastError = e
                        lastFailed = file
                        if (changedFile == null && Connected.isConnected(file.path) && Connected.isChanged(Connected.normalize(e))) {
                            changedFile = file
                            changedError = e
                        }
                    } finally {
                        batchPaths -= file.path
                        clearDownloading(file.path)
                    }
                }
                val failed = lastError
                // Fylvo: a file of a connected storage changed meanwhile: the listing is refreshed once
                // and the message says to try again.
                val changedOne = changedFile
                val changedCause = changedError
                val message = when {
                    saved == mine.size -> UiMessage(UiText.Plural(R.plurals.saved_files, saved, saved))
                    changedOne != null && changedCause != null -> downloadFailure(changedOne, changedCause).let {
                        // Some files were saved: the count stays the message, the changed one is to try again.
                        if (saved == 0) it else UiMessage(UiText.Res(R.string.saved_some_files, saved, mine.size), long = true)
                    }
                    saved == 0 && failed != null -> lastFailed?.let { downloadFailure(it, failed) } ?: failure(failed, fallback = R.string.download_failed)
                    else -> UiMessage(UiText.Res(R.string.saved_some_files, saved, mine.size))
                }
                _state.update { it.copy(snackbar = message) }
            } finally {
                // Cancelled: release what is left.
                mine.forEach { if (batchPaths.remove(it.path)) clearDownloading(it.path) }
                downloadJobs.remove(BATCH_SAVE_KEY)
                _state.update { it.copy(batchSaving = false) }
            }
        }
        downloadJobs[BATCH_SAVE_KEY] = job
    }

    private suspend fun saveToDeviceIo(context: Context, file: FileObject): String {
        val archive = file.isFolder
        val url = repo.downloadUrl(file.path, archive = archive, entity = saveEntity(file))
        val displayName = if (archive) "${file.name}.zip" else Connected.downloadName(file)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            val resolver = context.contentResolver
            val values = ContentValues().apply {
                put(MediaStore.Downloads.DISPLAY_NAME, displayName)
                put(
                    MediaStore.Downloads.MIME_TYPE,
                    if (archive) "application/zip"
                    else java.net.URLConnection.guessContentTypeFromName(displayName)
                        ?: "application/octet-stream",
                )
                put(MediaStore.Downloads.RELATIVE_PATH, "${Environment.DIRECTORY_DOWNLOADS}/Fylvo")
                put(MediaStore.MediaColumns.IS_PENDING, 1)
            }
            val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                ?: throw ApiException(-1, "Could not create download")
            try {
                resolver.openOutputStream(uri)!!.use { out -> streamTo(url, out, file.path) }
            } catch (e: Exception) {
                resolver.delete(uri, null, null)
                throw e
            }
            resolver.update(
                uri,
                ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) },
                null, null,
            )
            return displayName
        }
        // API 28: no MediaStore Downloads collection; the app-specific external
        // dir needs no permission and survives cache eviction.
        val dir = context.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)
            ?: File(context.filesDir, "downloads")
        dir.mkdirs()
        var dest = File(dir, displayName)
        var n = 1
        // Fylvo: the name is taken at once (createNewFile is atomic), so two saves of files with the
        // same name running at the same time never write into the same file.
        while (!dest.createNewFile()) {
            dest = File(dir, if (archive) "${file.name} ($n).zip" else numberedName(displayName, n))
            n++
        }
        try {
            repo.downloadToFile(url, dest) { done, total ->
                setDownloading(file.path, if (total > 0) done.toFloat() / total else null)
            }
        } catch (e: Exception) {
            dest.delete()
            throw e
        }
        return dest.name
    }

    /** Fylvo: "Budget (1).xlsx" for a taken "Budget.xlsx"; a name without extension gets " (1)" at the end. */
    private fun numberedName(name: String, n: Int): String {
        val dot = name.lastIndexOf('.')
        return if (dot > 0) "${name.substring(0, dot)} ($n)${name.substring(dot)}" else "$name ($n)"
    }

    /** Streams a download [url] into [out] with throttled progress for [path]. */
    private suspend fun streamTo(url: String, out: OutputStream, path: String) {
        val resp = repo.download(url)
        if (!resp.isSuccessful) {
            resp.errorBody()?.close()
            throw ApiException(resp.code(), "Download failed: HTTP ${resp.code()}")
        }
        val body = resp.body() ?: throw ApiException(-1, "Empty download")
        val total = body.contentLength()
        var done = 0L
        val buffer = ByteArray(256 * 1024)
        body.byteStream().use { input ->
            out.use { output ->
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val read = input.read(buffer)
                    if (read < 0) break
                    output.write(buffer, 0, read)
                    done += read
                    setDownloading(path, if (total > 0) done.toFloat() / total else null)
                }
            }
        }
    }

    /** Signed inline URL for streaming a preview (image, video, audio). */
    suspend fun previewUrl(file: FileObject): String {
        currentOfflineCopy(file)?.let { return android.net.Uri.fromFile(it).toString() }
        return try {
            // Fylvo: inside a connected storage the listed version, so a replaced file is not shown.
            repo.contentUrl(file.path, entity = saveEntity(file)).also { markFresh(file) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            anyOfflineCopy(file)?.let { android.net.Uri.fromFile(it).toString() } ?: throw connectedError(file, e)
        }
    }

    /**
     * Fylvo: the copy on this phone of a file kept offline when it is the version the listing
     * shows (any copy while the folder is shown from the phone); null otherwise.
     */
    private suspend fun currentOfflineCopy(file: FileObject): File? = when {
        // Fylvo: the working copy first (also outside an offline root, it may have outlived it).
        _state.value.editOf(file) != null -> edits.workingCopy(file.id) ?: offline.localCopy(file.path, entity = file.primaryEntity.ifEmpty { null })
        !_state.value.isOffline(file) -> null
        _state.value.offlineMode -> offline.localCopy(file.path)
        file.primaryEntity.isNotEmpty() -> offline.localCopy(file.path, entity = file.primaryEntity)
        else -> null
    }

    /**
     * Fylvo: any copy on this phone, for when the server answered with an error or cannot be
     * reached; says that it may be outdated.
     */
    private suspend fun anyOfflineCopy(file: FileObject): File? {
        if (!_state.value.isOffline(file)) return null
        val local = offline.localCopy(file.path) ?: return null
        _state.value = _state.value.copy(
            snackbar = UiMessage(UiText.Res(R.string.offline_copy_may_be_outdated)),
            staleCopies = _state.value.staleCopies + file.path,
        )
        return local
    }

    /** The server answered for [file] again: no "may be outdated" note for it any more. */
    private fun markFresh(file: FileObject) {
        if (file.path in _state.value.staleCopies) _state.value = _state.value.copy(staleCopies = _state.value.staleCopies - file.path)
    }

    /** Local copy for renderers that need a file (PDF, text); downloads if missing. */
    suspend fun localCopy(context: Context, file: FileObject, onProgress: (Float?) -> Unit): File {
        currentOfflineCopy(file)?.let { return it }
        val dest = cacheFileFor(context, file)
        if (cachedCopy(dest, file)) return dest
        return try {
            val url = repo.downloadUrl(file.path, entity = file.primaryEntity)
            repo.downloadToFile(url, dest) { done, total -> onProgress(if (total > 0) done.toFloat() / total else null) }
            markFresh(file)
            dest
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            anyOfflineCopy(file) ?: throw connectedError(file, e)
        }
    }

    /** Files of the folder on screen that open in the in-app viewer, in list order. */
    fun previewableSiblings(file: FileObject): List<FileObject> {
        val kind = FileKind.of(file)
        val gallery = kind == FileKind.IMAGE || kind == FileKind.VIDEO
        return _state.value.files.filter {
            // Fylvo: inside a connected storage only rows that may be opened in the app.
            !it.isFolder && !it.isUploading &&
                (!Connected.isConnected(it.path) || (Connected.tapAction(it) == Connected.Tap.OPEN && !Connected.sizeUnknown(it))) && if (gallery) FileKind.of(it).let { k -> k == FileKind.IMAGE || k == FileKind.VIDEO } else FileKind.of(it) == kind
        }.ifEmpty { listOf(file) }
    }

    // ---- view preferences -------------------------------------------------

    fun toggleViewMode() {
        val s = _state.value
        val next = if (s.effectiveMode == ViewMode.LIST) ViewMode.GRID else ViewMode.LIST
        viewModelScope.launch {
            if (s.category?.gallery == true) viewSettings.setGalleryMode(next) else viewSettings.setMode(next)
        }
    }

    /** Picking the active key again flips the direction, like the Files app. */
    fun sortBy(by: SortBy) {
        val cur = _state.value.view.sort
        // Fylvo: inside a connected storage a saved "created" sort counts as the "modified" it is shown as.
        val shown = if (_state.value.inConnected) Connected.sortFor(cur.by) else cur.by
        val next = if (shown == by) SortOrder(by, ascending = !cur.ascending)
        else SortOrder(by, ascending = by == SortBy.NAME)
        viewModelScope.launch { viewSettings.setSort(next) }
    }

    // ---- selection --------------------------------------------------------

    fun toggleSelected(file: FileObject) {
        val cur = _state.value.selected
        _state.value = _state.value.copy(
            selected = if (file.path in cur) cur - file.path else cur + file.path,
        )
    }

    /** Fylvo: the selection while a finger drags over the list. */
    fun setSelected(paths: Set<String>) {
        _state.value = _state.value.copy(selected = paths)
    }

    /** Select or clear a whole date group from its header control; unfinished uploads stay out. */
    fun toggleGroup(files: List<FileObject>) {
        val paths = files.filter { !it.isUploading }.map { it.path }
        val cur = _state.value.selected
        _state.value = _state.value.copy(selected = if (paths.all { it in cur }) cur - paths.toSet() else cur + paths)
    }

    /** Every loaded file except unfinished uploads (only a direct long press selects one of those). */
    fun selectAll() {
        val paths = _state.value.files.filter { !it.isUploading }.map { it.path }
        if (paths.isEmpty()) return
        _state.value = _state.value.copy(selected = _state.value.selected + paths)
    }

    fun clearSelection() {
        _state.value = _state.value.copy(selected = emptySet())
    }

    /**
     * Fylvo: a move or delete inside a connected storage. The row ids are those taken when the
     * action started ([ids]) or, if none were given, those of the listed rows. At most
     * [Connected.BATCH_LIMIT] addresses per request, one request after the other; the first
     * failure stops the rest. Afterwards the done paths leave the selection, the listings of the
     * folders involved are dropped and the folder on screen is listed again from the start (a
     * write ends its paging); the message counts only what was done.
     */
    private fun connectedWrite(
        paths: List<String>,
        ids: Map<String, String>?,
        target: String?,
        done: (Int) -> UiText,
        send: suspend (List<String>, Map<String, String>) -> Unit,
    ) {
        val rowIds = ids ?: Connected.idsOf(_state.value.files.filter { it.path in paths })
        viewModelScope.launch {
            val result = connectedBatches(paths) { chunk -> send(chunk, rowIds.filterKeys { it in chunk }) }
            val folders = paths.map(CrUri::parent).toSet() + listOfNotNull(target)
            listingCache.keys.removeAll { it in folders }
            val n = result.done.size
            val message = when (val e = result.error) {
                null -> UiMessage(done(n))
                else -> if (n == 0) failure(e, paths)
                else UiMessage(UiText.Res(R.string.connected_partly_done, n, paths.size, failure(e, paths).text), long = true)
            }
            _state.update { it.copy(selected = it.selected - result.done.toSet(), snackbar = message) }
            if (result.error != null && handleSessionExpiry(result.error)) return@launch
            val shown = _state.value
            if (!shown.home && shown.currentUri in folders) refresh()
        }
    }

    /**
     * Fylvo: a failed request on [paths]. A batch that failed only in part changed the folder
     * anyway, so the listing is reloaded too (the message stays).
     */
    private fun reportFailure(e: Throwable, paths: List<String>) {
        _state.value = _state.value.copy(snackbar = failure(e, paths))
        if (e is ApiException && e.code == ErrorText.CODE_BATCH_NOT_COMPLETED) refresh()
    }

    /** Fylvo: true once the one-time selection hint was asked for in this run. */
    private var selectionHintAsked = false

    /**
     * Fylvo: shows the one-time hint about long press and drag, unless it was shown before. When
     * another message is waiting it does nothing; the screen asks again once that one is gone.
     */
    fun showSelectionHint() {
        if (selectionHintAsked || _state.value.snackbar != null) return
        selectionHintAsked = true
        viewModelScope.launch {
            if (viewSettings.selectionHintShown.first()) return@launch
            if (_state.value.snackbar != null) {
                selectionHintAsked = false
                return@launch
            }
            _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.selection_hint), long = true))
            viewSettings.setSelectionHintShown()
        }
    }

    /**
     * Move or copy [paths] into [dst]; the list refreshes and the cache for both folders is dropped.
     * Fylvo: inside a connected storage [ids] are the row ids taken when the action started (the
     * listed rows are asked otherwise) and [dstId] the id of the target folder as listed. Copy is
     * never offered there.
     */
    fun moveTo(paths: List<String>, dst: String, copy: Boolean, ids: Map<String, String>? = null, dstId: String? = null) {
        if (paths.isEmpty()) return
        if (Connected.isConnected(dst) || paths.any(Connected::isConnected)) {
            val target = dstId?.takeIf { it.isNotEmpty() }?.let { mapOf(dst to it) }.orEmpty()
            connectedWrite(paths, ids, dst, done = { n -> UiText.Plural(R.plurals.moved_items, n, n, CrUri.displayName(dst)) }) { chunk, chunkIds ->
                repo.move(chunk, dst, copy = false, ids = chunkIds + target)
            }
            return
        }
        viewModelScope.launch {
            runCatching { repo.move(paths, dst, copy) }
                .onSuccess {
                    listingCache.remove(dst)
                    val n = paths.size
                    val where = CrUri.displayName(dst)
                    val text = if (copy) R.plurals.copied_items else R.plurals.moved_items
                    _state.value = _state.value.copy(
                        // Fylvo: moved files leave the selection; after a copy it is done.
                        selected = if (copy) emptySet() else _state.value.selected - paths.toSet(),
                        snackbar = UiMessage(UiText.Plural(text, n, n, where)),
                    )
                    refresh()
                }
                .onFailure { reportFailure(it, paths) }
        }
    }

    /**
     * Subfolders of [uri] for the folder picker. Fylvo: inside a connected storage only folders that
     * may be entered, and the listed folder itself is kept for [pickerAllowsMoveInto].
     */
    suspend fun listFolders(uri: String): List<FileObject> {
        if (!Connected.isConnected(uri)) return repo.listFolders(uri)
        // Only what this listing says counts: an earlier one is forgotten first.
        val key = CrUri.canonical(uri)
        pickerFolders.remove(key)
        val (folders, parent) = repo.listFoldersWithParent(uri)
        if (parent != null) pickerFolders[key] = parent
        return folders.filter(Connected::canEnter)
    }

    /**
     * Fylvo: folders the picker listed inside connected storages, by address. A state map, so the
     * picker's buttons follow when a listing arrives (written on the main thread by the picker).
     */
    private val pickerFolders = androidx.compose.runtime.mutableStateMapOf<String, FileObject>()

    /** Fylvo: the picker may move into (and create in) [uri]: always outside connected storages. */
    fun pickerAllowsMoveInto(uri: String): Boolean =
        !Connected.isConnected(uri) || Connected.canCreateIn(pickerFolders[CrUri.canonical(uri)])

    /** Fylvo: the id of the folder [uri] as the picker listed it (sent with a move), or null. */
    fun pickerFolderId(uri: String): String? = pickerFolders[CrUri.canonical(uri)]?.id?.takeIf { it.isNotEmpty() }

    // ---- search -----------------------------------------------------------
    //
    // Name search through the listing API's ?name= filter (case-folded), not
    // the /file/search full-text endpoint: that one is off unless the admin
    // enabled FTS, and answered "feature not enabled" on a stock server.

    private var searchJob: Job? = null

    /** Opens search; scoped to the current folder when inside one, otherwise everywhere. */
    fun openSearch() {
        val s = _state.value
        val scope = if (!s.home && !s.virtual && CrUri.isSearchScope(s.currentUri)) s.currentUri else null
        _state.value = s.copy(searchQuery = "", searchScope = scope, searchType = null, searchResults = emptyList(), selected = emptySet())
    }

    fun setSearchScope(folder: String?) {
        _state.value = _state.value.copy(searchScope = folder)
        runSearch()
    }

    fun setSearchType(type: Category?) {
        _state.value = _state.value.copy(searchType = type)
        runSearch()
    }

    /** Called as the user types; results are fetched after a short pause. */
    fun search(query: String) {
        _state.value = _state.value.copy(searchQuery = query)
        runSearch(debounceMs = 300)
    }

    private fun searchUri(): String? {
        val s = _state.value
        val q = s.searchQuery?.trim().orEmpty()
        if (q.isEmpty()) return null
        val base = s.searchScope ?: CrUri.MY_PREFIX
        val encoded = java.net.URLEncoder.encode(q, "UTF-8").replace("+", "%20")
        return buildString {
            append(base).append("?name=").append(encoded).append("&case_folding")
            s.searchType?.filter?.let { append('&').append(it) }
        }
    }

    private fun runSearch(debounceMs: Long = 0) {
        searchJob?.cancel()
        val uri = searchUri()
        if (uri == null) {
            _state.value = _state.value.copy(searchResults = emptyList(), searchNextToken = null, searchLoading = false, searchLoadingMore = false, searchError = null)
            return
        }
        _state.value = _state.value.copy(searchLoading = true, searchError = null, searchLoadingMore = false)
        searchJob = viewModelScope.launch {
            if (debounceMs > 0) kotlinx.coroutines.delay(debounceMs)
            try {
                val res = repo.list(uri, orderBy = "updated_at", orderDirection = "desc")
                _state.value = _state.value.copy(
                    searchLoading = false,
                    searchResults = res.files,
                    searchNextToken = res.pagination.nextToken,
                )
                prefetchThumbs(res.files)
            } catch (_: CancellationException) {
            } catch (e: Exception) {
                if (!handleSessionExpiry(e)) _state.value = _state.value.copy(searchLoading = false, searchError = userError(e))
            }
        }
    }

    fun searchMore() {
        val s = _state.value
        val token = s.searchNextToken ?: return
        val uri = searchUri() ?: return
        if (s.searchLoadingMore || s.searchLoading) return
        _state.value = s.copy(searchLoadingMore = true)
        viewModelScope.launch {
            try {
                val res = repo.list(uri, pageToken = token, orderBy = "updated_at", orderDirection = "desc")
                // A newer query replaced this one while the page was in flight.
                if (searchUri() != uri) return@launch
                _state.value = _state.value.copy(
                    searchLoadingMore = false,
                    searchResults = _state.value.searchResults + res.files,
                    searchNextToken = res.pagination.nextToken,
                )
            } catch (e: Exception) {
                if (searchUri() == uri) {
                    _state.value = _state.value.copy(searchLoadingMore = false, snackbar = failure(e))
                }
            }
        }
    }

    fun clearSearch() {
        searchJob?.cancel()
        _state.value = _state.value.copy(
            searchQuery = null,
            searchScope = null,
            searchType = null,
            searchResults = emptyList(),
            searchNextToken = null,
            searchLoading = false,
            searchLoadingMore = false,
            searchError = null,
        )
    }

    // ---- file operations --------------------------------------------------

    fun mkdir(name: String) = mkdirIn(_state.value.currentUri, name)

    /** Creates [name] under [parent]; refreshes if that is the folder on screen. */
    fun mkdirIn(parent: String, name: String) {
        if (name.isBlank()) return
        viewModelScope.launch { createFolder(parent, name) }
    }

    /**
     * Fylvo: creates [name] under [parent] and tells whether it worked (the folder picker lists
     * again only then). Inside a connected storage a name with "/" is refused (it would address a
     * folder further down).
     */
    suspend fun createFolder(parent: String, name: String): Boolean {
        if (name.isBlank()) return false
        if (Connected.isConnected(parent) && '/' in name) {
            _state.update { it.copy(snackbar = UiMessage(UiText.Res(R.string.error_bad_name))) }
            return false
        }
        // In the view model's scope: a confirmed folder is created also when the picker closes.
        return viewModelScope.async {
            try {
                repo.mkdir(CrUri.join(parent, name))
                listingCache.remove(parent)
                if (parent == _state.value.currentUri) refresh()
                true
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.update { it.copy(snackbar = failure(e)) }
                // The folder may exist although the answer was lost: show the folder as it is now.
                if (Connected.isConnected(parent)) {
                    listingCache.remove(parent)
                    if (parent == _state.value.currentUri) refresh()
                }
                false
            }
        }.await()
    }

    fun rename(file: FileObject, newName: String) {
        if (newName.isBlank() || newName == file.name) return
        if (Connected.isConnected(file.path) && '/' in newName) {
            _state.update { it.copy(snackbar = UiMessage(UiText.Res(R.string.error_bad_name))) }
            return
        }
        viewModelScope.launch {
            // Fylvo: inside a connected storage the row's id goes along (taken from the row acted on).
            runCatching { repo.rename(file.path, newName, Connected.idsOf(listOf(file))) }
                .also { if (Connected.isConnected(file.path)) listingCache.remove(CrUri.parent(file.path)) }
                .onSuccess {
                    // Fylvo: the old name leaves the selection.
                    _state.value = _state.value.copy(selected = _state.value.selected - file.path)
                    refresh()
                }
                .onFailure {
                    _state.value = _state.value.copy(snackbar = failure(it, listOf(file.path)))
                    // Fylvo: a row that changed meanwhile is shown as it is now.
                    if (Connected.isConnected(file.path)) refresh()
                }
        }
    }

    fun delete(file: FileObject) = deletePaths(listOf(file.path), Connected.idsOf(listOf(file)))

    /**
     * Moves to trash and offers Undo, which restores from the trash by origin path. Fylvo: inside a
     * connected storage into the provider's trash, without Undo; [ids] as for [moveTo].
     */
    fun deletePaths(paths: List<String>, ids: Map<String, String>? = null) {
        if (paths.isEmpty()) return
        if (paths.any(Connected::isConnected)) {
            val storage = CrUri.rootLabel(paths[0])
            connectedWrite(paths, ids, null, done = { n ->
                if (n == 1) UiText.Res(R.string.connected_deleted_one, CrUri.fileName(paths[0]), storage)
                else UiText.Res(R.string.connected_deleted_many, n, storage)
            }) { chunk, chunkIds -> repo.delete(chunk, ids = chunkIds) }
            return
        }
        viewModelScope.launch {
            runCatching { repo.delete(paths) }
                .onSuccess {
                    val n = paths.size
                    // Fylvo: inside a share the items go to the owner's trash, which a member
                    // cannot list, so there is no Undo there.
                    val inShare = CrUri.isShare(paths[0])
                    _state.value = _state.value.copy(
                        selected = _state.value.selected - paths.toSet(),
                        snackbar = UiMessage(
                            text = when {
                                n == 1 && inShare -> UiText.Res(R.string.moved_to_owner_trash_one, CrUri.fileName(paths[0]))
                                n == 1 -> UiText.Res(R.string.moved_to_trash_one, CrUri.fileName(paths[0]))
                                inShare -> UiText.Res(R.string.moved_to_owner_trash_many, n)
                                else -> UiText.Res(R.string.moved_to_trash_many, n)
                            },
                            actionLabel = if (inShare) null else UiText.Res(R.string.undo),
                            action = if (inShare) null else ({ undoDelete(paths) }),
                        ),
                    )
                    refresh()
                }
                .onFailure { reportFailure(it, paths) }
        }
    }

    private fun undoDelete(originalPaths: List<String>) {
        viewModelScope.launch {
            runCatching {
                val wanted = originalPaths.toSet()
                // The just-deleted items sit at the top of the trash; page
                // through in case the trash already held more than one page.
                val trashed = mutableListOf<String>()
                var token: String? = null
                var pages = 0
                do {
                    val page = repo.list(
                        Category.TRASH.uri, pageToken = token,
                        orderBy = "updated_at", orderDirection = "desc", pageSize = 200,
                    )
                    trashed += page.files.filter { it.restoreUri in wanted }.map { it.path }
                    token = page.pagination.nextToken
                    pages++
                } while (trashed.size < wanted.size && !token.isNullOrEmpty() && pages < UNDO_MAX_PAGES)
                if (trashed.isEmpty()) return@runCatching false
                repo.restore(trashed)
                true
            }
                .onSuccess { found ->
                    if (!found) {
                        _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.trash_items_not_found)))
                        return@onSuccess
                    }
                    listingCache.clear()
                    _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.restored)))
                    refresh()
                }
                .onFailure { _state.value = _state.value.copy(snackbar = failure(it)) }
        }
    }

    /**
     * Fylvo: switches offline on or off for [file]. A file becomes a single-file root at once (when
     * at least 1 GB stays free); a folder is counted first and asked about ([FilesUiState.offlinePrompt]).
     */
    fun toggleOffline(file: FileObject) {
        val s = _state.value
        val uri = offlineUriOf(file)
        s.offlineCoverage.rootAt(uri)?.let { root ->
            removeOffline(root.id, root.name)
            return
        }
        // Inside an offline folder the folder's copy is used; nothing to switch.
        if (s.offlineCoverage.coveringFolder(uri) != null || !canKeepOffline(file)) return
        if (!file.isFolder) {
            viewModelScope.launch {
                if (!hasRoomFor(file.size, offline.freeBytes())) {
                    _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.offline_no_room)))
                    return@launch
                }
                if (offline.addRoot(uri, file.name, folder = false)) {
                    _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.offline_switched_on, file.name)))
                }
            }
            return
        }
        _state.value = s.copy(offlinePrompt = OfflinePrompt(uri = uri, name = file.name))
        viewModelScope.launch {
            try {
                val tree = repo.listTree(uri)
                val bytes = tree.files.sumOf { it.size }
                val free = offline.freeBytes()
                val refusal = when {
                    !tree.complete -> OfflinePrompt.Refusal.TooManyFolders
                    !hasRoomFor(bytes, free) -> OfflinePrompt.Refusal.NoRoom
                    else -> null
                }
                if (_state.value.offlinePrompt?.uri == uri) {
                    _state.value = _state.value.copy(
                        offlinePrompt = OfflinePrompt(uri, file.name, loading = false, files = tree.files.size, bytes = bytes, freeBytes = free, refusal = refusal),
                    )
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (handleSessionExpiry(e)) return@launch
                _state.value = _state.value.copy(offlinePrompt = null, snackbar = failure(e))
            }
        }
    }

    /** The person agreed to keep the counted folder offline. */
    fun confirmOffline() {
        val prompt = _state.value.offlinePrompt ?: return
        _state.value = _state.value.copy(offlinePrompt = null)
        if (prompt.loading || prompt.refusal != null) return
        viewModelScope.launch {
            if (offline.addRoot(prompt.uri, prompt.name, folder = true)) {
                _state.value = _state.value.copy(snackbar = UiMessage(UiText.Res(R.string.offline_switched_on, prompt.name)))
            }
        }
    }

    fun dismissOfflinePrompt() {
        _state.value = _state.value.copy(offlinePrompt = null)
    }

    /**
     * Switches an offline root off: its copies on this phone are deleted. Fylvo: when files edited
     * in it hold changes the server does not have, it asks first ([FilesUiState.rootPrompt]).
     */
    fun removeOffline(rootId: String, name: String) {
        viewModelScope.launch {
            val open = edits.rootBlockers(rootId)
            if (open.isNotEmpty()) {
                _state.update { it.copy(rootPrompt = RootEditPrompt(rootId, name, open)) }
                return@launch
            }
            switchOff(rootId, name)
        }
    }

    private suspend fun switchOff(rootId: String, name: String) {
        offline.removeRoot(rootId)
        _state.update { it.copy(snackbar = UiMessage(UiText.Res(R.string.offline_switched_off, name))) }
    }

    /** Fylvo: the answer to [FilesUiState.rootPrompt]: upload first (true) or discard (false); the root goes after. */
    fun resolveRootPrompt(upload: Boolean) {
        val prompt = _state.value.rootPrompt ?: return
        if (prompt.busy) return
        _state.update { it.copy(rootPrompt = prompt.copy(busy = true)) }
        viewModelScope.launch {
            // Why it did not work, or null when the root can go.
            val problem = if (upload) {
                edits.upload(prompt.entries)?.let { reason ->
                    val ids = prompt.entries.mapTo(HashSet()) { it.id }
                    val code = _state.value.editEntries.filter { it.id in ids }.map(::refusalCode).firstOrNull { it > 0 } ?: 0
                    UiText.Res(R.string.edit_upload_failed, notUploadedText(reason, code))
                }
            } else {
                UiText.Res(R.string.edit_discard_failed, prompt.name).takeUnless { edits.discardAll(prompt.entries.mapTo(HashSet()) { it.id }) }
            }
            _state.update { it.copy(rootPrompt = null) }
            if (problem == null) switchOff(prompt.rootId, prompt.name)
            else _state.update { it.copy(snackbar = UiMessage(problem)) }
        }
    }

    fun dismissRootPrompt() {
        if (_state.value.rootPrompt?.busy == true) return
        _state.update { it.copy(rootPrompt = null) }
    }

    /**
     * Fylvo: "Bearbeiten": makes (or reopens) the working copy of [file], then [open] hands it to an
     * editor. Not offered for a copy that is not the current version yet.
     */
    fun startEditing(file: FileObject, open: (File) -> Unit) {
        viewModelScope.launch {
            when (val result = edits.startEditing(file, _state.value.offlineMode)) {
                is OfflineEdits.StartResult.Ready -> {
                    open(result.file)
                    edits.opened(result.entryId)
                }
                OfflineEdits.StartResult.NotReady ->
                    _state.update { it.copy(snackbar = UiMessage(UiText.Res(R.string.edit_not_ready))) }
            }
        }
    }

    /** Fylvo: "Bearbeitung abschliessen" for [entry]; asks what to do when the changes could not go up. */
    fun finishEditing(entry: EditEntry) {
        _state.update { it.copy(snackbar = UiMessage(UiText.Res(R.string.edit_uploading_now))) }
        viewModelScope.launch {
            when (val result = edits.finish(entry.id)) {
                OfflineEdits.FinishResult.Done ->
                    _state.update { it.copy(snackbar = UiMessage(UiText.Res(R.string.edit_finished, entry.name))) }
                is OfflineEdits.FinishResult.Failed -> _state.update { s ->
                    val now = s.editEntries.firstOrNull { it.id == entry.id }
                    s.copy(
                        editPrompt = EditPrompt(
                            entry.id, entry.name, result.reason,
                            unsent = now != null && (now.pending != null || now.refusedSnapshots.isNotEmpty()),
                            code = now?.let(::refusalCode) ?: 0,
                        ),
                    )
                }
            }
        }
    }

    /** Fylvo: "Änderungen verwerfen" (asked before): the working copy goes. */
    fun discardEditing(entryId: String, name: String) {
        _state.update { it.copy(editPrompt = null) }
        viewModelScope.launch {
            val text = if (edits.discard(entryId)) R.string.edit_discarded else R.string.edit_discard_failed
            _state.update { it.copy(snackbar = UiMessage(UiText.Res(text, name))) }
        }
    }

    /** Fylvo: "Später erneut versuchen": the working copy stays, a check comes soon. */
    fun retryEditLater() {
        _state.update { it.copy(editPrompt = null) }
        viewModelScope.launch { edits.checkSoon() }
    }

    fun dismissEditPrompt() {
        _state.update { it.copy(editPrompt = null) }
    }

    fun dismissEditNotice(entry: EditEntry) {
        viewModelScope.launch { edits.dismissNotice(entry.id) }
    }

    /** Fylvo: the working copy of [entry] for the Offline screen, to open it. */
    suspend fun editedFile(entry: EditEntry): File? = edits.workingCopy(entry.fileId)

    fun syncOfflineNow() {
        viewModelScope.launch { offline.syncNow() }
    }

    /** The copy on this phone of a single offline file, for the Offline screen. */
    suspend fun offlineFile(uri: String): File? = offline.localCopy(uri)

    suspend fun shareLink(
        file: FileObject,
        password: String = "",
        expireSeconds: Int = 0,
        downloads: Int = 0,
    ): String = repo.createShare(file.path, password = password, expireSeconds = expireSeconds, downloads = downloads)

    /** Trash: put items back where they were. */
    fun restore(paths: List<String>) {
        if (paths.isEmpty()) return
        viewModelScope.launch {
            runCatching { repo.restore(paths) }
                .onSuccess {
                    listingCache.clear()
                    _state.value = _state.value.copy(
                        selected = _state.value.selected - paths.toSet(),
                        snackbar = UiMessage(UiText.Plural(R.plurals.restored_items, paths.size, paths.size)),
                    )
                    refresh()
                }
                .onFailure { reportFailure(it, paths) }
        }
    }

    /** Trash: remove for good. */
    fun deleteForever(paths: List<String>) {
        if (paths.isEmpty()) return
        viewModelScope.launch {
            runCatching { repo.delete(paths, permanently = true) }
                .onSuccess {
                    _state.value = _state.value.copy(
                        selected = _state.value.selected - paths.toSet(),
                        snackbar = UiMessage(UiText.Plural(R.plurals.deleted_items_permanently, paths.size, paths.size)),
                    )
                    refresh()
                }
                .onFailure { reportFailure(it, paths) }
        }
    }

    /**
     * Resolve thumbnail URLs for the first screenfuls in parallel right after
     * a listing lands, so rows do not each pay a round trip as they scroll in.
     */
    private fun prefetchThumbs(files: List<FileObject>) {
        val wanted = files.filter { !repo.hasNoThumb(it) }.take(PREFETCH_THUMBS)
        if (wanted.isEmpty()) return
        viewModelScope.launch {
            val gate = kotlinx.coroutines.sync.Semaphore(PREFETCH_PARALLELISM)
            wanted.forEach { f ->
                launch { gate.withPermit { thumbUrl(f) } }
            }
        }
    }

    /** Fylvo: one request per file at a time and the cache live in the repository (shared with the file picker). */
    suspend fun thumbUrl(file: FileObject): String? = repo.cachedThumbUrl(file)

    /** Fylvo: true once the server said this version of the file has no preview. */
    fun hasNoThumb(file: FileObject): Boolean = repo.hasNoThumb(file)

    fun consumeSnackbar() {
        _state.value = _state.value.copy(snackbar = null)
    }

    /**
     * Fylvo: a connected storage that is gone or must be connected again: its cached listings and
     * the rows on screen go, the error screen shows (with the way to the web app for 40921), and
     * the Home section learns the new state.
     */
    private fun showStorageTrouble(uri: String, e: Exception) {
        val root = CrUri.root(uri)
        listingCache.keys.removeAll { CrUri.root(it) == root }
        connected.load()
        _state.update {
            it.copy(
                loading = false, revalidating = false, loadingMore = false, files = emptyList(), nextToken = null,
                parent = null, connected = null, selected = emptySet(), error = userError(e), errorCode = Connected.codeOf(e),
            )
        }
    }

    /**
     * Fylvo: a human sentence for a failed request on [paths]. When one of them is a file whose
     * upload has not finished, a lock answer also says that such uploads go away by themselves.
     */
    private fun failure(e: Throwable, paths: List<String> = emptyList(), @StringRes fallback: Int? = null): UiMessage {
        val involvesUpload = paths.isNotEmpty() && paths.any { it in uploadingPaths() }
        return UiMessage(userError(e, involvesUpload, fallback))
    }

    /** Paths of the files on screen whose upload has not finished (FileObject.isUploading). */
    private fun uploadingPaths(): Set<String> {
        val s = _state.value
        return (s.files + s.searchResults + s.recent).filter { it.isUploading }.mapTo(HashSet()) { it.path }
    }

    /**
     * Folders always stay on top like the Files app. The server's name order is
     * case-sensitive (uppercase first), which reads as random to people, so the
     * name sort is redone here case-insensitively; size and date come as-is.
     */
    private fun sortFiles(files: List<FileObject>): List<FileObject> {
        // Fylvo: a connected listing keeps the server order (folders first, pages fit together).
        if (Connected.isConnected(_state.value.currentUri)) return files
        val sort = _state.value.view.sort
        val byType = compareByDescending<FileObject> { it.isFolder }
        if (sort.by != SortBy.NAME) return files.sortedWith(byType)
        val byName = compareBy<FileObject, String>(String.CASE_INSENSITIVE_ORDER) { it.name }
        return files.sortedWith(byType.then(if (sort.ascending) byName else byName.reversed()))
    }
}
