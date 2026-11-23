package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.util.CrUri

/**
 * Landing page modelled on the system Files app: a search field, a Recent
 * strip of the newest files anywhere in the drive, category tiles, then
 * the storage entry (My files) and collections (offline files).
 */
@Composable
fun HomeScreen(
    state: FilesUiState,
    viewModel: FilesViewModel,
    onSearch: () -> Unit,
    onOpenFile: (FileObject) -> Unit,
    onOpenRecent: () -> Unit,
    onFileMore: (FileObject) -> Unit,
    /** Fylvo: the account's building blocks; null shows Home as before customisation. */
    extras: HomeExtras? = null,
) {
    // Home has no top app bar, so the Scaffold gives it no top inset: take the
    // status bar height here or the search field draws under the clock.
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .statusBarsPadding()
            .padding(bottom = 96.dp),
    ) {
        SearchField(onSearch)

        // Background work the user did not start right now still deserves a line.
        if (state.uploads.isNotEmpty() || state.backgroundSyncs.isNotEmpty()) {
            ActivityCard(state, onOpenUploads = onOpenRecent)
        }

        if (extras == null) {
            RecentSection(state, viewModel, onOpenFile, onOpenRecent, onFileMore)
        } else {
            extras.widgets.forEach { widget ->
                when (widget) {
                    "recent" -> RecentSection(state, viewModel, onOpenFile, onOpenRecent, onFileMore)
                    "favorites" -> FavoritesSection(extras)
                    "links" -> LinksSection(extras)
                    "tags" -> TagsSection(extras)
                    "storage" -> StorageSection()
                }
            }
        }

        // Fylvo: Google Drive, OneDrive and the like, connected in the web app.
        ConnectedHomeSection(viewModel)
    }
}

/** Fylvo: the newest files anywhere, as a calm list with each file's folder. */
@Composable
private fun RecentSection(
    state: FilesUiState,
    viewModel: FilesViewModel,
    onOpenFile: (FileObject) -> Unit,
    onOpenRecent: () -> Unit,
    onFileMore: (FileObject) -> Unit,
) {
        SectionHeader(stringResource(R.string.category_recent), action = if (state.recent.isNotEmpty()) stringResource(R.string.see_all) to onOpenRecent else null)
        when {
            state.recent.isEmpty() && state.recentLoading -> Box(
                Modifier
                    .fillMaxWidth()
                    .height(150.dp),
                contentAlignment = Alignment.Center,
            ) { CircularProgressIndicator(Modifier.size(28.dp)) }
            state.recent.isEmpty() -> Text(
                stringResource(R.string.recent_hint),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 24.dp),
            )
            // Fylvo: a plain list like Google Drive's Home, with the folder each file is in.
            else -> state.recent.forEach { file ->
                FileListItem(
                    file = file,
                    thumbUrl = rememberThumbUrl(viewModel, file),
                    selecting = false,
                    selected = false,
                    offline = state.isOffline(file),
                    edit = state.editOf(file),
                    // Fylvo: no selection here; a long press opens the file's sheet.
                    actions = FileActions(
                        onOpen = { onOpenFile(file) },
                        onLongPress = { onFileMore(file) },
                        longPressLabel = stringResource(R.string.options),
                        longPressOnRow = true,
                    ),
                    subtitle = "${fileSubtitle(file)} · ${CrUri.parentDisplayName(file.path)}",
                )
            }
        }
}

/** Fylvo: what the customised home page shows besides Recent, and what a tap does. */
data class HomeExtras(
    /** Visible building blocks in the account's order: recent, favorites, links, tags, storage. */
    val widgets: List<String>,
    val favorites: List<FileObject>,
    val links: List<io.github.svenbuild.fylvo.android.data.prefs.Place.Link>,
    val tags: List<io.github.svenbuild.fylvo.android.api.TagInfo>,
    val onOpenFavorite: (FileObject) -> Unit,
    val onFavoriteMore: (FileObject) -> Unit,
    val onAllFavorites: () -> Unit,
    val onOpenLink: (io.github.svenbuild.fylvo.android.data.prefs.Place.Link) -> Unit,
    val onOpenTag: (io.github.svenbuild.fylvo.android.api.TagInfo) -> Unit,
)

