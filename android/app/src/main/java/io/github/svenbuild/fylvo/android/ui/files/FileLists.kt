package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.ScrollableState
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.awaitLongPressOrCancellation
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ViewMode
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.ui.files.transfers.UploadsCard
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.grid.LazyGridState

// ---- content --------------------------------------------------------------

@Composable
@OptIn(ExperimentalFoundationApi::class)
internal fun FileList(
    state: FilesUiState,
    viewModel: FilesViewModel,
    listState: LazyListState,
    actionsFor: (FileObject) -> FileActions,
    /** Fylvo: swipe actions per row (list view only). */
    swipeFor: (FileObject) -> RowSwipe? = { null },
) {
    val today = stringResource(R.string.today)
    val yesterday = stringResource(R.string.yesterday)
    val unknownDate = stringResource(R.string.unknown_date)
    val unknown = stringResource(R.string.unknown)
    val trashSubtitle = stringResource(R.string.trash_row_subtitle)
    val uploadContext = LocalContext.current.applicationContext
    // Fylvo: the upload card is put above the first file; near the top, show it instead of
    // keeping the first file in place (which pushed the card out of view).
    val hasUploads = state.uploadsHere.isNotEmpty()
    LaunchedEffect(hasUploads) { if (hasUploads && listState.firstVisibleItemIndex <= 1) listState.scrollToItem(0) }
    val haptics = LocalHapticFeedback.current
    LazyColumn(
        state = listState,
        modifier = Modifier
            .fillMaxSize()
            .dragSelect(state.currentUri, state.effectiveMode, listState, viewModel, haptics) {
                val info = listState.layoutInfo
                // Item offsets count from the viewport start, which lies above the top content padding.
                info.visibleItemsInfo.map { DragItem(it.key, top = it.offset - info.viewportStartOffset, height = it.size) }
            },
        contentPadding = PaddingValues(bottom = 88.dp),
    ) {
        uploadItems(state, viewModel, uploadContext)
        val groups = dayGroups(state)
        if (groups != null) {
            // Timeline views read by day, like the Files app's category lists.
            groups.forEach { (day, group) ->
                // Fylvo: unfinished uploads are not selected from the header.
                val selectable = group.filter { !it.isUploading }
                stickyHeader(key = "day-$day") {
                    DayHeader(
                        label = dayGroupLabel(group.first().updatedAt, today = today, yesterday = yesterday, unknown = unknownDate),
                        allSelected = selectable.all { it.path in state.selected },
                        onToggle = if (selectable.isEmpty()) null else ({ viewModel.toggleGroup(group) }),
                    )
                }
                items(group, key = ::fileKey, contentType = { FILE_ITEM }) { file ->
                    FileListItem(
                        file = file,
                        thumbUrl = rememberThumbUrl(viewModel, file),
                        selecting = state.selecting,
                        selected = file.path in state.selected,
                        downloadProgress = state.downloading[file.path],
                        downloading = file.path in state.downloading,
                        offline = state.isOffline(file),
                        edit = state.editOf(file),
                        actions = rememberActions(actionsFor(file)),
                        swipe = rememberSwipe(swipeFor(file)),
                        subtitle = "${fileSubtitle(file)} · ${CrUri.parentDisplayName(file.path)}",
                    )
                }
            }
            if (state.loadingMore) item { LoadingMoreRow() }
            return@LazyColumn
        }
        items(state.files, key = ::fileKey, contentType = { FILE_ITEM }) { file ->
            FileListItem(
                file = file,
                thumbUrl = rememberThumbUrl(viewModel, file),
                selecting = state.selecting,
                selected = file.path in state.selected,
                downloadProgress = state.downloading[file.path],
                downloading = file.path in state.downloading,
                offline = state.isOffline(file),
                edit = state.editOf(file),
                actions = rememberActions(actionsFor(file)),
                swipe = rememberSwipe(swipeFor(file)),
                subtitle = if (state.inTrash) {
                    trashSubtitle.format(formatDate(file.updatedAt), file.restoreUri?.let { CrUri.displayName(it) } ?: unknown)
                } else {
                    fileSubtitle(file)
                },
            )
        }
        if (state.loadingMore) {
            item { LoadingMoreRow() }
        }
    }
}

