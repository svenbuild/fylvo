package io.github.svenbuild.fylvo.android.ui.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.Label
import androidx.compose.material.icons.outlined.StarOutline
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FavoritesList
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.TagInfo
import io.github.svenbuild.fylvo.android.util.CrUri

/** Fylvo: the page of a place that is not a folder listing (favourites, tags). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PlacePage(
    title: String,
    onBack: (() -> Unit)?,
    bottomBar: @Composable () -> Unit,
    content: @Composable (PaddingValues) -> Unit,
) {
    if (onBack != null) BackHandler(onBack = onBack)
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = {
                    if (onBack != null) IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.up))
                    }
                },
            )
        },
        bottomBar = bottomBar,
        content = content,
    )
}

/** Fylvo: the account's favourites, newest first (folders open, files open in place). */
@Composable
fun FavoritesPage(
    favorites: FavoritesList?,
    viewModel: FilesViewModel,
    /** Null when the page is a tab of its own. */
    onBack: (() -> Unit)?,
    actionsFor: (FileObject) -> FileActions,
    bottomBar: @Composable () -> Unit,
    /** Fylvo: swipe actions per row (only removing the star here). */
    swipeFor: (FileObject) -> RowSwipe? = { null },
    /** Fylvo: the list could not be loaded (no copy, no answer): a retry instead of a spinner. */
    loadFailed: Boolean = false,
    onRetry: () -> Unit = {},
) {
    PlacePage(stringResource(R.string.favorites), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when {
                favorites == null && loadFailed -> EmptyState(
                    icon = Icons.Outlined.StarOutline,
                    title = stringResource(R.string.customize_load_failed),
                    action = stringResource(R.string.retry) to onRetry,
                )
                favorites == null -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                favorites.files.isEmpty() && favorites.unavailable == 0 -> EmptyState(
                    icon = Icons.Outlined.StarOutline,
                    title = stringResource(R.string.no_favorites),
                    detail = stringResource(R.string.no_favorites_hint),
                )
                else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
                    if (favorites.unavailable > 0) item {
                        Text(
                            pluralStringResource(R.plurals.favorites_unavailable, favorites.unavailable, favorites.unavailable),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 20.dp, vertical = 6.dp),
                        )
                    }
                    items(favorites.files, key = { it.id }) { file ->
                        FileListItem(
                            file = file,
                            thumbUrl = rememberThumbUrl(viewModel, file),
                            selecting = false,
                            selected = false,
                            actions = actionsFor(file),
                            subtitle = "${fileSubtitle(file)} · ${CrUri.parentDisplayName(file.path)}",
                            swipe = swipeFor(file),
                        )
                    }
                }
            }
        }
    }
}

/** Fylvo: the account's tags with colour and count; a tap searches for the tag. */
@Composable
fun TagsPage(
    tags: List<TagInfo>,
    onBack: (() -> Unit)?,
    onOpen: (TagInfo) -> Unit,
    bottomBar: @Composable () -> Unit,
) {
    PlacePage(stringResource(R.string.tags), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            if (tags.isEmpty()) {
                EmptyState(
                    icon = Icons.AutoMirrored.Outlined.Label,
                    title = stringResource(R.string.no_tags),
                    detail = stringResource(R.string.no_tags_hint),
                )
            } else {
                LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
                    items(tags, key = { it.name }) { tag ->
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .clickable { onOpen(tag) }
                                .padding(horizontal = 20.dp, vertical = 14.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Box(Modifier.size(10.dp).background(colorOf(tag.color) ?: MaterialTheme.colorScheme.onSurfaceVariant, CircleShape))
                            Text(tag.name, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f).padding(start = 16.dp))
                            Text("${tag.count}", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
        }
    }
}