/** Fylvo: favourites as tiles that scroll sideways (like Google Drive's suggested folders). */
@OptIn(androidx.compose.foundation.ExperimentalFoundationApi::class)
@Composable
private fun FavoritesSection(extras: HomeExtras) {
    if (extras.favorites.isEmpty()) return
    SectionHeader(stringResource(R.string.favorites), action = stringResource(R.string.see_all) to extras.onAllFavorites)
    LazyRow(
        contentPadding = PaddingValues(horizontal = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        items(extras.favorites.take(MAX_FAVORITE_TILES), key = { it.id }) { file ->
            val options = stringResource(R.string.options)
            Surface(
                shape = RoundedCornerShape(14.dp),
                color = MaterialTheme.colorScheme.surfaceContainer,
                border = androidx.compose.foundation.BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
                modifier = Modifier
                    .width(120.dp)
                    .clip(RoundedCornerShape(14.dp))
                    .combinedClickable(
                        onClick = { extras.onOpenFavorite(file) },
                        onLongClick = { extras.onFavoriteMore(file) },
                        onLongClickLabel = options,
                    ),
            ) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    FileThumb(file, thumbUrl = null, size = 24.dp, iconSize = 22.dp, plain = true)
                    Text(file.name, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(
                        if (file.isFolder) stringResource(R.string.folder) else CrUri.parentDisplayName(file.path),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

private const val MAX_FAVORITE_TILES = 12

/** Fylvo: the sidebar's shortcuts as rows. */
@Composable
private fun LinksSection(extras: HomeExtras) {
    if (extras.links.isEmpty()) return
    SectionHeader(stringResource(R.string.home_links))
    extras.links.forEach { link ->
        Row(
            Modifier
                .fillMaxWidth()
                .clickable { extras.onOpenLink(link) }
                .padding(horizontal = 20.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(placeIcons(link).first, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(22.dp))
            Text(placeLabel(link), style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 16.dp))
        }
    }
}

/** Fylvo: the account's tags as chips with their count. */
@OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
@Composable
private fun TagsSection(extras: HomeExtras) {
    if (extras.tags.isEmpty()) return
    SectionHeader(stringResource(R.string.tags))
    androidx.compose.foundation.layout.FlowRow(
        Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        extras.tags.forEach { tag -> TagChip(tag) { extras.onOpenTag(tag) } }
    }
}

/** Fylvo: how much of the account's storage is used. */
@Composable
private fun StorageSection() {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as io.github.svenbuild.fylvo.android.FylvoApp
    val capacity by androidx.compose.runtime.produceState<io.github.svenbuild.fylvo.android.api.CapacityResponse?>(initialValue = null) {
        value = app.fileRepository.capacity()
    }
    val cap = capacity?.takeIf { it.total > 0 } ?: return
    SectionHeader(stringResource(R.string.storage))
    LinearProgressIndicator(
        progress = { (cap.used.toDouble() / cap.total).toFloat().coerceIn(0f, 1f) },
        modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 6.dp),
    )
    Text(
        stringResource(R.string.storage_used, formatSize(cap.used), formatSize(cap.total)),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(horizontal = 20.dp),
    )
}

@Composable
private fun ActivityCard(state: FilesUiState, onOpenUploads: () -> Unit) {
    Surface(
        shape = RoundedCornerShape(12.dp),
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 4.dp),
    ) {
        Column(Modifier.padding(horizontal = 20.dp, vertical = 14.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            state.uploads.forEach { u ->
                ActivityRow(
                    title = if (u.total > 1) stringResource(R.string.uploading_n_of, u.index + 1, u.total) else stringResource(R.string.uploading),
                    detail = stringResource(R.string.upload_target, u.name.ifEmpty { stringResource(R.string.waiting) }, CrUri.displayName(u.parentUri)),
                    fraction = u.fraction,
                )
            }
            state.backgroundSyncs.forEach { b ->
                ActivityRow(
                    title = stringResource(R.string.background_progress, stringResource(b.kind.label), b.index + 1, b.total),
                    detail = b.name,
                    fraction = b.fraction,
                )
            }
        }
    }
}

@Composable
private fun ActivityRow(title: String, detail: String, fraction: Float?) {
    Column {
        Text(title, style = MaterialTheme.typography.bodyLarge)
        Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (fraction != null) {
            LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
        } else {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
        }
    }
}

@Composable
private fun SearchField(onSearch: () -> Unit) {
    Surface(
        onClick = onSearch,
        shape = RoundedCornerShape(28.dp),
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 10.dp)
            .height(48.dp),
    ) {
        Row(Modifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically) {
            Icon(
                Icons.Default.Search, contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(start = 18.dp, end = 12.dp),
            )
            Text(
                stringResource(R.string.search_files),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun SectionHeader(title: String, action: Pair<String, () -> Unit>? = null) {
    Row(
        Modifier
            .fillMaxWidth()
            .padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        if (action != null) {
            TextButton(onClick = action.second) { Text(action.first) }
        }
    }
}

