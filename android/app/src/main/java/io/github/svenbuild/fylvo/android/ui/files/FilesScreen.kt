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
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.UploadFile
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
import androidx.compose.runtime.remember
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
import io.github.svenbuild.fylvo.android.ui.settings.AccountScreen
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
    onSignOut: () -> Unit,
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
    var preview by remember { mutableStateOf<Pair<List<FileObject>, Int>?>(null) }
    var renameTarget by remember { mutableStateOf<FileObject?>(null) }
    var deleteTarget by remember { mutableStateOf<FileObject?>(null) }
    /** Fylvo: the selected paths to delete, taken when the question opens. */
    var deleteSelectedTarget by remember { mutableStateOf<List<String>?>(null) }
    var deleteForeverTarget by remember { mutableStateOf<List<String>?>(null) }
    /** Paths to move/copy and whether it is a copy; non-null shows the folder picker. */
    var movePick by remember { mutableStateOf<Pair<List<String>, Boolean>?>(null) }
    /** Folder the search was opened from (scope chip), null when opened at root/home. */
    var searchOpenedFrom by remember { mutableStateOf<String?>(null) }
    /** Fylvo: the Account tab, and the offline files page reached from it. */
    // Saveable: a language change restarts the activity and should land on the Account tab again.
    var accountOpen by rememberSaveable { mutableStateOf(false) }
    var offlineOpen by remember { mutableStateOf(false) }
    val searchFocus = remember { FocusRequester() }

    val inSearch = state.searchQuery != null

    var pendingUpload by remember { mutableStateOf<PendingUpload?>(null) }
    val haptics = LocalHapticFeedback.current

    val uploadLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        val parentUri = state.currentUri
        val loaded = state.files.map { it.name }.toSet()
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

    fun openWithSystemApp(file: FileObject) {
        viewModel.fetchForOpen(context, file) { local -> openWithSystem(context, local) }
    }

    /** In-app viewer for media, PDFs and text; everything else goes to the system. */
    fun openFile(file: FileObject) {
        // Fylvo: an unfinished upload has no content yet; its sheet says so.
        if (file.isUploading) {
            sheetTarget = file
            return
        }
        if (previewKindOf(file) == null) {
            openWithSystemApp(file)
            return
        }
        val siblings = if (inSearch || state.home) listOf(file) else viewModel.previewableSiblings(file)
        preview = siblings to siblings.indexOfFirst { it.path == file.path }.coerceAtLeast(0)
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

    // ---- Fylvo: selection button and its menu ----------------------------------
    var menuOpen by remember { mutableStateOf(false) }
    /** Where the selection button is (window coordinates); the menu card grows out of it. */
    var fabAnchor by remember { mutableStateOf<Rect?>(null) }
    // The card closes with the selection.
    LaunchedEffect(state.selecting) { if (!state.selecting) menuOpen = false }
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
        }
    }

    /** Fylvo: which tab of the bottom bar the screen belongs to. */
    val currentTab = when {
        accountOpen -> Tab.ACCOUNT
        state.home -> Tab.HOME
        state.category == Category.SHARED_WITH_ME || CrUri.isShare(state.currentUri) -> Tab.SHARED
        state.category == null || state.category == Category.TRASH -> Tab.FILES
        else -> Tab.HOME
    }
    val atTabRoot = state.category == Category.SHARED_WITH_ME ||
        (state.category == null && CrUri.isRoot(state.currentUri) && !CrUri.isShare(state.currentUri))

    fun selectTab(tab: Tab) {
        // Fylvo: a tab change ends a selection, also towards Account.
        viewModel.clearSelection()
        accountOpen = tab == Tab.ACCOUNT
        offlineOpen = false
        when (tab) {
            Tab.HOME -> viewModel.goHome()
            Tab.FILES -> viewModel.openMyFiles()
            Tab.SHARED -> viewModel.openCategory(Category.SHARED_WITH_ME)
            Tab.ACCOUNT -> Unit
        }
    }

    BackHandler(enabled = menuOpen || state.selecting || inSearch || !state.home) {
        when {
            menuOpen -> menuOpen = false
            state.selecting -> viewModel.clearSelection()
            inSearch -> closeSearch()
            else -> viewModel.navigateUp()
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
                    sort = state.view.sort,
                    onBack = { viewModel.navigateUp() },
                    onSearch = { openSearch() },
                    onToggleView = { viewModel.toggleViewMode() },
                    onSort = { viewModel.sortBy(it) },
                    onRefresh = { viewModel.refresh() },
                    onTrash = { viewModel.openCategory(Category.TRASH) },
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
        bottomBar = { if (!inSearch) BottomTabs(currentTab, ::selectTab) },
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
                    onOpenRecent = { viewModel.openCategory(Category.RECENT) },
                    onFileMore = { sheetTarget = it },
                )
                inSearch -> SearchResults(
                    state = state,
                    viewModel = viewModel,
                    listState = listState,
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
                state.error != null -> EmptyState(
                    icon = Icons.Outlined.FolderOpen,
                    title = stringResource(R.string.load_folder_failed),
                    detail = state.error?.asString(),
                    action = stringResource(R.string.retry) to { viewModel.refresh() },
                )
                state.files.isEmpty() && state.uploadsHere.isEmpty() -> when (state.category) {
                    null -> EmptyState(
                        icon = Icons.Outlined.FolderOpen,
                        title = stringResource(R.string.folder_empty),
                        detail = if (state.canAddHere) stringResource(R.string.folder_empty_hint) else null,
                    )
                    Category.TRASH -> EmptyState(icon = Icons.Outlined.Delete, title = stringResource(R.string.trash_empty), detail = stringResource(R.string.trash_empty_hint))
                    Category.RECENT -> EmptyState(icon = Icons.Outlined.History, title = stringResource(R.string.nothing_recent), detail = stringResource(R.string.recent_hint))
                    Category.IMAGES -> EmptyState(icon = Icons.Outlined.Image, title = stringResource(R.string.no_images))
                    Category.VIDEOS -> EmptyState(icon = Icons.Outlined.Videocam, title = stringResource(R.string.no_videos))
                    Category.AUDIO -> EmptyState(icon = Icons.Outlined.MusicNote, title = stringResource(R.string.no_audio))
                    Category.DOCUMENTS -> EmptyState(icon = Icons.Outlined.Description, title = stringResource(R.string.no_documents))
                    Category.SHARED_WITH_ME -> EmptyState(icon = Icons.Outlined.FolderOpen, title = stringResource(R.string.nothing_shared))
                }
                else -> Column(Modifier.fillMaxSize()) {
                    if (state.offlineMode) OfflineBanner()
                    Box(Modifier.weight(1f)) {
                        if (state.effectiveMode == ViewMode.GRID) FileGrid(state, viewModel, gridState, ::actionsFor)
                        else FileList(state, viewModel, listState, ::actionsFor)
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
        !accountOpen && !offlineOpen && preview == null && movePick == null && sheetTarget == null && !addSheetOpen &&
        !mkdirOpen && renameTarget == null && deleteTarget == null && deleteSelectedTarget == null &&
        deleteForeverTarget == null && shareTarget == null && detailsTarget == null && pendingUpload == null &&
        state.offlinePrompt == null
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
            canShare = Permissions::canShareLink,
            canDelete = Permissions::canModifyFile,
        )
      }
    }

    if (addSheetOpen) {
        ModalBottomSheet(onDismissRequest = { addSheetOpen = false }) {
            ListItem(
                modifier = Modifier.clickable {
                    addSheetOpen = false
                    uploadLauncher.launch(arrayOf("*/*"))
                },
                headlineContent = { Text(stringResource(R.string.upload_files)) },
                supportingContent = { Text(stringResource(R.string.into_folder, state.folderTitle())) },
                leadingContent = { Icon(Icons.Default.UploadFile, contentDescription = null) },
            )
            ListItem(
                modifier = Modifier.clickable {
                    addSheetOpen = false
                    mkdirOpen = true
                },
                headlineContent = { Text(stringResource(R.string.new_folder)) },
                leadingContent = { Icon(Icons.Default.CreateNewFolder, contentDescription = null) },
            )
            Box(Modifier.height(24.dp))
        }
    }

    detailsTarget?.let { file ->
        DetailsSheet(file = file, thumbUrl = rememberThumbUrl(viewModel, file), onDismiss = { detailsTarget = null })
    }

    shareTarget?.let { file ->
        ShareLinkSheet(
            file = file,
            thumbUrl = rememberThumbUrl(viewModel, file),
            createLink = { pw, exp, dl -> viewModel.shareLink(file, pw, exp, dl) },
            onDismiss = { shareTarget = null },
            onMessage = { msg -> scope.launch { snackbar.showSnackbar(msg) } },
        )
    }

    sheetTarget?.let { file ->
        FileActionSheet(
            file = file,
            thumbUrl = rememberThumbUrl(viewModel, file),
            offlineOn = state.offlineOn(file),
            offlineCovered = state.offlineCovered(file),
            offlineAllowed = state.offlineAllowed(file),
            onDismiss = { sheetTarget = null },
            onOpen = { sheetTarget = null; if (file.isFolder) viewModel.navigateTo(file) else openFile(file) },
            onShare = { sheetTarget = null; shareLink(file) },
            onDownload = { viewModel.saveToDevice(context, file) },
            onToggleOffline = { sheetTarget = null; viewModel.toggleOffline(file) },
            onRename = { sheetTarget = null; renameTarget = file },
            onDelete = { sheetTarget = null; deleteTarget = file },
            onMove = { sheetTarget = null; movePick = listOf(file.path) to false },
            onCopy = { sheetTarget = null; movePick = listOf(file.path) to true },
            onDetails = { sheetTarget = null; detailsTarget = file },
            canModify = Permissions.canModifyFile(file),
            canShare = Permissions.canShareLink(file),
            inTrash = state.inTrash,
            onRestore = { sheetTarget = null; viewModel.restore(listOf(file.path)) },
            onDeleteForever = { sheetTarget = null; deleteForeverTarget = listOf(file.path) },
            onShowInFolder = if (!state.inTrash && (state.home || state.virtual || inSearch)) ({
                sheetTarget = null
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
            onCreateFolder = { parent, name -> viewModel.mkdirIn(parent, name) },
            onPick = { dst ->
                movePick = null
                viewModel.moveTo(paths, dst, copy)
            },
            onDismiss = { movePick = null },
        )
      }
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
            onConfirm = {
                viewModel.deletePaths(paths)
                deleteSelectedTarget = null
            },
            onDismiss = { deleteSelectedTarget = null },
        )
    }

    if (accountOpen) {
        AccountScreen(
            offlineCount = state.offlineRoots.size,
            onOpenOffline = { offlineOpen = true },
            onBack = { accountOpen = false },
            onSignOut = onSignOut,
            bottomBar = { BottomTabs(Tab.ACCOUNT, ::selectTab) },
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
            onBack = { offlineOpen = false },
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
            bottomBar = { BottomTabs(Tab.ACCOUNT, ::selectTab) },
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
    category?.let { stringResource(it.label) } ?: CrUri.displayName(currentUri)

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
        // No handler; the file stays in cache.
    }
}