@Composable
internal fun FileGrid(
    state: FilesUiState,
    viewModel: FilesViewModel,
    gridState: LazyGridState,
    actionsFor: (FileObject) -> FileActions,
) {
    val gallery = state.category?.gallery == true
    val uploadContext = LocalContext.current.applicationContext
    val hasUploads = state.uploadsHere.isNotEmpty()
    LaunchedEffect(hasUploads) { if (hasUploads && gridState.firstVisibleItemIndex <= 1) gridState.scrollToItem(0) }
    val haptics = LocalHapticFeedback.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    val sidePadding = if (gallery) 3.dp else 10.dp
    LazyVerticalGrid(
        columns = GridCells.Adaptive(minSize = if (gallery) 118.dp else 110.dp),
        state = gridState,
        modifier = Modifier
            .fillMaxSize()
            .dragSelect(state.currentUri, state.effectiveMode, gridState, viewModel, haptics) {
                val info = gridState.layoutInfo
                // Item offsets leave out the content padding: x counts from the start padding,
                // y from the viewport start (above the top padding).
                val width = info.viewportSize.width.toFloat()
                info.visibleItemsInfo.map {
                    val left = sidePadding.toPx() + it.offset.x
                    DragItem(
                        it.key,
                        top = it.offset.y - info.viewportStartOffset,
                        height = it.size.height,
                        left = if (rtl) width - left - it.size.width else left,
                        width = it.size.width.toFloat(),
                    )
                }
            },
        contentPadding = if (gallery) PaddingValues(start = sidePadding, end = sidePadding, top = 3.dp, bottom = 88.dp)
        else PaddingValues(start = sidePadding, end = sidePadding, top = 4.dp, bottom = 88.dp),
        horizontalArrangement = Arrangement.spacedBy(if (gallery) 3.dp else 4.dp),
        verticalArrangement = Arrangement.spacedBy(if (gallery) 3.dp else 4.dp),
    ) {
        if (state.uploadsHere.isNotEmpty()) {
            item(span = { GridItemSpan(maxLineSpan) }, key = "uploads") { UploadsCard(state.uploadsHere) { id -> viewModel.cancelUpload(uploadContext, id) } }
        }
        items(state.files, key = ::fileKey, contentType = { FILE_ITEM }) { file ->
            FileGridItem(
                file = file,
                thumbUrl = rememberThumbUrl(viewModel, file),
                selecting = state.selecting,
                selected = file.path in state.selected,
                downloadProgress = state.downloading[file.path],
                downloading = file.path in state.downloading,
                actions = rememberActions(actionsFor(file)),
                gallery = gallery,
                offline = state.isOffline(file),
                edit = state.editOf(file),
            )
        }
        if (state.loadingMore) {
            item(span = { GridItemSpan(maxLineSpan) }) { LoadingMoreRow() }
        }
    }
}

private fun LazyListScope.uploadItems(state: FilesUiState, viewModel: FilesViewModel, uploadContext: android.content.Context) {
    if (state.uploadsHere.isNotEmpty()) {
        item(key = "uploads") { UploadsCard(state.uploadsHere) { id -> viewModel.cancelUpload(uploadContext, id) } }
    }
}

/**
 * Day separator with a Files-app style select-all circle at the end. Fylvo: no circle
 * ([onToggle] null) when the day holds nothing selectable (only unfinished uploads).
 */
