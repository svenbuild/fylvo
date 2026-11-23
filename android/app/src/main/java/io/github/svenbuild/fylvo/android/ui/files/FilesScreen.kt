package io.github.svenbuild.fylvo.android.ui.files

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.DriveFolderUpload
import androidx.compose.material.icons.automirrored.outlined.Article
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.UploadFile
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.MusicNote
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshDefaults
import androidx.compose.material3.pulltorefresh.pullToRefresh
import androidx.compose.material3.pulltorefresh.rememberPullToRefreshState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ViewMode
import io.github.svenbuild.fylvo.android.data.offline.EditEntry
import io.github.svenbuild.fylvo.android.data.offline.needsAttention
import io.github.svenbuild.fylvo.android.ui.settings.AccountScreen
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.data.SortBy
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.Permissions
import io.github.svenbuild.fylvo.android.util.asString
import io.github.svenbuild.fylvo.android.work.UploadWorker
import io.github.svenbuild.fylvo.android.ui.files.transfers.PendingUpload
import io.github.svenbuild.fylvo.android.ui.files.transfers.UploadConflictDialog
import io.github.svenbuild.fylvo.android.ui.preview.PreviewScreen
import io.github.svenbuild.fylvo.android.ui.preview.previewKindOf
import java.io.File
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FilesScreen(
    viewModel: FilesViewModel,
) {
    val state by viewModel.state.collectAsState()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    val listState = rememberLazyListState()
    val gridState = rememberLazyGridState()

    var mkdirOpen by remember { mutableStateOf(false) }
    var addSheetOpen by remember { mutableStateOf(false) }
    var sheetTarget by remember { mutableStateOf<FileObject?>(null) }
    /** Fylvo: the sheet shows a favourite from the cached list: only what needs no current path. */
    var sheetFromFavorites by remember { mutableStateOf(false) }
    LaunchedEffect(sheetTarget) { if (sheetTarget == null) sheetFromFavorites = false }
    var preview by remember { mutableStateOf<Pair<List<FileObject>, Int>?>(null) }
    var renameTarget by remember { mutableStateOf<FileObject?>(null) }
    var deleteTarget by remember { mutableStateOf<FileObject?>(null) }
    /** Fylvo: the selected paths to delete, taken when the question opens. */
    var deleteSelectedTarget by remember { mutableStateOf<List<String>?>(null) }
    var deleteForeverTarget by remember { mutableStateOf<List<String>?>(null) }
    /** Fylvo: the working copy to discard, asked first. */
    var discardTarget by remember { mutableStateOf<EditEntry?>(null) }
    /** Paths to move/copy and whether it is a copy; non-null shows the folder picker. */
    var movePick by remember { mutableStateOf<Pair<List<String>, Boolean>?>(null) }
    /** Fylvo: row ids of a connected storage, taken when a move or delete of several rows starts. */
    var actionIds by remember { mutableStateOf<Map<String, String>>(emptyMap()) }
    /** Folder the search was opened from (scope chip), null when opened at root/home. */
    var searchOpenedFrom by remember { mutableStateOf<String?>(null) }
    /** Fylvo: the Account tab, and the offline files page reached from it. */
    // Saveable: a language change restarts the activity and should land on the Account tab again.
    var accountOpen by rememberSaveable { mutableStateOf(false) }
    var offlineOpen by remember { mutableStateOf(false) }
    /** Fylvo: Account › Customise. */
    var customizeOpen by rememberSaveable { mutableStateOf(false) }
    /** Fylvo 1.6: Account › profile or security ([ACCOUNT_PROFILE], [ACCOUNT_SECURITY]). */
    var accountPage by rememberSaveable { mutableStateOf<String?>(null) }
    /** Fylvo: a page of a place ([PAGE_FAVORITES], [PAGE_TAGS]) over the files, or null. */
    var page by rememberSaveable { mutableStateOf<String?>(null) }
    val searchFocus = remember { FocusRequester() }

    val inSearch = state.searchQuery != null
    // Fylvo: the account's customisation (star, colours, tags); empty on an older server.
    val app = context.applicationContext as io.github.svenbuild.fylvo.android.FylvoApp
    val prefsRepo = app.accountPrefs
    val prefsState by prefsRepo.state.collectAsState()
    // Fylvo 1.6: the account's group rights (null while not known: nothing that needs them is offered).
    val accountState by app.account.state.collectAsState()
    val groupShare = accountState.group(Permissions.GROUP_SHARE)
    LaunchedEffect(state.currentUri, accountOpen) {
        if (accountState.me == null || accountState.mapEnabled == null) runCatching { app.account.ensureLoaded() }
    }
    /** Fylvo: the file whose shares are listed ("Manage shares"). */
    var sharesOfTarget by remember { mutableStateOf<FileObject?>(null) }
    /** Fylvo 1.6: the file whose versions are listed, and "Empty trash" waiting for its yes. */
    var versionsTarget by remember { mutableStateOf<FileObject?>(null) }
    var emptyTrashAsk by remember { mutableStateOf(false) }
    /** Fylvo 1.6: an archive being looked into, one being extracted (target picker), files to pack. */
    var archiveView by remember { mutableStateOf<FileObject?>(null) }
    var extractPick by remember { mutableStateOf<FileObject?>(null) }
    var compressAsk by remember { mutableStateOf<List<FileObject>?>(null) }
    /** Fylvo 1.6: a folder being shared with people. */
    var peopleTarget by remember { mutableStateOf<PeopleTarget?>(null) }
    /** Fylvo 1.6: a folder of the phone read for uploading, waiting for its yes (target, its id). */
    var folderUploadAsk by remember { mutableStateOf<Triple<io.github.svenbuild.fylvo.android.work.FolderUpload.Scan, String, String?>?>(null) }
    var folderUploadTarget by rememberSaveable { mutableStateOf<String?>(null) }
    var folderReading by remember { mutableStateOf(false) }
    val folderLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { tree ->
        val target = folderUploadTarget
        folderUploadTarget = null
        if (tree == null || target == null || CrUri.hasSecret(target)) return@rememberLauncherForActivityResult
        folderReading = true
        scope.launch {
            val scan = withContext(Dispatchers.IO) {
                runCatching {
                    context.contentResolver.takePersistableUriPermission(tree, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    io.github.svenbuild.fylvo.android.work.FolderUpload.scan(context, tree)
                }.getOrNull()
            }
            folderReading = false
            if (scan == null || (scan.files.isEmpty() && scan.dirs.isEmpty())) {
                io.github.svenbuild.fylvo.android.work.FolderUpload.release(context, tree)
                snackbar.showSnackbar(context.getString(if (scan == null) R.string.folder_upload_unreadable else R.string.folder_upload_empty))
                return@launch
            }
            // The target folder is taken by id now; it must still be the same when the upload starts.
            val id = if (CrUri.isRoot(target)) null else runCatching { app.fileRepository.folderInfo(target).id }.getOrNull()
            folderUploadAsk = Triple(scan, target, id)
        }
    }
    val groupArchive = accountState.group(Permissions.GROUP_ARCHIVE)
    // Fylvo 1.6: the text editor; its view model keeps the text when the phone is turned.
    val editor = androidx.lifecycle.viewmodel.compose.viewModel(key = "text-editor") {
        io.github.svenbuild.fylvo.android.ui.editor.TextEditorViewModel(app.texts, app.sessionManager) { f -> viewModel.state.value.editOf(f) != null }
    }
    val editorState by editor.state.collectAsState()
    /** Fylvo 1.6: "New text file" / "New Markdown note" asks for a name first. */
    var newTextExt by remember { mutableStateOf<String?>(null) }
    /** Fylvo 1.6: a share link that needs its password (kept in memory only). */
    var linkPassword by remember { mutableStateOf<LinkPasswordAsk?>(null) }
    var linkBusy by remember { mutableStateOf(false) }


    /**
     * Fylvo 1.6: the further actions of one file, the same in its sheet and in the selection menu
     * (design/android-parity-plan.md). Each one follows its own rule (util/Permissions).
     */
    fun moreActions(file: FileObject): List<SelectionAction> {
        if (state.offlineMode || file.isUploading || state.inTrash) return emptyList()
        return listOfNotNull(
            SelectionAction.MANAGE_SHARES.takeIf { file.shared == true && Permissions.canShare(file, groupShare) },
            SelectionAction.SHARE_PEOPLE.takeIf { file.isFolder && Permissions.canShare(file, groupShare) },
            // Not while an offline working copy is open (its base would go stale unnoticed).
            SelectionAction.VERSIONS.takeIf { Permissions.canManageVersions(file) && state.editOf(file) == null },
            SelectionAction.VIEW_ARCHIVE.takeIf {
                !file.isFolder && io.github.svenbuild.fylvo.android.data.ArchiveRepository.viewable(file.name) && Permissions.canArchiveFrom(listOf(file), groupArchive)
            },
            SelectionAction.EXTRACT.takeIf {
                !file.isFolder && io.github.svenbuild.fylvo.android.data.ArchiveRepository.extractable(file.name) && Permissions.canArchiveFrom(listOf(file), groupArchive)
            },
            SelectionAction.COMPRESS.takeIf { Permissions.canArchiveFrom(listOf(file), groupArchive) && Permissions.canArchiveInto(state.parent) },
        )
    }

    fun runMore(action: SelectionAction, file: FileObject) {
        when (action) {
            SelectionAction.MANAGE_SHARES -> sharesOfTarget = file
            SelectionAction.VERSIONS -> versionsTarget = file
            SelectionAction.VIEW_ARCHIVE -> archiveView = file
            SelectionAction.SHARE_PEOPLE -> peopleTarget = PeopleTarget(file.path, null)
            SelectionAction.EXTRACT -> extractPick = file
            SelectionAction.COMPRESS -> compressAsk = listOf(file)
            else -> Unit
        }
    }

    var pendingUpload by remember { mutableStateOf<PendingUpload?>(null) }
    val haptics = LocalHapticFeedback.current

    /** Fylvo: the folder an upload was started in; kept when Android recreates the app behind the picker. */
    var uploadTarget by rememberSaveable { mutableStateOf<String?>(null) }
    val uploadLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        val parentUri = uploadTarget ?: state.currentUri
        uploadTarget = null
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        // Fylvo 1.6: never into a link with a password (the upload queue is stored).
        if (CrUri.hasSecret(parentUri)) return@rememberLauncherForActivityResult
        // Fylvo: the names on screen only when they are of the target folder.
        val loaded = if (parentUri == state.currentUri) state.files.map { it.name }.toSet() else emptySet()
        // Fylvo: names and the upload list are read and written off the main thread; with a few
        // hundred picked photos that takes a moment and must not freeze the screen.
        scope.launch {
            val pending = runCatching { withContext(Dispatchers.IO) {
                // All names in the folder, not only the loaded pages (a big folder has many).
                val existing = loaded + viewModel.namesInFolder(parentUri)
                PendingUpload(uris, UploadWorker.uniqueWithinSelection(uris.map { UploadWorker.displayName(context, it) }, existing), parentUri, existing)
            } }.getOrElse { e ->
                if (e is kotlinx.coroutines.CancellationException) throw e
                android.widget.Toast.makeText(context, context.getString(R.string.upload_could_not_start), android.widget.Toast.LENGTH_LONG).show()
                return@launch
            }
            if (pending.conflicts.isEmpty()) {
                // The transfer card above the files shows the batch as soon as it is queued.
                withContext(Dispatchers.IO) { UploadWorker.enqueueSafely(context, uris, parentUri, pending.names) }
            } else {
                pendingUpload = pending
            }
        }
    }

    LaunchedEffect(Unit) { if (!state.home) viewModel.refresh() }

    // Fylvo: back from the web app after connecting a storage again: its folder is asked again.
    // Only after a real pause, never on entering the screen (that would ask in a loop).
    val reconnectShown by rememberUpdatedState(state.inConnected && state.errorCode == Connected.CODE_RECONNECT)
    var wasPaused by remember { mutableStateOf(false) }
    LifecycleResumeEffect(Unit) {
        if (wasPaused && reconnectShown) viewModel.refresh()
        wasPaused = false
        onPauseOrDispose { wasPaused = true }
    }

    LaunchedEffect(state.snackbar) {
        val msg = state.snackbar ?: return@LaunchedEffect
        // Consume only after showing: clearing first would change this effect's
        // key and cancel it before the snackbar appears.
        val result = snackbar.showSnackbar(
            message = msg.text.resolve(context),
            actionLabel = msg.actionLabel?.resolve(context),
            duration = if (msg.action != null || msg.long) SnackbarDuration.Long else SnackbarDuration.Short,
        )
        viewModel.consumeSnackbar()
        if (result == SnackbarResult.ActionPerformed) msg.action?.invoke()
    }

    // Infinite scroll: load the next page when the end comes into view. Fylvo: checked again when a
    // refresh ends, since no page is loaded while it runs.
    LaunchedEffect(listState, gridState, state.effectiveMode, state.revalidating) {
        val lastVisible = if (state.effectiveMode == ViewMode.GRID) {
            snapshotFlow { gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index }
        } else {
            snapshotFlow { listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index }
        }
        lastVisible.collect { last ->
            if (last == null) return@collect
            if (inSearch) {
                if (last >= state.searchResults.size - 5) viewModel.searchMore()
            } else if (last >= state.files.size - 5) {
                viewModel.loadMore()
            }
        }
    }

    LaunchedEffect(inSearch) {
        if (inSearch) runCatching { searchFocus.requestFocus() }
    }

    fun openSearch() {
        val s = state
        searchOpenedFrom = if (!s.home && !s.virtual && CrUri.isSearchScope(s.currentUri)) s.currentUri else null
        viewModel.openSearch()
    }

    fun closeSearch() {
        viewModel.clearSearch()
        searchOpenedFrom = null
    }


    /** Fylvo: "Bearbeiten": the working copy goes to an editor; a message when none takes it. */
    fun editFile(file: FileObject) {
        viewModel.startEditing(file) { local ->
            val message = when (openForEdit(context, local)) {
                Editor.EDIT -> null
                Editor.VIEW -> R.string.edit_opened_to_view
                Editor.NONE -> R.string.edit_no_editor
            }
            if (message != null) scope.launch { snackbar.showSnackbar(context.getString(message)) }
        }
    }

    fun openWithSystemApp(file: FileObject) {
        viewModel.fetchForOpen(context, file) { local -> openWithSystem(context, local) }
    }

    /** In-app viewer for media, PDFs and text; everything else goes to the system. */
    fun openFile(file: FileObject, alone: Boolean = false) {
        // Fylvo: an unfinished upload has no content yet; its sheet says so.
        if (file.isUploading) {
            sheetTarget = file
            return
        }
        // Fylvo: a size the server does not know cannot be held to the preview limits.
        if (previewKindOf(file) == null || Connected.sizeUnknown(file)) {
            openWithSystemApp(file)
            return
        }
        val siblings = if (alone || inSearch || state.home) listOf(file) else viewModel.previewableSiblings(file)
        preview = siblings to siblings.indexOfFirst { it.path == file.path }.coerceAtLeast(0)
    }

    /**
     * Fylvo 1.6: opens the share [id] of a link of the signed-in server, with [typed] as its
     * password; asks for one when needed (design/android-parity-plan.md, 3).
     */
    fun openShareLink(id: String, typed: String) {
        if (linkBusy) return
        linkBusy = true
        val tag = app.sessionManager.sessionTag()
        scope.launch {
            try {
                runCatching { app.account.ensureLoaded() }
                val info = app.shares.visit(id, typed)
                if (!app.sessionManager.isCurrent(tag)) return@launch
                val account = app.account.state.value
                when (val d = io.github.svenbuild.fylvo.android.util.ShareLinks.decide(id, info, typed, account.me?.id, account.group(Permissions.GROUP_SHARE_DOWNLOAD))) {
                    is io.github.svenbuild.fylvo.android.util.ShareLinks.Decision.NeedPassword -> linkPassword = LinkPasswordAsk(id, d.name, d.wrong)
                    io.github.svenbuild.fylvo.android.util.ShareLinks.Decision.NotAllowed -> snackbar.showSnackbar(context.getString(R.string.link_not_allowed))
                    is io.github.svenbuild.fylvo.android.util.ShareLinks.Decision.Open -> {
                        linkPassword = null
                        accountOpen = false
                        offlineOpen = false
                        customizeOpen = false
                        page = null
                        viewModel.clearSelection()
                        if (inSearch) closeSearch()
                        CrUri.nameShare(d.root, d.name)
                        viewModel.openPlace(d.root)
                        // A shared single file opens right away: its preview, or its sheet when the app
                        // has none (no download without asking). Only while still on that share.
                        if (!info.isFolder) {
                            val single = runCatching { app.fileRepository.list(d.root).files.firstOrNull { !it.isFolder } }.getOrNull()
                            if (single != null && app.sessionManager.isCurrent(tag) && viewModel.state.value.currentUri == d.root) {
                                if (io.github.svenbuild.fylvo.android.ui.preview.previewKindOf(single) != null) openFile(single, alone = true)
                                else sheetTarget = single
                            }
                        }
                    }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                snackbar.showSnackbar(io.github.svenbuild.fylvo.android.util.userError(e).resolve(context))
            } finally {
                linkBusy = false
            }
        }
    }

    var shareTarget by remember { mutableStateOf<FileObject?>(null) }
    var detailsTarget by remember { mutableStateOf<FileObject?>(null) }
    fun shareLink(file: FileObject) { shareTarget = file }

    val selectLabel = stringResource(R.string.select)
    val deselectLabel = stringResource(R.string.deselect)
    val optionsLabel = stringResource(R.string.options)

    fun actionsFor(file: FileObject): FileActions = FileActions(
        onOpen = {
            when {
                // Fylvo: an unfinished upload joins a selection only by a long press.
                state.selecting -> if (!file.isUploading || file.path in state.selected) viewModel.toggleSelected(file)
                state.inTrash -> sheetTarget = file
                // Fylvo: inside a connected storage the row's bits decide (Connected.tapAction).
                Connected.isConnected(file.path) -> when (Connected.tapAction(file)) {
                    Connected.Tap.ENTER -> viewModel.navigateTo(file)
                    Connected.Tap.BROWSER -> Connected.externalUrl(file)?.let { openInBrowser(context, it) }
                    Connected.Tap.OPEN -> openFile(file)
                    Connected.Tap.SHEET -> sheetTarget = file
                }
                file.isFolder -> viewModel.navigateTo(file)
                else -> openFile(file)
            }
        },
        // Fylvo: the list detects the real long press (and drag); this one is for TalkBack.
        onLongPress = {
            haptics.performHapticFeedback(HapticFeedbackType.LongPress)
            viewModel.toggleSelected(file)
        },
        longPressLabel = if (file.path in state.selected) deselectLabel else selectLabel,
        longPressOnRow = false,
    )

    /**
     * Fylvo: up one level; from the top of a place opened from a page of the app (favourites,
     * tags) back to that page.
     */
    fun goUp() {
        val origin = state.origin
        if (origin != null && origin.startsWith(PAGE_ORIGIN) && state.atPlaceRoot) {
            viewModel.openMyFiles()
            page = origin.removePrefix(PAGE_ORIGIN)
            return
        }
        viewModel.navigateUp()
    }

    /** Fylvo: opens a place of the chips above My files; "up" leads back to [origin]. */
    fun openChip(place: io.github.svenbuild.fylvo.android.data.prefs.Place, origin: String = CrUri.MY_PREFIX) {
        viewModel.clearSelection()
        when (place) {
            io.github.svenbuild.fylvo.android.data.prefs.Place.Favorites -> { page = PAGE_FAVORITES; scope.launch { prefsRepo.refreshFavorites() } }
            io.github.svenbuild.fylvo.android.data.prefs.Place.Tags -> { page = PAGE_TAGS; scope.launch { prefsRepo.refreshFavorites() } }
            io.github.svenbuild.fylvo.android.data.prefs.Place.Shared -> viewModel.openCategory(Category.SHARED_WITH_ME, origin)
            io.github.svenbuild.fylvo.android.data.prefs.Place.Trash -> viewModel.openCategory(Category.TRASH, origin)
            io.github.svenbuild.fylvo.android.data.prefs.Place.Recent -> viewModel.openCategory(Category.RECENT, origin)
            is io.github.svenbuild.fylvo.android.data.prefs.Place.Category -> viewModel.openCategory(categoryOf(place), origin)
            is io.github.svenbuild.fylvo.android.data.prefs.Place.Link ->
                viewModel.openPlace(place.uri, title = place.name.takeIf { it.isNotEmpty() && CrUri.isSearch(place.uri) }, origin = origin)
            is io.github.svenbuild.fylvo.android.data.prefs.Place.Storage -> viewModel.openPlace(storageUri(place), origin = origin)
            io.github.svenbuild.fylvo.android.data.prefs.Place.Home -> viewModel.goHome()
            io.github.svenbuild.fylvo.android.data.prefs.Place.Files -> viewModel.openMyFiles()
            io.github.svenbuild.fylvo.android.data.prefs.Place.Offline -> { accountOpen = true; offlineOpen = true }
            io.github.svenbuild.fylvo.android.data.prefs.Place.Account -> accountOpen = true
        }
    }
    val connectedState by viewModel.connected.state.collectAsState()

    // Fylvo: Home from the account's building blocks (null: Home as before customisation).
    val homeExtras = io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.homeWidgets(prefsState.prefs.takeIf { prefsState.usable })?.let { widgets ->
        HomeExtras(
            widgets = widgets,
            favorites = prefsState.favorites?.files.orEmpty(),
            links = io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.links(prefsState.prefs),
            tags = prefsState.tags,
            onOpenFavorite = { file ->
                if (file.isFolder) viewModel.openPlace(file.path, origin = ORIGIN_HOME) else openFile(file, alone = true)
            },
            onFavoriteMore = { sheetFromFavorites = true; sheetTarget = it },
            onAllFavorites = { page = PAGE_FAVORITES; scope.launch { prefsRepo.refreshFavorites() } },
            onOpenLink = { link -> openChip(link, origin = ORIGIN_HOME) },
            onOpenTag = { tag ->
                viewModel.openPlace(io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.tagSearchUri(tag.name), title = tag.name, origin = ORIGIN_HOME)
            },
        )
    }


    // ---- Fylvo: selection button and its menu ----------------------------------
    var menuOpen by remember { mutableStateOf(false) }
    /** Where the selection button is (window coordinates); the menu card grows out of it. */
    var fabAnchor by remember { mutableStateOf<Rect?>(null) }
    // The card closes with the selection.
    LaunchedEffect(state.selecting) { if (!state.selecting) menuOpen = false }
    // Fylvo: when following the account's view starts or ends, the folder is listed again.
    val followsView = prefsState.usable && !prefsState.disableViewSync && prefsState.prefs?.customized?.contains("view") == true
    var followedBefore by remember { mutableStateOf(followsView) }
    LaunchedEffect(followsView) {
        if (followsView != followedBefore) {
            followedBefore = followsView
            if (!state.home) viewModel.refresh()
        }
    }
    // Inside a share: whether it is a member share (the star is offered only there).
    LaunchedEffect(state.currentUri, prefsState.usable) {
        if (prefsState.usable && CrUri.isShare(state.currentUri) && !CrUri.hasSecret(state.currentUri)) {
            CrUri.shareId(state.currentUri)?.let { prefsRepo.memberShare(it) }
        }
    }
    fun starOf(file: FileObject): io.github.svenbuild.fylvo.android.data.prefs.StarState =
        if (!prefsState.usable || state.offlineMode || (state.inTrash && page == null)) io.github.svenbuild.fylvo.android.data.prefs.StarState.NONE
        else io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.starState(file, prefsState.favoriteIds) { prefsState.memberShares[it] }
    var colorTarget by remember { mutableStateOf<List<FileObject>?>(null) }

    var tagsTarget by remember { mutableStateOf<List<FileObject>?>(null) }
    /** Fylvo: shows the server's message (or a general one) when a change failed. */
    fun customizeResult(message: String?) {
        if (message != null) scope.launch { snackbar.showSnackbar(message.ifEmpty { context.getString(R.string.customize_save_failed) }) }
    }
    fun setFavorite(files: List<FileObject>, on: Boolean) = scope.launch {
        customizeResult(
            if (on) prefsRepo.addFavorites(files.filter { starOf(it) == io.github.svenbuild.fylvo.android.data.prefs.StarState.CAN_ADD }.map { it.path })
            else prefsRepo.removeFavorites(files.map { it.id }),
        )
    }

    /**
     * Fylvo: what swiping [file]'s row does (account setting `mobile.swipe`). [limited]: Home and
     * the favourites page, where only removing the star is offered.
     */
    fun swipeFor(file: FileObject, limited: Boolean = false): RowSwipe? {
        val prefs = prefsState.prefs?.takeIf { prefsState.usable } ?: return null
        val swipe = io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.swipe(prefs.mobile)
        if (swipe.left == io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.NONE && swipe.right == io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.NONE) return null
        if (state.selecting || state.offlineMode || file.isUploading || Connected.isConnected(file.path)) return null
        if (!limited && !inSearch && page == null && (state.inTrash || state.category == Category.SHARED_WITH_ME)) return null
        val star = starOf(file)
        fun allowed(action: io.github.svenbuild.fylvo.android.data.prefs.SwipeAction) = io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.swipeAction(
            action, star,
            canShare = Permissions.canShare(file, groupShare),
            canOffline = state.offlineAllowed(file) && !state.offlineCovered(file),
            canDelete = Permissions.canModifyFile(file),
            limited = limited,
        )
        return RowSwipe(right = allowed(swipe.right), left = allowed(swipe.left), star = star) { action ->
            when (action) {
                io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.FAVORITE ->
                    setFavorite(listOf(file), star == io.github.svenbuild.fylvo.android.data.prefs.StarState.CAN_ADD)
                io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.SHARE -> shareLink(file)
                io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.OFFLINE -> viewModel.toggleOffline(file)
                // Undo exists in My files; inside a share the usual question comes first.
                io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.DELETE ->
                    if (CrUri.isShare(file.path)) deleteTarget = file else viewModel.delete(file)
                io.github.svenbuild.fylvo.android.data.prefs.SwipeAction.NONE -> Unit
            }
        }
    }

    /** The listed rows of the selection; paths not listed (a refresh in progress) have none. */
    val selectedFiles = state.files.filter { it.path in state.selected }
    val selectedOne = selectedFiles.singleOrNull()?.takeIf { state.selected.size == 1 }
    val selection = selectionActions(
        selected = selectedFiles,
        count = state.selected.size,
        ctx = SelectionContext(
            inTrash = state.inTrash,
            offlineMode = state.offlineMode,
            virtual = state.virtual,
            offlineShown = selectedOne != null && (state.offlineCovered(selectedOne) || state.offlineAllowed(selectedOne)),
            editable = selectedOne != null && state.canEdit(selectedOne),
            edited = selectedOne != null && state.editOf(selectedOne) != null,
            connected = state.inConnected,
            star = starOfSelection(selectedFiles.map(::starOf)).takeIf { selectedFiles.size == state.selected.size }
                ?: io.github.svenbuild.fylvo.android.data.prefs.StarState.NONE,
            metadata = !state.offlineMode && !state.inConnected && io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.canEditMetadata(selectedFiles) &&
                selectedFiles.size == state.selected.size,
            groupShare = groupShare,
            more = selectedOne?.let(::moreActions).orEmpty(),
            compress = selectedFiles.size > 1 && selectedFiles.size == state.selected.size &&
                Permissions.canArchiveFrom(selectedFiles, groupArchive) && Permissions.canArchiveInto(state.parent),
            pinnable = selectedOne != null && selectedOne.owned == true &&
                io.github.svenbuild.fylvo.android.data.prefs.FylvoActions.openAllowed(selectedOne.path) &&
                io.github.svenbuild.fylvo.android.widget.FolderShortcuts.canPin(context),
        ),
    )
    val listingBusy = state.loading || state.revalidating

    fun selectionEnabled(action: SelectionAction): Boolean =
        selectionActionEnabled(action, state.selected.size, busy = listingBusy, batchSaving = state.batchSaving)

    /** Runs [action] on the selected rows as they are listed right now. */
    fun onSelectionAction(action: SelectionAction) {
        val targets = state.files.filter { it.path in state.selected }
        val paths = targets.map { it.path }
        val one = targets.singleOrNull()?.takeIf { state.selected.size == 1 }
        menuOpen = false
        actionIds = Connected.idsOf(targets)
        when (action) {
            SelectionAction.SELECT_ALL -> viewModel.selectAll()
            SelectionAction.DETAILS -> one?.let { detailsTarget = it }
            SelectionAction.SHOW_IN_FOLDER -> one?.let { viewModel.navigateTo(FileObject(type = 1, path = CrUri.parent(it.path))) }
            SelectionAction.SHARE -> one?.let { shareLink(it) }
            SelectionAction.DOWNLOAD -> when {
                one != null -> viewModel.saveToDevice(context, one)
                targets.isNotEmpty() -> viewModel.saveAllToDevice(context, targets)
            }
            SelectionAction.DELETE -> when {
                one != null -> deleteTarget = one
                paths.isNotEmpty() -> deleteSelectedTarget = paths
            }
            SelectionAction.MOVE -> if (paths.isNotEmpty()) movePick = paths to false
            SelectionAction.COPY -> if (paths.isNotEmpty()) movePick = paths to true
            SelectionAction.RENAME -> one?.let { renameTarget = it }
            SelectionAction.OFFLINE -> one?.let { viewModel.toggleOffline(it) }
            SelectionAction.RESTORE -> viewModel.restore(paths)
            SelectionAction.DELETE_FOREVER -> if (paths.isNotEmpty()) deleteForeverTarget = paths
            SelectionAction.EDIT -> one?.let { editFile(it) }
            SelectionAction.FINISH_EDIT -> one?.let(state::editOf)?.let(viewModel::finishEditing)
            SelectionAction.DISCARD_EDIT -> one?.let(state::editOf)?.let { discardTarget = it }
            SelectionAction.FAVORITE -> setFavorite(targets, true)
            SelectionAction.UNFAVORITE -> setFavorite(targets, false)
            SelectionAction.COLOR -> colorTarget = targets
            SelectionAction.TAGS -> tagsTarget = targets
            SelectionAction.MANAGE_SHARES, SelectionAction.VERSIONS, SelectionAction.VIEW_ARCHIVE, SelectionAction.EXTRACT,
            SelectionAction.SHARE_PEOPLE -> one?.let { runMore(action, it) }
            SelectionAction.COMPRESS -> if (targets.isNotEmpty()) compressAsk = targets
            SelectionAction.PIN_HOME -> one?.let { folder ->
                scope.launch { io.github.svenbuild.fylvo.android.widget.FolderShortcuts.pin(context, folder, prefsRepo.accountHash()) }
            }
        }
    }

    // ---- Fylvo: the bottom tabs, from the account (mobile.tabs) ----------------------------
    val links = if (prefsState.usable) io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.links(prefsState.prefs) else emptyList()
    val tabs = if (prefsState.usable) io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.tabs(
        prefsState.prefs?.mobile,
        linkExists = { uri -> links.any { it.uri == uri } },
        // Storages are known only once their list arrived; until then a storage tab stays.
        storageExists = { id -> connectedState.overview == null || connectedState.storages.any { it.id == id } },
    ).map { tab -> if (tab is io.github.svenbuild.fylvo.android.data.prefs.Place.Link) links.firstOrNull { it.uri == tab.uri } ?: tab else tab }
    else io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.DEFAULT_TABS
    val chips = if (prefsState.usable) io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.chips(
        prefsState.prefs,
        tabs = tabs,
        hasFavorites = prefsState.favoriteIds.isNotEmpty(),
        hasTags = prefsState.tags.isNotEmpty(),
        storages = connectedState.storages.map { it.id },
    ).filter { it !is io.github.svenbuild.fylvo.android.data.prefs.Place.Storage || connectedState.storages.any { s -> s.id == it.id } } else emptyList()
    val showChips = !state.home && !inSearch && !state.selecting && state.currentUri == CrUri.MY_PREFIX
    // Fylvo 1.6: "Shared by me" next to the shares of others.
    val showSharedChips = !state.home && !inSearch && !state.selecting && state.currentUri == CrUri.SHARED_WITH_ME
    val startTab = io.github.svenbuild.fylvo.android.data.prefs.Navigation.startTab(prefsState.prefs?.startPage.takeIf { prefsState.usable }, tabs)
    /** The tab tapped last; a place that is no tab of its own keeps it lit. */
    var lastTab by rememberSaveable(stateSaver = placeSaver) { mutableStateOf<io.github.svenbuild.fylvo.android.data.prefs.Place?>(null) }
    val where = io.github.svenbuild.fylvo.android.data.prefs.Where(
        home = state.home, uri = state.currentUri, origin = state.origin, page = page, account = accountOpen, offline = offlineOpen,
    )
    /** Fylvo: which tab of the bottom bar the screen belongs to. */
    val currentTab = io.github.svenbuild.fylvo.android.data.prefs.Navigation.activeTab(where, tabs, links, lastTab)
    val atTabRoot = io.github.svenbuild.fylvo.android.data.prefs.Navigation.atTabRoot(where, currentTab, links)

    fun selectTab(tab: io.github.svenbuild.fylvo.android.data.prefs.Place) {
        // Fylvo: a tab change ends a selection, also towards Account.
        viewModel.clearSelection()
        lastTab = tab
        accountOpen = tab == io.github.svenbuild.fylvo.android.data.prefs.Place.Account || tab == io.github.svenbuild.fylvo.android.data.prefs.Place.Offline
        offlineOpen = tab == io.github.svenbuild.fylvo.android.data.prefs.Place.Offline
        customizeOpen = false
        accountPage = null
        page = null
        when (tab) {
            io.github.svenbuild.fylvo.android.data.prefs.Place.Home -> viewModel.goHome()
            io.github.svenbuild.fylvo.android.data.prefs.Place.Files -> viewModel.openMyFiles()
            io.github.svenbuild.fylvo.android.data.prefs.Place.Shared -> viewModel.openCategory(Category.SHARED_WITH_ME)
            io.github.svenbuild.fylvo.android.data.prefs.Place.Account, io.github.svenbuild.fylvo.android.data.prefs.Place.Offline -> Unit
            // A page tab starts on a clean state below it (no folder, no search to come back to).
            io.github.svenbuild.fylvo.android.data.prefs.Place.Favorites -> { if (inSearch) closeSearch(); viewModel.openMyFiles(); page = PAGE_FAVORITES; scope.launch { prefsRepo.refreshFavorites() } }
            io.github.svenbuild.fylvo.android.data.prefs.Place.Tags -> { if (inSearch) closeSearch(); viewModel.openMyFiles(); page = PAGE_TAGS; scope.launch { prefsRepo.refreshFavorites() } }
            io.github.svenbuild.fylvo.android.data.prefs.Place.Recent -> viewModel.openCategory(Category.RECENT)
            io.github.svenbuild.fylvo.android.data.prefs.Place.Trash -> viewModel.openCategory(Category.TRASH)
            is io.github.svenbuild.fylvo.android.data.prefs.Place.Category -> viewModel.openCategory(categoryOf(tab))
            is io.github.svenbuild.fylvo.android.data.prefs.Place.Link -> viewModel.openPlace(tab.uri, title = tab.name.takeIf { it.isNotEmpty() && CrUri.isSearch(tab.uri) })
            is io.github.svenbuild.fylvo.android.data.prefs.Place.Storage -> viewModel.openPlace(storageUri(tab))
        }
    }
    /** Fylvo: [goUp]; without a Home tab "up" never ends on the hidden home page, but on the start tab. */
    fun goUpOrStart() {
        goUp()
        if (viewModel.state.value.home && io.github.svenbuild.fylvo.android.data.prefs.Place.Home !in tabs) selectTab(startTab)
    }

    /** Fylvo: back at the top of a tab: to the start tab (at the start tab the app is left). */
    val backToStart = io.github.svenbuild.fylvo.android.data.prefs.Navigation.backAtTabRoot(currentTab, startTab)

    // ---- Fylvo: requests of other apps, shortcuts and the widget (IncomingQueue) ----------------
    var startApplied by rememberSaveable { mutableStateOf(false) }
    val incoming by io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.pending.collectAsState()
    val shareUris by io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.collectAsState()
    val pickToShare = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) {
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.session = app.sessionManager.sessionTag()
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.value = uris
        }
    }
    LaunchedEffect(incoming, prefsState.supported, prefsState.prefs != null, prefsState.loadFailed) {
        val request = incoming ?: return@LaunchedEffect
        // A request from before a sign-out or account change is dropped.
        val requestSession = io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.session
        if (requestSession == null || !app.sessionManager.isCurrent(requestSession)) {
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.pending.value = null
            return@LaunchedEffect
        }
        // Favourites: wait until it is known whether the server has them (cached copy or answer).
        if (request == io.github.svenbuild.fylvo.android.data.prefs.Incoming.Favorites && prefsState.supported == null && prefsState.prefs == null && !prefsState.loadFailed) return@LaunchedEffect
        io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.pending.value = null
        // A request decides where the app is; the start tab does not move it any more.
        startApplied = true
        menuOpen = false
        viewModel.clearSelection()
        if (inSearch) closeSearch()
        when (request) {
            is io.github.svenbuild.fylvo.android.data.prefs.Incoming.Share -> {
                // A new share replaces an open sheet together with its choice (nothing mixes).
                io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value = null
                io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.value = request.uris
            }
            io.github.svenbuild.fylvo.android.data.prefs.Incoming.Search -> {
                accountOpen = false
                offlineOpen = false
                customizeOpen = false
                page = null
                openSearch()
            }
            io.github.svenbuild.fylvo.android.data.prefs.Incoming.Upload -> runCatching { pickToShare.launch(arrayOf("*/*")) }
            io.github.svenbuild.fylvo.android.data.prefs.Incoming.Favorites ->
                if (!prefsState.usable) selectTab(io.github.svenbuild.fylvo.android.data.prefs.Place.Files)
                else if (io.github.svenbuild.fylvo.android.data.prefs.Place.Favorites in tabs) selectTab(io.github.svenbuild.fylvo.android.data.prefs.Place.Favorites)
                else { accountOpen = false; offlineOpen = false; customizeOpen = false; openChip(io.github.svenbuild.fylvo.android.data.prefs.Place.Favorites) }
            is io.github.svenbuild.fylvo.android.data.prefs.Incoming.Open -> {
                accountOpen = false
                offlineOpen = false
                customizeOpen = false
                page = null
                viewModel.openPlace(if (request.folder) request.uri else CrUri.parent(request.uri))
            }
            is io.github.svenbuild.fylvo.android.data.prefs.Incoming.ShareLink -> openShareLink(request.id, request.password)
            io.github.svenbuild.fylvo.android.data.prefs.Incoming.ForeignLink ->
                snackbar.showSnackbar(context.getString(R.string.link_not_own_server))
        }
    }

    // Fylvo: the account's start tab, once per app start (an opened target is not thrown away).
    LaunchedEffect(prefsState.usable, startTab) {
        if (startApplied || !prefsState.usable || prefsState.prefs == null) return@LaunchedEffect
        startApplied = true
        if (state.home && !inSearch && !accountOpen && page == null && startTab != io.github.svenbuild.fylvo.android.data.prefs.Place.Home) selectTab(startTab)
    }

    BackHandler(enabled = menuOpen || state.selecting || inSearch || !atTabRoot || backToStart != null) {
        when {
            menuOpen -> menuOpen = false
            state.selecting -> viewModel.clearSelection()
            inSearch -> closeSearch()
            atTabRoot -> backToStart?.let(::selectTab)
            else -> goUpOrStart()
        }
    }

    Scaffold(
        // Fylvo: while the selection menu is open, only it is reachable (also for TalkBack).
        modifier = if (menuOpen) Modifier.clearAndSetSemantics {} else Modifier,
        topBar = {
            val barMode = when {
                state.selecting -> "select"
                inSearch -> "search"
                state.home -> "home"
                else -> "folder"
            }
            AnimatedContent(
                targetState = barMode,
                transitionSpec = { fadeIn(tween(180)) togetherWith fadeOut(tween(120)) },
                label = "topBar",
            ) { mode -> when (mode) {
                "select" -> SelectionTopBar(
                    count = state.selected.size,
                    detail = selectionDetail(
                        selectedFiles.takeIf { it.size == state.selected.size }.orEmpty(),
                        folderLabel = stringResource(R.string.folder),
                    ),
                )
                "search" -> SearchTopBar(
                    text = state.searchQuery.orEmpty(),
                    onTextChange = { viewModel.search(it) },
                    onClose = { closeSearch() },
                    focus = searchFocus,
                )
                "home" -> Unit
                else -> FolderTopBar(
                    title = state.folderTitle(),
                    subtitle = listOfNotNull(
                        when {
                            state.inTrash -> stringResource(R.string.kept_for_7_days)
                            state.virtual -> stringResource(R.string.across_all_folders)
                            CrUri.isRoot(state.currentUri) -> null
                            else -> CrUri.displayPath(CrUri.parent(state.currentUri))
                        },
                    ).joinToString(" • ").ifEmpty { null },
                    // Fylvo: the top of a tab needs no back arrow, the bottom bar leads there.
                    showBack = !atTabRoot,
                    showSort = !state.virtual,
                    viewMode = state.effectiveMode,
                    sort = state.activeSort,
                    onBack = { goUpOrStart() },
                    onSearch = { openSearch() },
                    onToggleView = { viewModel.toggleViewMode() },
                    onSort = { viewModel.sortBy(it) },
                    onRefresh = { viewModel.refresh() },
                    onTrash = { viewModel.openCategory(Category.TRASH) },
                    // Fylvo 1.6: "Empty trash" in the trash itself, when there is something in it.
                    onEmptyTrash = if (state.inTrash && state.files.isNotEmpty() && !state.offlineMode) ({ emptyTrashAsk = true }) else null,
                    showSearch = !state.inConnected,
                    showTrash = !state.inConnected,
                    sortOptions = if (state.inConnected) listOf(SortBy.NAME, SortBy.SIZE, SortBy.UPDATED) else SortBy.entries,
                )
            } }
        },
        floatingActionButton = {
            val scrollingDown = rememberScrollingDown(state.effectiveMode == ViewMode.GRID, listState, gridState)
            // Fylvo: while selecting the button stays (also where "+" is not offered) and turns
            // into the selection button with its quick actions.
            FilesFab(
                visible = state.selecting || (state.canAddHere && !inSearch && !scrollingDown),
                count = state.selected.size,
                quick = selection.quick,
                menuOpen = menuOpen,
                enabled = ::selectionEnabled,
                onQuick = ::onSelectionAction,
                onClick = { if (state.selecting) menuOpen = true else addSheetOpen = true },
                onAnchor = { fabAnchor = it },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
        // Fylvo: the tabs stay while selecting; a tab ends the selection.
        bottomBar = { if (!inSearch) BottomTabs(tabs, currentTab, ::selectTab) },
    ) { padding ->
        // Spin the pull indicator only for a pull the user made; initial and
        // background loads have their own, quieter indicators.
        var pulled by remember { mutableStateOf(false) }
        val busy = if (state.home) state.recentLoading else state.loading || state.revalidating
        LaunchedEffect(busy) { if (!busy) pulled = false }
        val pullState = rememberPullToRefreshState()
        // Fylvo: a Box with pullToRefresh instead of PullToRefreshBox, which cannot be switched
        // off; no pull to refresh while selecting.
        Box(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .pullToRefresh(
                    isRefreshing = pulled && busy,
                    state = pullState,
                    enabled = !state.selecting,
                    onRefresh = {
                        pulled = true
                        if (state.home) viewModel.loadRecent() else viewModel.refresh()
                    },
                ),
        ) {
            // Cached folder shown while the fresh listing arrives: a thin bar, no spinner.
            if (state.revalidating && !inSearch) {
                LinearProgressIndicator(
                    Modifier
                        .fillMaxWidth()
                        .height(2.dp)
                        .zIndex(1f),
                )
            }
            // One fade per place (home, a folder, a category, search); state changes within a
            // place (loading, empty, revalidating) swap without animation so nothing flickers.
            val place = when {
                inSearch -> "search"
                state.home -> "home"
                else -> state.currentUri
            }
            AnimatedContent(
                targetState = place,
                transitionSpec = { fadeIn(tween(220)) togetherWith fadeOut(tween(160)) },
                label = "content",
            ) { shownPlace -> when {
                shownPlace != place -> Box(Modifier.fillMaxSize()) // outgoing frame: fade on empty surface
                state.home && !inSearch -> HomeScreen(
                    state = state,
                    viewModel = viewModel,
                    onSearch = { openSearch() },
                    onOpenFile = { openFile(it) },
                    onOpenRecent = { viewModel.openCategory(Category.RECENT, ORIGIN_HOME) },
                    onFileMore = { sheetTarget = it },
                    extras = homeExtras,
                    swipeFor = { swipeFor(it, limited = true) },
                )
                // Fylvo 1.6: a pasted link is opened, not searched for.
                inSearch && io.github.svenbuild.fylvo.android.util.ShareLinks.looksLikeLink(state.searchQuery.orEmpty()) -> {
                    val serverBase by produceState("") { value = app.sessionManager.serverUrlNow() }
                    val ref = io.github.svenbuild.fylvo.android.util.ShareLinks.parse(state.searchQuery.orEmpty(), serverBase)
                    Column(Modifier.fillMaxSize()) {
                        if (ref != null) ListItem(
                            modifier = Modifier.clickable(enabled = !linkBusy) { openShareLink(ref.id, ref.password) },
                            headlineContent = { Text(stringResource(R.string.open_share_link)) },
                            supportingContent = { Text(stringResource(R.string.open_share_link_hint)) },
                            leadingContent = { Icon(Icons.Outlined.Link, contentDescription = null) },
                        ) else if (serverBase.isNotEmpty()) EmptyState(
                            icon = Icons.Outlined.Link,
                            title = stringResource(R.string.link_not_own_server),
                        )
                    }
                }
                inSearch -> SearchResults(
                    fullTextAvailable = accountState.fullTextSearch,
                    tags = if (prefsState.usable) prefsState.tags.map { it.name } else emptyList(),
                    state = state,
                    viewModel = viewModel,
                    listState = listState,
                    swipeFor = { swipeFor(it) },
                    openedFrom = searchOpenedFrom,
                    actionsFor = { file ->
                        FileActions(
                            onOpen = {
                                if (file.isFolder) {
                                    closeSearch()
                                    viewModel.navigateTo(file)
                                } else {
                                    openFile(file)
                                }
                            },
                            // Fylvo: no selection in search; a long press opens the file's sheet.
                            onLongPress = { sheetTarget = file },
                            longPressLabel = optionsLabel,
                            longPressOnRow = true,
                        )
                    },
                )
                state.loading && state.files.isEmpty() -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                state.error != null -> {
                    EmptyState(
                    icon = Icons.Outlined.FolderOpen,
                    title = stringResource(R.string.load_folder_failed),
                    detail = state.error?.asString(),
                    // Fylvo: a connected storage that must be connected again: that is done in the web app.
                    action = if (state.inConnected && state.errorCode == Connected.CODE_RECONNECT) {
                        stringResource(R.string.connected_open_web_app) to {
                            scope.launch { openInBrowser(context, viewModel.connected.webSettingsUrl(Connected.storageId(state.currentUri))) }
                            Unit
                        }
                    } else {
                        stringResource(R.string.retry) to { viewModel.refresh() }
                    },
                )
                }
                state.files.isEmpty() && state.uploadsHere.isEmpty() -> when (state.category) {
                    null -> Column(Modifier.fillMaxSize()) {
                        if (showChips) PlaceChips(chips, ::openChip)
                        // Fylvo: a connected folder whose entries were all left out says so.
                        if (state.inConnected) ConnectedBanner(state.connected?.omitted ?: 0)
                        EmptyState(
                            icon = Icons.Outlined.FolderOpen,
                            title = stringResource(R.string.folder_empty),
                            detail = when {
                                state.canAddHere -> stringResource(R.string.folder_empty_hint)
                                // Fylvo 1.6: an upload link with a password: the app cannot upload there.
                                CrUri.hasSecret(state.currentUri) && Permissions.enabled(state.parent?.capability, Permissions.UPLOAD_FILE) ->
                                    stringResource(R.string.link_upload_password)
                                else -> null
                            },
                        )
                    }
                    Category.TRASH -> EmptyState(icon = Icons.Outlined.Delete, title = stringResource(R.string.trash_empty), detail = stringResource(R.string.trash_empty_hint))
                    Category.RECENT -> EmptyState(icon = Icons.Outlined.History, title = stringResource(R.string.nothing_recent), detail = stringResource(R.string.recent_hint))
                    Category.IMAGES -> EmptyState(icon = Icons.Outlined.Image, title = stringResource(R.string.no_images))
                    Category.VIDEOS -> EmptyState(icon = Icons.Outlined.Videocam, title = stringResource(R.string.no_videos))
                    Category.AUDIO -> EmptyState(icon = Icons.Outlined.MusicNote, title = stringResource(R.string.no_audio))
                    Category.DOCUMENTS -> EmptyState(icon = Icons.Outlined.Description, title = stringResource(R.string.no_documents))
                    Category.SHARED_WITH_ME -> Column(Modifier.fillMaxSize()) {
                        if (showSharedChips) SharedChips { page = PAGE_SHARES }
                        EmptyState(icon = Icons.Outlined.FolderOpen, title = stringResource(R.string.nothing_shared))
                    }
                }
                else -> Column(Modifier.fillMaxSize()) {
                    // Fylvo: the places of the sidebar above My files.
                    if (showChips) PlaceChips(chips, ::openChip)
                    if (showSharedChips) SharedChips { page = PAGE_SHARES }
                    if (state.offlineMode) OfflineBanner()
                    if (state.inConnected) ConnectedBanner(state.connected?.omitted ?: 0)
                    Box(Modifier.weight(1f)) {
                        if (state.effectiveMode == ViewMode.GRID) FileGrid(state, viewModel, gridState, ::actionsFor)
                        else FileList(state, viewModel, listState, ::actionsFor, swipeFor = { swipeFor(it) })
                    }
                }
            } }
            PullToRefreshDefaults.Indicator(
                state = pullState,
                isRefreshing = pulled && busy,
                modifier = Modifier.align(Alignment.TopCenter),
            )
        }
    }

    SelectionMenu(
        open = menuOpen,
        anchor = fabAnchor,
        items = selection.menu,
        offline = selectedOne?.let { OfflineSwitch(on = state.offlineOn(it), covered = state.offlineCovered(it), folder = it.isFolder) },
        enabled = ::selectionEnabled,
        onAction = ::onSelectionAction,
        onClose = { menuOpen = false },
    )

    // Fylvo: the one-time hint about long press and drag, on a quiet folder screen with files.
    val hintReady = !state.home && !inSearch && !state.inTrash && !state.offlineMode && !state.loading &&
        state.error == null && state.files.isNotEmpty() && !state.selecting && !menuOpen && state.snackbar == null &&
        !accountOpen && !offlineOpen && !customizeOpen && preview == null && movePick == null && sheetTarget == null && !addSheetOpen &&
        !mkdirOpen && renameTarget == null && deleteTarget == null && deleteSelectedTarget == null &&
        deleteForeverTarget == null && shareTarget == null && detailsTarget == null && pendingUpload == null &&
        state.offlinePrompt == null && discardTarget == null && state.editPrompt == null && state.rootPrompt == null
    LaunchedEffect(hintReady) { if (hintReady) viewModel.showSelectionHint() }

    pendingUpload?.let { pending ->
        UploadConflictDialog(
            pending = pending,
            onResolve = { r ->
                pendingUpload = null
                scope.launch(Dispatchers.IO) { UploadWorker.enqueueSafely(context, r.uris, pending.parentUri, r.names, r.overwrite, r.overwriteOnly) }
            },
            onDismiss = { pendingUpload = null },
        )
    }

    var lastPreview by remember { mutableStateOf(preview) }
    if (preview != null) lastPreview = preview
    AnimatedVisibility(
        visible = preview != null,
        enter = fadeIn(tween(220)) + scaleIn(tween(260), initialScale = 0.94f),
        exit = fadeOut(tween(180)) + scaleOut(tween(200), targetScale = 0.96f),
    ) {
      lastPreview?.let { (files, index) ->
        PreviewScreen(
            files = files,
            startIndex = index,
            viewModel = viewModel,
            onClose = { preview = null },
            onShare = { shareLink(it) },
            onDownload = { f -> viewModel.saveToDevice(context, f) },
            onOpenWith = { openWithSystemApp(it) },
            onDelete = { preview = null; deleteTarget = it },
            canShare = { Permissions.canShare(it, groupShare) },
            canDelete = Permissions::canModifyFile,
            canEdit = { f ->
                Permissions.canSaveContent(f) && io.github.svenbuild.fylvo.android.ui.editor.editableAsText(f) &&
                    viewModel.state.value.editOf(f) == null && !state.offlineMode
            },
            onEdit = { f -> editor.open(io.github.svenbuild.fylvo.android.ui.editor.EditorTarget.Existing(f)) },
        )
      }
    }

    // Fylvo 1.6: the text editor over everything (also over the preview it was opened from).
    if (editorState.target != null) {
        io.github.svenbuild.fylvo.android.ui.editor.TextEditorScreen(editor) { wrote ->
            editor.close()
            if (wrote) {
                preview = null
                viewModel.refresh()
            }
        }
    }

    if (addSheetOpen) {
        ModalBottomSheet(onDismissRequest = { addSheetOpen = false }) {
            // Fylvo: in a connected storage each entry follows the folder's own bit.
            if (!state.inConnected || Connected.canUploadTo(state.parent)) ListItem(
                modifier = Modifier.clickable {
                    addSheetOpen = false
                    if (!CrUri.hasSecret(state.currentUri)) {
                        uploadTarget = state.currentUri
                        uploadLauncher.launch(arrayOf("*/*"))
                    }
                },
                headlineContent = { Text(stringResource(R.string.upload_files)) },
                supportingContent = { Text(stringResource(R.string.into_folder, state.folderTitle())) },
                leadingContent = { Icon(Icons.Default.UploadFile, contentDescription = null) },
            )
            // Fylvo 1.6: a whole folder of the phone (not into connected storages, like the web).
            if (!state.inConnected && !state.offlineMode && Permissions.canAddTo(state.parent)) ListItem(
                modifier = Modifier.clickable {
                    addSheetOpen = false
                    folderUploadTarget = state.currentUri
                    runCatching { folderLauncher.launch(null) }
                },
                headlineContent = { Text(stringResource(R.string.folder_upload)) },
                supportingContent = { Text(stringResource(R.string.into_folder, state.folderTitle())) },
                leadingContent = { Icon(androidx.compose.material.icons.Icons.Default.DriveFolderUpload, contentDescription = null) },
            )
            if (!state.inConnected || Connected.canCreateIn(state.parent)) ListItem(
                modifier = Modifier.clickable {
                    addSheetOpen = false
                    mkdirOpen = true
                },
                headlineContent = { Text(stringResource(R.string.new_folder)) },
                leadingContent = { Icon(Icons.Default.CreateNewFolder, contentDescription = null) },
            )
            // Fylvo 1.6: new text files (the web's templates with content: text and Markdown).
            if (Permissions.canCreateTextIn(state.parent) && !state.offlineMode) {
                ListItem(
                    modifier = Modifier.clickable { addSheetOpen = false; newTextExt = "txt" },
                    headlineContent = { Text(stringResource(R.string.new_text_file)) },
                    leadingContent = { Icon(Icons.Outlined.Description, contentDescription = null) },
                )
                ListItem(
                    modifier = Modifier.clickable { addSheetOpen = false; newTextExt = "md" },
                    headlineContent = { Text(stringResource(R.string.new_markdown_note)) },
                    leadingContent = { Icon(androidx.compose.material.icons.Icons.AutoMirrored.Outlined.Article, contentDescription = null) },
                )
            }
            Box(Modifier.height(24.dp))
        }
    }

    detailsTarget?.let { file ->
        DetailsSheet(
            file = file,
            thumbUrl = rememberThumbUrl(viewModel, file),
            onDismiss = { detailsTarget = null },
            mapEnabled = accountState.mapEnabled,
            onShares = if (Permissions.canShare(file, groupShare)) ({ detailsTarget = null; sharesOfTarget = file }) else null,
        )
    }

    shareTarget?.let { file ->
        val thumbUrl = rememberThumbUrl(viewModel, file)
        ShareLinkSheet(
            name = file.name,
            isFolder = file.isFolder,
            thumb = { FileThumb(file, thumbUrl, size = 48.dp) },
            existing = null,
            save = { options -> app.shares.create(file, options).also { viewModel.refresh() } },
            onDismiss = { shareTarget = null },
            onMessage = { msg -> scope.launch { snackbar.showSnackbar(msg) } },
        )
    }

    // Fylvo 1.6: what the share lists may do (shared by "Shared by me" and a file's own list).
    val shareActions = ShareActions(
        openFolder = { uri ->
            page = null
            sharesOfTarget = null
            accountOpen = false
            offlineOpen = false
            customizeOpen = false
            viewModel.openPlace(uri)
        },
        editPeople = { share, uri -> sharesOfTarget = null; peopleTarget = PeopleTarget(uri, share) },
        message = { msg -> scope.launch { snackbar.showSnackbar(msg) } },
        canEdit = groupShare == true,
    )
    linkPassword?.let { ask ->
        LinkPasswordDialog(
            ask = ask,
            busy = linkBusy,
            onOpen = { pw -> openShareLink(ask.id, pw) },
            onDismiss = { linkPassword = null },
        )
    }
    // Fylvo 1.6: archives.
    fun startedTask(started: Boolean, e: Throwable?) {
        scope.launch {
            if (started) {
                val r = snackbar.showSnackbar(context.getString(R.string.task_started), actionLabel = context.getString(R.string.tasks))
                if (r == androidx.compose.material3.SnackbarResult.ActionPerformed) { accountOpen = true; accountPage = ACCOUNT_TASKS }
            } else if (e != null) snackbar.showSnackbar(io.github.svenbuild.fylvo.android.util.userError(e).resolve(context))
        }
    }
    archiveView?.let { file ->
        ArchivePage(
            file = file,
            canExtract = io.github.svenbuild.fylvo.android.data.ArchiveRepository.extractable(file.name),
            onExtract = { archiveView = null; extractPick = file },
            onClose = { archiveView = null },
        )
    }
    extractPick?.let { file ->
        // The archive's own folder when it may receive files, else My files.
        val start = CrUri.parent(file.path).takeIf { Permissions.canArchiveInto(state.parent) && it == state.currentUri } ?: CrUri.MY_PREFIX
        FolderPicker(
            title = stringResource(R.string.archive_extract_to),
            confirmLabel = R.string.archive_extract_here,
            startUri = start,
            excluded = emptySet(),
            loadFolders = { viewModel.listFolders(it) },
            onCreateFolder = { parent, name -> viewModel.createFolder(parent, name) },
            canPick = { Permissions.canArchiveInto(viewModel.pickerFolder(it)) },
            canCreateIn = { Permissions.canArchiveInto(viewModel.pickerFolder(it)) },
            onPick = { dst ->
                extractPick = null
                scope.launch {
                    runCatching { app.archives.extract(file.path, dst) }
                        .onSuccess { startedTask(true, null) }
                        .onFailure { startedTask(false, it) }
                }
            },
            onDismiss = { extractPick = null },
        )
    }
    compressAsk?.let { files ->
        val fallback = stringResource(R.string.archive_default_name)
        NameDialog(
            title = stringResource(R.string.archive_compress),
            label = stringResource(R.string.name),
            confirm = stringResource(R.string.archive_compress_go),
            initial = io.github.svenbuild.fylvo.android.data.ArchiveRepository.zipName(files.map { it.name }, fallback),
            onConfirm = { name ->
                compressAsk = null
                val zip = name.trim().let { if (it.lowercase().endsWith(".zip")) it else "$it.zip" }
                viewModel.clearSelection()
                scope.launch {
                    runCatching { app.archives.compress(files.map { it.path }, CrUri.join(state.currentUri, zip)) }
                        .onSuccess { startedTask(true, null) }
                        .onFailure { startedTask(false, it) }
                }
            },
            onDismiss = { compressAsk = null },
        )
    }

    versionsTarget?.let { file ->
        VersionsSheet(
            file = file,
            onDownload = { f, entity -> viewModel.saveToDevice(context, f, entity) },
            onChanged = { viewModel.refresh() },
            onMessage = { msg -> scope.launch { snackbar.showSnackbar(msg) } },
            onDismiss = { versionsTarget = null },
        )
    }
    if (emptyTrashAsk) {
        var emptying by remember { mutableStateOf(false) }
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { if (!emptying) emptyTrashAsk = false },
            title = { Text(stringResource(R.string.empty_trash_title)) },
            text = { Text(stringResource(R.string.empty_trash_text)) },
            confirmButton = {
                androidx.compose.material3.TextButton(enabled = !emptying, onClick = {
                    emptying = true
                    scope.launch {
                        runCatching { app.history.emptyTrash() }
                            .onSuccess { snackbar.showSnackbar(context.getString(R.string.trash_emptied)) }
                            .onFailure { snackbar.showSnackbar(io.github.svenbuild.fylvo.android.util.userError(it).resolve(context)) }
                        emptying = false
                        emptyTrashAsk = false
                        viewModel.refresh()
                    }
                }) {
                    if (emptying) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    else Text(stringResource(R.string.empty_trash), color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = { androidx.compose.material3.TextButton(enabled = !emptying, onClick = { emptyTrashAsk = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
    folderUploadAsk?.let { (scan, target, targetId) ->
        val many = scan.files.size >= io.github.svenbuild.fylvo.android.work.FolderUpload.MANY
        androidx.compose.material3.AlertDialog(
            onDismissRequest = {
                folderUploadAsk = null
                io.github.svenbuild.fylvo.android.work.FolderUpload.release(context, scan.tree)
            },
            title = { Text(stringResource(R.string.folder_upload_title, scan.name)) },
            text = {
                Text(
                    pluralStringResource(R.plurals.folder_upload_text, scan.files.size, scan.files.size, formatSize(scan.bytes), CrUri.displayName(target)) +
                        if (many) "\n\n" + stringResource(R.string.folder_upload_many) else "",
                )
            },
            confirmButton = {
                androidx.compose.material3.TextButton(onClick = {
                    folderUploadAsk = null
                    val tag = app.sessionManager.sessionTag()
                    scope.launch {
                        val ok = withContext(Dispatchers.IO) {
                            runCatching {
                                // Still the folder that was chosen (nothing lands in one that took its place).
                                if (targetId != null && app.fileRepository.folderIdMatches(target, targetId) != true) return@runCatching false
                                if (!app.sessionManager.isCurrent(tag)) return@runCatching false
                                // An existing folder of that name is filled up; files with a taken name get a new one.
                                val root = CrUri.join(target, scan.name)
                                app.fileRepository.mkdirs(root)
                                scan.dirs.forEach { app.fileRepository.mkdirs(CrUri.join(root, it)) }
                                io.github.svenbuild.fylvo.android.work.FolderUpload.keep(context, scan.tree)
                                io.github.svenbuild.fylvo.android.work.FolderUpload.groups(scan).forEach { (dir, items) ->
                                    val parent = if (dir.isEmpty()) root else CrUri.join(root, dir)
                                    val existing = viewModel.namesInFolder(parent)
                                    val names = UploadWorker.uniqueWithinSelection(items.map { it.name }, existing)
                                    UploadWorker.enqueueSafely(context, items.map { it.uri }, parent, names)
                                }
                                true
                            }.getOrDefault(false)
                        }
                        io.github.svenbuild.fylvo.android.work.FolderUpload.releaseFinished(context)
                        snackbar.showSnackbar(context.getString(if (ok) R.string.folder_upload_started else R.string.folder_upload_failed))
                        if (ok) viewModel.refresh()
                    }
                }) { Text(stringResource(R.string.upload)) }
            },
            dismissButton = {
                androidx.compose.material3.TextButton(onClick = {
                    folderUploadAsk = null
                    io.github.svenbuild.fylvo.android.work.FolderUpload.release(context, scan.tree)
                }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }
    if (folderReading) {
        androidx.compose.material3.AlertDialog(
            onDismissRequest = {},
            confirmButton = {},
            text = { androidx.compose.foundation.layout.Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                Text(stringResource(R.string.folder_upload_reading), modifier = Modifier.padding(start = 16.dp))
            } },
        )
    }
    peopleTarget?.let { t ->
        PeopleSheet(
            target = t,
            onDone = { msg -> peopleTarget = null; viewModel.refresh(); msg?.let { scope.launch { snackbar.showSnackbar(it) } } },
            onDismiss = { peopleTarget = null },
        )
    }
    sharesOfTarget?.let { file ->
        FileSharesSheet(file, shareActions, onDismiss = { sharesOfTarget = null; viewModel.refresh() })
    }

    sheetTarget?.takeIf { Connected.isConnected(it.path) }?.let { file ->
        ConnectedFileSheet(
            file = file,
            onDismiss = { sheetTarget = null },
            onEnter = { sheetTarget = null; viewModel.navigateTo(file) },
            onOpen = { sheetTarget = null; openFile(file) },
            onOpenExternal = { url -> sheetTarget = null; openInBrowser(context, url) },
            onDownload = { sheetTarget = null; viewModel.saveToDevice(context, file) },
            onDetails = { sheetTarget = null; detailsTarget = file },
            onRename = { sheetTarget = null; renameTarget = file },
            onMove = { sheetTarget = null; actionIds = Connected.idsOf(listOf(file)); movePick = listOf(file.path) to false },
            onDelete = { sheetTarget = null; deleteTarget = file },
        )
    }

    sheetTarget?.takeIf { !Connected.isConnected(it.path) }?.let { file ->
        val limited = sheetFromFavorites
        FileActionSheet(
            file = file,
            thumbUrl = rememberThumbUrl(viewModel, file),
            offlineOn = state.offlineOn(file),
            offlineCovered = state.offlineCovered(file),
            offlineAllowed = state.offlineAllowed(file) && !limited,
            onDismiss = { sheetTarget = null; sheetFromFavorites = false },
            onOpen = {
                sheetTarget = null
                sheetFromFavorites = false
                // From a page: the page closes, so the opened place is seen.
                val from = page
                page = null
                if (file.isFolder) viewModel.openPlace(file.path, origin = from?.let { PAGE_ORIGIN + it } ?: ORIGIN_HOME.takeIf { limited })
                else openFile(file, alone = true)
            },
            onShare = { sheetTarget = null; shareLink(file) },
            onDownload = { viewModel.saveToDevice(context, file) },
            onToggleOffline = { sheetTarget = null; viewModel.toggleOffline(file) },
            onRename = { sheetTarget = null; renameTarget = file },
            onDelete = { sheetTarget = null; deleteTarget = file },
            onMove = { sheetTarget = null; movePick = listOf(file.path) to false },
            onCopy = { sheetTarget = null; movePick = listOf(file.path) to true },
            onDetails = { sheetTarget = null; detailsTarget = file },
            canModify = Permissions.canModifyFile(file) && !limited,
            canShare = Permissions.canShare(file, groupShare) && !limited,
            extras = if (limited) emptyList() else moreActions(file),
            onExtra = { action -> sheetTarget = null; runMore(action, file) },
            // Fylvo: a sheet from a page (favourites) belongs to that page, never to the trash below it.
            inTrash = state.inTrash && page == null,
            onRestore = { sheetTarget = null; viewModel.restore(listOf(file.path)) },
            onDeleteForever = { sheetTarget = null; deleteForeverTarget = listOf(file.path) },
            canEdit = state.canEdit(file) && !limited,
            canDownload = !limited,
            edited = state.editOf(file) != null,
            onEdit = { sheetTarget = null; editFile(file) },
            onFinishEdit = { sheetTarget = null; state.editOf(file)?.let(viewModel::finishEditing) },
            onDiscardEdit = { sheetTarget = null; discardTarget = state.editOf(file) },
            star = starOf(file),
            onStar = { on -> sheetTarget = null; setFavorite(listOf(file), on) },
            canEditMetadata = !limited && !state.offlineMode && io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.canEditMetadata(listOf(file)),
            onColor = { sheetTarget = null; colorTarget = listOf(file) },
            onTags = { sheetTarget = null; tagsTarget = listOf(file) },
            onShowInFolder = if (limited || (!state.inTrash && (state.home || state.virtual || inSearch))) ({
                sheetTarget = null
                sheetFromFavorites = false
                page = null
                if (inSearch) closeSearch()
                viewModel.navigateTo(FileObject(type = 1, path = CrUri.parent(file.path)))
            }) else null,
        )
    }

    var lastPick by remember { mutableStateOf(movePick) }
    if (movePick != null) lastPick = movePick
    AnimatedVisibility(
        visible = movePick != null,
        enter = slideInVertically(tween(260)) { it / 4 } + fadeIn(tween(200)),
        exit = slideOutVertically(tween(200)) { it / 4 } + fadeOut(tween(160)),
    ) {
      lastPick?.let { (paths, copy) ->
        FolderPicker(
            title = stringResource(if (copy) R.string.copy_to else R.string.move_to),
            confirmLabel = if (copy) R.string.copy_to_folder else R.string.move_to_folder,
            // The picked files' own folder: inside a share the picker then stays in that share.
            startUri = CrUri.parent(paths.first()),
            excluded = if (copy) emptySet() else paths.toSet(),
            loadFolders = { viewModel.listFolders(it) },
            onCreateFolder = { parent, name -> viewModel.createFolder(parent, name) },
            // Fylvo: in a connected storage only into folders that allow it.
            canPick = { viewModel.pickerAllowsMoveInto(it) },
            canCreateIn = { viewModel.pickerAllowsMoveInto(it) },
            onPick = { dst ->
                movePick = null
                viewModel.moveTo(paths, dst, copy, ids = actionIds.takeIf { Connected.isConnected(dst) }, dstId = viewModel.pickerFolderId(dst))
            },
            onDismiss = { movePick = null },
        )
      }
    }

    newTextExt?.let { ext ->
        val initial = stringResource(if (ext == "md") R.string.new_note_name else R.string.new_text_name) + "." + ext
        NameDialog(
            title = stringResource(if (ext == "md") R.string.new_markdown_note else R.string.new_text_file),
            label = stringResource(R.string.name),
            confirm = stringResource(R.string.create),
            initial = initial,
            selectEnd = initial.length - ext.length - 1,
            onConfirm = { name ->
                newTextExt = null
                editor.open(io.github.svenbuild.fylvo.android.ui.editor.EditorTarget.New(state.currentUri, name.trim()))
            },
            onDismiss = { newTextExt = null },
        )
    }

    if (mkdirOpen) {
        NameDialog(
            title = stringResource(R.string.new_folder),
            label = stringResource(R.string.folder_name),
            confirm = stringResource(R.string.create),
            onConfirm = {
                viewModel.mkdir(it)
                mkdirOpen = false
            },
            onDismiss = { mkdirOpen = false },
        )
    }

    renameTarget?.let { file ->
        NameDialog(
            title = stringResource(R.string.rename),
            label = stringResource(R.string.new_name),
            initial = file.name,
            // Select the stem so typing replaces the name and keeps the extension.
            selectEnd = if (file.isFolder) file.name.length else file.name.lastIndexOf('.').takeIf { it > 0 } ?: file.name.length,
            confirm = stringResource(R.string.rename),
            onConfirm = {
                viewModel.rename(file, it)
                renameTarget = null
            },
            onDismiss = { renameTarget = null },
        )
    }

    deleteTarget?.let { file ->
        ConfirmDeleteDialog(
            title = stringResource(R.string.delete_file_title, file.name),
            note = connectedDeleteNote(file.path),
            onConfirm = {
                viewModel.delete(file)
                deleteTarget = null
            },
            onDismiss = { deleteTarget = null },
        )
    }

    deleteForeverTarget?.let { paths ->
        val n = paths.size
        AlertDialog(
            onDismissRequest = { deleteForeverTarget = null },
            title = { Text(pluralStringResource(R.plurals.delete_forever_title, n, n)) },
            text = { Text(stringResource(R.string.cannot_be_undone)) },
            confirmButton = {
                TextButton(onClick = { viewModel.deleteForever(paths); deleteForeverTarget = null }) {
                    Text(stringResource(R.string.delete_forever), color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = { TextButton(onClick = { deleteForeverTarget = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }

    deleteSelectedTarget?.let { paths ->
        val n = paths.size
        ConfirmDeleteDialog(
            title = pluralStringResource(R.plurals.delete_items_title, n, n),
            note = connectedDeleteNote(paths.first()),
            onConfirm = {
                viewModel.deletePaths(paths, actionIds.takeIf { Connected.isConnected(paths.first()) })
                deleteSelectedTarget = null
            },
            onDismiss = { deleteSelectedTarget = null },
        )
    }

    discardTarget?.let { entry ->
        DiscardEditDialog(
            entry = entry,
            onConfirm = { viewModel.discardEditing(entry.id, entry.name); discardTarget = null },
            onDismiss = { discardTarget = null },
        )
    }

    state.editPrompt?.let { prompt ->
        NotUploadedDialog(
            prompt = prompt,
            onRetryLater = viewModel::retryEditLater,
            onDiscard = { viewModel.discardEditing(prompt.entryId, prompt.name) },
            onDismiss = viewModel::dismissEditPrompt,
        )
    }

    state.rootPrompt?.let { prompt ->
        val n = prompt.entries.size
        EditChoiceDialog(
            title = pluralStringResource(R.plurals.edit_unuploaded_title, n, n),
            text = stringResource(R.string.edit_root_text, prompt.name),
            busy = prompt.busy,
            onUpload = { viewModel.resolveRootPrompt(upload = true) },
            onDiscard = { viewModel.resolveRootPrompt(upload = false) },
            onDismiss = viewModel::dismissRootPrompt,
        )
    }

    if (accountOpen) {
        AccountScreen(
            offlineCount = state.offlineRoots.size,
            onOpenOffline = { offlineOpen = true },
            onOpenShares = { page = PAGE_SHARES },
            onOpenProfile = { accountPage = ACCOUNT_PROFILE },
            onOpenSecurity = { accountPage = ACCOUNT_SECURITY },
            onOpenTasks = { accountPage = ACCOUNT_TASKS },
            onOpenConnected = if (connectedState.overview?.let { it.available && it.allowed } == true) ({ accountPage = ACCOUNT_CONNECTED }) else null,
            onOpenCustomize = { customizeOpen = true },
            // Account is a tab: back goes to the start tab like every other tab.
            onBack = { selectTab(startTab) },
            bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) },
        )
    }

    // Fylvo: the favourites and tags pages.
    val pageActions: (FileObject) -> FileActions = { file ->
        FileActions(
            onOpen = {
                val from = page
                page = null
                if (file.isFolder) viewModel.openPlace(file.path, origin = PAGE_ORIGIN + from)
                else openFile(file, alone = true)
            },
            onLongPress = { sheetFromFavorites = true; sheetTarget = file },
            longPressLabel = optionsLabel,
            longPressOnRow = true,
        )
    }
    /** As a tab: back goes to the start tab (none at the start tab); from a chip: back to the files. */
    val pageBack: (() -> Unit)? = if (atTabRoot) backToStart?.let { start -> { selectTab(start) } } else ({ page = null })
    if (page == PAGE_FAVORITES) {
        FavoritesPage(
            favorites = prefsState.favorites,
            viewModel = viewModel,
            onBack = pageBack,
            actionsFor = pageActions,
            bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) },
            swipeFor = { swipeFor(it, limited = true) },
            loadFailed = prefsState.loadFailed || prefsState.favoritesFailed,
            onRetry = { scope.launch { prefsRepo.refreshFavorites(); prefsRepo.refresh() } },
        )
    }
    // Fylvo 1.6: profile and security, over Account.
    if (accountOpen && accountPage == ACCOUNT_PROFILE) {
        io.github.svenbuild.fylvo.android.ui.settings.ProfilePage(onBack = { accountPage = null }, bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) })
    }
    if (accountOpen && accountPage == ACCOUNT_CONNECTED) {
        ConnectedPage(
            viewModel = viewModel,
            onBack = { accountPage = null },
            bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) },
            onOpen = { s -> accountPage = null; accountOpen = false; viewModel.openPlace(s.uri) },
            onMessage = { msg -> scope.launch { snackbar.showSnackbar(msg) } },
        )
    }
    // Fylvo 1.6: the question before a new storage is saved, and messages of the sign-in.
    val connectUi by app.connect.ui.collectAsState()
    val connectMessage by app.connect.message.collectAsState()
    (connectUi as? ConnectController.Ui.Confirm)?.let { c ->
        ConnectConfirmDialog(
            preview = c.preview,
            myName = accountState.me?.let { it.nickname.ifEmpty { it.email } }.orEmpty(),
            onAnswer = { yes -> scope.launch { app.connect.answer(yes) } },
        )
    }
    LaunchedEffect(connectMessage) {
        connectMessage?.let { snackbar.showSnackbar(it.resolve(context)); app.connect.messageShown() }
    }
    LaunchedEffect(Unit) {
        app.reloadConnected = { viewModel.connected.reload() }
        // A sign-in that was said yes to, but whose answer was lost (the app was stopped): sent again.
        runCatching { app.connect.resume() }
    }
    if (accountOpen && accountPage == ACCOUNT_TASKS) {
        TasksPage(onBack = { accountPage = null }, bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) }, onOpenFolder = { uri ->
            accountPage = null
            accountOpen = false
            viewModel.openPlace(uri)
        })
    }
    if (accountOpen && accountPage == ACCOUNT_SECURITY) {
        io.github.svenbuild.fylvo.android.ui.settings.SecurityPage(onBack = { accountPage = null }, bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) })
    }
    if (page == PAGE_SHARES) {
        SharesPage(onBack = { page = null }, bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) }, actions = shareActions)
    }
    if (page == PAGE_TAGS) {
        TagsPage(
            tags = prefsState.tags,
            onBack = pageBack,
            onOpen = { tag ->
                page = null
                viewModel.openPlace(io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.tagSearchUri(tag.name), title = tag.name, origin = PAGE_ORIGIN + PAGE_TAGS)
            },
            bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) },
        )
    }

    AnimatedVisibility(
        visible = customizeOpen,
        enter = slideInHorizontally(tween(260)) { it / 3 } + fadeIn(tween(200)),
        exit = slideOutHorizontally(tween(200)) { it / 3 } + fadeOut(tween(160)),
    ) {
        io.github.svenbuild.fylvo.android.ui.settings.CustomizeScreen(
            onBack = { customizeOpen = false },
            storages = connectedState.storages.map { it.id }.takeIf { connectedState.overview != null },
            bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) },
        )
    }

    AnimatedVisibility(
        visible = offlineOpen,
        enter = slideInHorizontally(tween(260)) { it / 3 } + fadeIn(tween(200)),
        exit = slideOutHorizontally(tween(200)) { it / 3 } + fadeOut(tween(160)),
    ) {
        OfflineScreen(
            roots = state.offlineRoots,
            syncing = state.offlineSyncing,
            // As a tab of its own: back goes to the start tab; from Account: back to Account.
            onBack = { if (lastTab == io.github.svenbuild.fylvo.android.data.prefs.Place.Offline && backToStart != null) selectTab(backToStart) else offlineOpen = false },
            onOpen = { root ->
                if (root.folder) {
                    // Browsable without a connection too (the Files tab falls back to the copies).
                    offlineOpen = false
                    accountOpen = false
                    viewModel.navigateTo(FileObject(type = 1, name = root.name, path = root.uri))
                } else {
                    scope.launch {
                        val local = viewModel.offlineFile(root.uri)
                        if (local != null) openWithSystem(context, local)
                        else snackbar.showSnackbar(context.getString(R.string.offline_not_here_yet))
                    }
                }
            },
            onSyncNow = { viewModel.syncOfflineNow() },
            onRemove = { root -> viewModel.removeOffline(root.id, root.name) },
            bottomBar = { BottomTabs(tabs, currentTab, ::selectTab) },
            edits = state.editEntries.filter(::needsAttention),
            onEditOpen = { entry -> scope.launch { viewModel.editedFile(entry)?.let { openWithSystem(context, it) } } },
            onEditFinish = viewModel::finishEditing,
            onEditDiscard = { discardTarget = it },
            onEditDismissNotice = viewModel::dismissEditNotice,
        )
    }

    // Fylvo 1.6: messages also show over the pages above the list (Account pages, shares, ...).
    if (accountOpen || page != null || customizeOpen || offlineOpen) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.BottomCenter) {
            SnackbarHost(snackbar, Modifier.padding(bottom = 104.dp))
        }
    }

    // Fylvo: "Save in Fylvo" for shared files: the folder is chosen first.
    shareUris?.takeIf { io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.session?.let(app.sessionManager::isCurrent) == true }?.let { uris ->
        val shareStore = remember { io.github.svenbuild.fylvo.android.data.prefs.ShareTargetStore(context.applicationContext) }
        // First: whether the folder used last was read yet; second: that folder.
        val lastRead by produceState<Pair<Boolean, ShareTarget?>>(initialValue = false to null) {
            value = true to shareStore.get()?.let { (uri, id) -> ShareTarget(uri, id) }
        }
        val last = lastRead.second
        // The chosen folder lives with the shared files (it survives a rebuilt screen).
        val chosenPair by io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.collectAsState()
        val chosen = chosenPair?.let { (uri, id) -> ShareTarget(uri, id) }
        fun choose(target: ShareTarget?) { io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value = target?.let { it.uri to it.id } }
        var shareBusy by remember(uris) { mutableStateOf(false) }
        var shareNotice by remember(uris) { mutableStateOf<String?>(null) }
        var pickingShareFolder by remember(uris) { mutableStateOf(false) }
        /** Counts the folder choices; a late answer for an older one is dropped. */
        var pickSeq by remember(uris) { mutableStateOf(0) }
        val favoriteFolders = prefsState.favorites?.files.orEmpty()
            .filter { it.isFolder && io.github.svenbuild.fylvo.android.data.prefs.FylvoActions.openAllowed(it.path) && it.path != last?.uri }
            .map { ShareTarget(it.path, it.id, io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.folderColor(it)) }
        // Preselected: the folder used last, else My files.
        LaunchedEffect(lastRead.first, uris) { if (lastRead.first && chosen == null) choose(last ?: ShareTarget(CrUri.MY_PREFIX, null)) }
        fun closeShare() {
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.value = null
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value = null
        }
        if (pickingShareFolder) {
            FolderPicker(
                title = stringResource(R.string.share_into_fylvo),
                confirmLabel = R.string.share_save_here,
                startUri = CrUri.MY_PREFIX,
                excluded = emptySet(),
                loadFolders = { viewModel.listFolders(it) },
                onCreateFolder = { parent, name -> viewModel.createFolder(parent, name) },
                onPick = { dst ->
                    pickingShareFolder = false
                    shareNotice = null
                    // The old target is gone at once; Upload waits for the new one.
                    choose(null)
                    if (CrUri.isRoot(dst)) {
                        choose(ShareTarget(CrUri.MY_PREFIX, null))
                    } else {
                        // A folder below My files only with its id (from the listing, else asked now).
                        val listed = viewModel.pickerFolderId(dst)
                        if (listed != null) choose(ShareTarget(dst, listed))
                        else scope.launch {
                            val mine = ++pickSeq
                            val id = runCatching { app.fileRepository.folderInfo(dst) }.getOrNull()
                                ?.takeIf { it.isFolder && it.owned == true }?.id
                            // Only for this share and while nothing else was chosen meanwhile.
                            if (mine != pickSeq || io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.value !== uris || io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value != null) return@launch
                            if (id != null) choose(ShareTarget(dst, id)) else shareNotice = context.getString(R.string.share_folder_unchecked)
                        }
                    }
                },
                onDismiss = { pickingShareFolder = false },
            )
        } else {
            ShareTargetSheet(
                count = uris.size,
                last = last,
                favorites = favoriteFolders,
                chosen = chosen,
                // A folder chosen in the picker is shown too, so the target is always visible.
                extra = chosen?.takeIf { c -> c.uri != CrUri.MY_PREFIX && c.uri != last?.uri && favoriteFolders.none { it.uri == c.uri } },
                busy = shareBusy,
                notice = shareNotice,
                onChoose = { pickSeq++; choose(it); shareNotice = null },
                onPickOther = { pickingShareFolder = true },
                onUpload = {
                    val target = chosen ?: return@ShareTargetSheet
                    // Below My files only with a known id: otherwise the folder could not be checked.
                    if (!CrUri.isRoot(target.uri) && target.id.isNullOrEmpty()) {
                        shareNotice = context.getString(R.string.share_folder_unchecked)
                        return@ShareTargetSheet
                    }
                    shareBusy = true
                    scope.launch {
                        val tag = app.sessionManager.sessionTag()
                        val account = UploadWorker.currentAccount(app)
                        // The folder must still be the chosen one (by id); without a connection the
                        // upload checks it itself before every file.
                        val same = target.id?.let { app.fileRepository.folderIdMatches(target.uri, it) } ?: true
                        if (same == false) {
                            shareBusy = false
                            // Only the target that was checked is dropped, not a newer choice.
                            if (io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value == (target.uri to target.id)) choose(null)
                            shareNotice = context.getString(R.string.share_folder_changed)
                            return@launch
                        }
                        // Signed out or another account meanwhile: nothing is queued. Closed or another
                        // folder chosen while it was checked: nothing either.
                        if (!app.sessionManager.isCurrent(tag)) { closeShare(); return@launch }
                        val stillChosen = io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value == (target.uri to target.id)
                        if (io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.value !== uris || !stillChosen) { shareBusy = false; return@launch }
                        withContext(Dispatchers.IO) {
                            UploadWorker.enqueueSafely(context, uris, target.uri, expectedParentId = target.id, account = account)
                            if (target.id != null) shareStore.set(target.uri, target.id) else shareStore.set(target.uri, "")
                        }
                        closeShare()
                    }
                },
                onDismiss = { closeShare() },
            )
        }
    }

    // Fylvo: folder colour and tags of the selection (or of the file of a sheet).
    colorTarget?.let { folders ->
        FolderColorSheet(
            folders = folders,
            onPick = { hex ->
                colorTarget = null
                scope.launch {
                    val patch = io.github.svenbuild.fylvo.android.api.MetadataPatch(
                        key = io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.ICON_COLOR, value = hex.orEmpty(), remove = hex == null,
                    )
                    customizeResult(prefsRepo.patchMetadata(folders.map { it.path }, listOf(patch)))
                    viewModel.refresh()
                }
            },
            onDismiss = { colorTarget = null },
        )
    }
    tagsTarget?.let { files ->
        TagsSheet(
            files = files,
            known = prefsState.tags,
            onSet = { name, color, on ->
                scope.launch {
                    val patch = io.github.svenbuild.fylvo.android.api.MetadataPatch(
                        key = io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.TAG_PREFIX + name, value = color, remove = !on,
                    )
                    customizeResult(prefsRepo.patchMetadata(files.map { it.path }, listOf(patch)))
                }
            },
            onDismiss = {
                tagsTarget = null
                viewModel.refresh()
            },
        )
    }

    state.offlinePrompt?.let { prompt ->
        OfflinePromptDialog(prompt, onConfirm = { viewModel.confirmOffline() }, onDismiss = { viewModel.dismissOfflinePrompt() })
    }
}

