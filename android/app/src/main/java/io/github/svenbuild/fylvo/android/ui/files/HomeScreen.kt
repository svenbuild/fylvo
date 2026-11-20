package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.material.icons.filled.MoreVert
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
                    offline = file.path in state.favoritePaths,
                    actions = FileActions(onOpen = { onOpenFile(file) }, onLongPress = { onFileMore(file) }, onMore = { onFileMore(file) }),
                    subtitle = "${subtitleFor(file.size, file.updatedAt, file.isFolder)} · ${CrUri.parentDisplayName(file.path)}",
                )
            }
        }
    }
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