@Composable
private fun DayHeader(label: String, allSelected: Boolean, onToggle: (() -> Unit)?) {
    Row(
        Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surface)
            .padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(label, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
        if (onToggle != null) {
            IconButton(onClick = onToggle, modifier = Modifier.size(36.dp)) {
                Icon(
                    if (allSelected) Icons.Default.CheckCircle else Icons.Outlined.CheckCircle,
                    contentDescription = stringResource(if (allSelected) R.string.clear_selection_for else R.string.select_all_from, label),
                    tint = if (allSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        } else {
            Box(Modifier.size(36.dp))
        }
    }
}

// ---- display order and drag selection ---------------------------------------

/** Content type of file rows and tiles (the lists also hold headers, the upload card and a loading row). */
private const val FILE_ITEM = "file"

/** Key of a file's row or tile in the lazy lists. */
internal fun fileKey(file: FileObject): String = file.id + file.path

/** Fylvo: the day groups of a timeline list (categories outside the trash); null when not grouped. */
internal fun dayGroups(state: FilesUiState): Map<String, List<FileObject>>? =
    if (state.virtual && !state.inTrash && state.effectiveMode == ViewMode.LIST) state.files.groupBy { dayKey(it.updatedAt) } else null

/** Fylvo: the files in the order they are shown (day groups one after another). */
internal fun displayOrder(state: FilesUiState): List<FileObject> = dayGroups(state)?.values?.flatten() ?: state.files

/** A visible item of a lazy list or grid, in the list's own pointer coordinates. */
private class DragItem(
    val key: Any,
    val top: Int,
    val height: Int,
    val left: Float = 0f,
    val width: Float = Float.MAX_VALUE,
) {
    operator fun contains(p: Offset): Boolean = p.y >= top && p.y < top + height && p.x >= left && p.x < left + width
}

/** Band at the top and bottom edge in which a drag scrolls the list, and the fastest step per frame. */
private val AutoScrollEdge = 48.dp
private val AutoScrollStep = 20.dp

/**
 * Fylvo: a long press selects a file; keeping the finger down and dragging selects every file
 * between the first one and the one under the finger (like Google Photos), scrolling at the top
 * and bottom edge. The list owns this gesture, the rows keep a plain tap.
 *
 * A long press on anything but a file (day header, upload card, gap) starts nothing, so the list
 * keeps scrolling. After a valid long press every move and the release are consumed before the
 * rows see them, so the row's tap does not fire as well.
 */
private fun Modifier.dragSelect(
    currentUri: String,
    mode: ViewMode,
    scroll: ScrollableState,
    viewModel: FilesViewModel,
    haptics: HapticFeedback,
    visibleItems: Density.() -> List<DragItem>,
): Modifier = pointerInput(currentUri, mode) {
    coroutineScope {
        awaitEachGesture {
            val down = awaitFirstDown(requireUnconsumed = false)
            val press = awaitLongPressOrCancellation(down.id) ?: return@awaitEachGesture

            /** Lazy keys of the files on screen, to their paths. */
            fun paths(): Map<Any, String> = displayOrder(viewModel.state.value).associate { fileKey(it) to it.path }

            /** The file under [p]; null over anything else, also where a pinned day header covers a file. */
            fun fileAt(p: Offset): String? {
                val paths = paths()
                val hits = visibleItems().filter { p in it }
                if (hits.any { it.key !in paths }) return null
                return hits.firstOrNull()?.let { paths[it.key] }
            }

            /** The first or last visible file, for a finger at the top or bottom edge. */
            fun edgeFile(top: Boolean): String? {
                val paths = paths()
                val files = visibleItems().filter { it.key in paths }.sortedBy { it.top }
                return (if (top) files.firstOrNull() else files.lastOrNull())?.let { paths[it.key] }
            }

            val anchor = fileAt(press.position) ?: return@awaitEachGesture
            press.consume()
            haptics.performHapticFeedback(HapticFeedbackType.LongPress)
            val before = viewModel.state.value.selected
            val edge = AutoScrollEdge.toPx()
            var current = anchor
            var finger = press.position
            // The edge bands count only once the finger moved: the first row lies in the top band.
            var moved = false
            var stopped = false
            // The first update selects the anchor; from then on an empty selection was cleared from outside.
            var started = false

            /**
             * How far the finger reaches into the top (negative) or bottom (positive) edge band,
             * 1 at the edge and beyond it (over the top bar or the tabs); 0 elsewhere.
             */
            fun edgePull(): Float {
                val y = finger.y
                return when {
                    y < 0f || (moved && y < edge) -> -((edge - y) / edge).coerceAtMost(1f)
                    y > size.height || (moved && y > size.height - edge) -> ((y - (size.height - edge)) / edge).coerceAtMost(1f)
                    else -> 0f
                }
            }

            fun update() {
                // Cleared from outside (back, a tab) while a scroll step ran: nothing comes back.
                if (stopped || (started && viewModel.state.value.selected.isEmpty())) {
                    stopped = true
                    return
                }
                val pull = edgePull()
                val target = when {
                    pull < 0f -> edgeFile(top = true)
                    pull > 0f -> edgeFile(top = false)
                    else -> {
                        // Beside the grid's tiles (side padding or past the edge) the nearest column counts.
                        val files = visibleItems().filter { it.width != Float.MAX_VALUE }
                        val minX = files.minOfOrNull { it.left } ?: 0f
                        val maxX = files.maxOfOrNull { it.left + it.width - 1f } ?: (size.width - 1f)
                        fileAt(Offset(finger.x.coerceIn(minX, maxOf(minX, maxX)), finger.y))
                    }
                }
                // Over a gap or a header the last file stays the end of the range.
                if (target != null) current = target
                val s = viewModel.state.value
                val next = dragSelection(displayOrder(s), before, anchor, current)
                if (next == null) {
                    stopped = true
                    return
                }
                if (next != s.selected) viewModel.setSelected(next)
                started = true
            }

            update()
            // Scrolls while the finger is in the edge band or beyond it, then selects again.
            val autoScroll = launch {
                val step = AutoScrollStep.toPx()
                while (isActive) {
                    // Cleared from outside (back, a tab) or the anchor is gone: no more scrolling.
                    if (stopped || viewModel.state.value.selected.isEmpty()) break
                    val delta = edgePull() * step
                    if (delta != 0f && scroll.scrollBy(delta) != 0f) update()
                    delay(16)
                }
            }
            try {
                while (!stopped) {
                    val event = awaitPointerEvent(PointerEventPass.Initial)
                    val change = event.changes.firstOrNull { it.id == press.id } ?: break
                    event.changes.forEach { it.consume() }
                    if (!change.pressed) break
                    finger = change.position
                    if ((finger - press.position).getDistance() > viewConfiguration.touchSlop) moved = true
                    // Cleared from outside (back, a tab): the drag ends with it.
                    if (viewModel.state.value.selected.isEmpty()) break
                    update()
                }
            } finally {
                autoScroll.cancel()
            }
        }
    }
}

@Composable
internal fun LoadingMoreRow() {
    Row(
        Modifier
            .fillMaxWidth()
            .padding(12.dp),
        horizontalArrangement = Arrangement.Center,
    ) { CircularProgressIndicator(Modifier.size(28.dp)) }
}

@Composable
internal fun EmptyState(
    icon: ImageVector,
    title: String,
    detail: String? = null,
    action: Pair<String, () -> Unit>? = null,
) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(32.dp)) {
            Icon(
                icon,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(72.dp),
            )
            Text(
                title,
                style = MaterialTheme.typography.titleLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 16.dp),
            )
            if (detail != null) {
                Text(
                    detail,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            if (action != null) {
                TextButton(onClick = action.second, modifier = Modifier.padding(top = 8.dp)) { Text(action.first) }
            }
        }
    }
}