// ---- helpers --------------------------------------------------------------

/** Title of the place on screen: the category's name, otherwise the folder's. */
@Composable
private fun FilesUiState.folderTitle(): String =
    placeTitle ?: category?.let { stringResource(it.label) } ?: CrUri.displayName(currentUri)

/**
 * True while the user scrolls away from the top (content moving up), so the
 * FAB gets out of the way like in Gmail and Files; false again on any scroll
 * back or at rest near the top.
 */
@Composable
private fun rememberScrollingDown(useGrid: Boolean, listState: LazyListState, gridState: androidx.compose.foundation.lazy.grid.LazyGridState): Boolean {
    var down by remember { mutableStateOf(false) }
    // Both states persist for the screen's lifetime; key on the active mode or
    // grid mode would keep watching the dead list state after one list view.
    LaunchedEffect(useGrid) {
        var lastIndex = 0
        var lastOffset = 0
        snapshotFlow {
            if (useGrid) gridState.firstVisibleItemIndex to gridState.firstVisibleItemScrollOffset
            else listState.firstVisibleItemIndex to listState.firstVisibleItemScrollOffset
        }.collect { (index, offset) ->
            val movingDown = index > lastIndex || (index == lastIndex && offset > lastOffset + 4)
            val movingUp = index < lastIndex || (index == lastIndex && offset < lastOffset - 4)
            if (movingDown) down = true else if (movingUp || (index == 0 && offset == 0)) down = false
            lastIndex = index
            lastOffset = offset
        }
    }
    return down
}

private fun openWithSystem(context: Context, local: File) {
    val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", local)
    val intent = Intent(Intent.ACTION_VIEW).apply {
        setDataAndType(uri, context.contentResolver.getType(uri) ?: "*/*")
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    try {
        context.startActivity(intent)
    } catch (_: ActivityNotFoundException) {
        // Fylvo 1.6: no app for this kind of file: say so (the file stays in the cache).
        android.widget.Toast.makeText(context, context.getString(R.string.no_app_for_file), android.widget.Toast.LENGTH_LONG).show()
    }
}

/** Fylvo: the pages of places that are not folder listings. */
internal const val PAGE_FAVORITES = "favorites"
internal const val PAGE_TAGS = "tags"
/** Fylvo 1.6: the pages over Account. */
internal const val ACCOUNT_PROFILE = "profile"
internal const val ACCOUNT_SECURITY = "security"
internal const val ACCOUNT_TASKS = "tasks"
internal const val ACCOUNT_CONNECTED = "connected"
/** Fylvo 1.6: "Shared by me", from the Shared tab or from Account. */
internal const val PAGE_SHARES = "shares"
/** Fylvo: [FilesUiState.origin] of a place opened from a page ("page:<page>"). */
internal const val PAGE_ORIGIN = "page:"
