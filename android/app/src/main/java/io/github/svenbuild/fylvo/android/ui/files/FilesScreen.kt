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
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.Delete
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
import androidx.compose.material3.FloatingActionButton
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
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
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
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
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
    var deleteSelectedOpen by remember { mutableStateOf(false) }
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
            val pending = withContext(Dispatchers.IO) {
                // All names in the folder, not only the loaded pages (a big folder has many).
                val existing = loaded + viewModel.namesInFolder(parentUri)
                PendingUpload(uris, UploadWorker.uniqueWithinSelection(uris.map { UploadWorker.displayName(context, it) }, existing), parentUri, existing)
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
            duration = if (msg.action != null) SnackbarDuration.Long else SnackbarDuration.Short,
        )
        viewModel.consumeSnackbar()
        if (result == SnackbarResult.ActionPerformed) msg.action?.invoke()
    }

    // Infinite scroll: load the next page when the end comes into view.
    LaunchedEffect(listState, gridState, state.effectiveMode) {
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

    fun actionsFor(file: FileObject): FileActions = FileActions(
        onOpen = {
            when {
                state.selecting -> viewModel.toggleSelected(file)
                state.inTrash -> sheetTarget = file
                file.isFolder -> viewModel.navigateTo(file)
                else -> openFile(file)
            }
        },
        onLongPress = {
            haptics.performHapticFeedback(HapticFeedbackType.LongPress)
            viewModel.toggleSelected(file)
        },
        onMore = { sheetTarget = file },
    )

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
        accountOpen = tab == Tab.ACCOUNT
        offlineOpen = false
        when (tab) {
            Tab.HOME -> viewModel.goHome()
            Tab.FILES -> viewModel.openMyFiles()
            Tab.SHARED -> viewModel.openCategory(Category.SHARED_WITH_ME)
            Tab.ACCOUNT -> Unit
        }
    }

    BackHandler(enabled = state.selecting || inSearch || !state.home) {
        when {
            state.selecting -> viewModel.clearSelection()
            inSearch -> closeSearch()
            else -> viewModel.navigateUp()
        }
    }

    Scaffold(
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
                    total = state.files.size,
                    inTrash = state.inTrash,
                    // Fylvo: move, copy and delete only when every picked file may be changed.
                    canModify = state.files.filter { it.path in state.selected }.all(Permissions::canModifyFile),
                    onClose = { viewModel.clearSelection() },
                    onSelectAll = { viewModel.selectAll() },
                    onMove = { movePick = state.selected.toList() to false },
                    onCopy = { movePick = state.selected.toList() to true },
                    onDelete = { if (state.inTrash) deleteForeverTarget = state.selected.toList() else deleteSelectedOpen = true },
                    onRestore = { viewModel.restore(state.selected.toList()) },
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
                    onSelect = { state.files.firstOrNull()?.let { viewModel.toggleSelected(it) } },
                    onRefresh = { viewModel.refresh() },
                    onTrash = { viewModel.openCategory(Category.TRASH) },
                )
            } }
        },
        floatingActionButton = {
            val scrollingDown = rememberScrollingDown(state.effectiveMode == ViewMode.GRID, listState, gridState)
            AnimatedVisibility(
                visible = state.canAddHere && !state.selecting && !inSearch && !scrollingDown,
                enter = scaleIn() + fadeIn(),
                exit = scaleOut() + fadeOut(),
            ) {
                // Brand primary, not M3's default tertiaryContainer purple —
                // matches the web app's primary action colour.
                FloatingActionButton(
                    onClick = { addSheetOpen = true },
                    containerColor = MaterialTheme.colorScheme.primary,
                    contentColor = MaterialTheme.colorScheme.onPrimary,
                ) {
                    Icon(Icons.Default.Add, contentDescription = stringResource(R.string.add))
                }
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
        bottomBar = { if (!state.selecting && !inSearch) BottomTabs(currentTab, ::selectTab) },
    ) { padding ->
        // Spin the pull indicator only for a pull the user made; initial and
        // background loads have their own, quieter indicators.
        var pulled by remember { mutableStateOf(false) }
        val busy = if (state.home) state.recentLoading else state.loading || state.revalidating
        LaunchedEffect(busy) { if (!busy) pulled = false }
        PullToRefreshBox(
            isRefreshing = pulled && busy,
            onRefresh = {
                pulled = true
                if (state.home) viewModel.loadRecent() else viewModel.refresh()
            },
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
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
                            onLongPress = {},
                            onMore = { sheetTarget = file },
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
                state.effectiveMode == ViewMode.GRID -> FileGrid(state, viewModel, gridState, ::actionsFor)
                else -> FileList(state, viewModel, listState, ::actionsFor)
            } }
        }
    }

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
            isFavorite = file.path in state.favoritePaths,
            onDismiss = { sheetTarget = null },
            onOpen = { sheetTarget = null; if (file.isFolder) viewModel.navigateTo(file) else openFile(file) },
            onShare = { sheetTarget = null; shareLink(file) },
            onDownload = { viewModel.saveToDevice(context, file) },
            onToggleFavorite = { sheetTarget = null; viewModel.toggleFavorite(context, file) },
            onRename = { sheetTarget = null; renameTarget = file },
            onDelete = { sheetTarget = null; deleteTarget = file },
            onSelect = { sheetTarget = null; viewModel.toggleSelected(file) },
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

    if (deleteSelectedOpen) {
        val n = state.selected.size
        ConfirmDeleteDialog(
            title = pluralStringResource(R.plurals.delete_items_title, n, n),
            onConfirm = {
                viewModel.deleteSelected()
                deleteSelectedOpen = false
            },
            onDismiss = { deleteSelectedOpen = false },
        )
    }

    if (accountOpen) {
        AccountScreen(
            offlineCount = state.favorites.size,
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
            favorites = state.favorites,
            onBack = { offlineOpen = false },
            onOpen = { entry -> openWithSystem(context, File(entry.localFile)) },
            onRefresh = { entry -> viewModel.refreshFavorite(context, entry) },
            onRemove = { entry -> viewModel.removeFavorite(entry) },
            bottomBar = { BottomTabs(Tab.ACCOUNT, ::selectTab) },
        )
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
