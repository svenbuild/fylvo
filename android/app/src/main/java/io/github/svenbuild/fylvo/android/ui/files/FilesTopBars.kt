package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Sort
import androidx.compose.material.icons.filled.ArrowDownward
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.GridView
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.automirrored.filled.ViewList
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.SortBy
import io.github.svenbuild.fylvo.android.data.ViewMode
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue

// ---- top bars -------------------------------------------------------------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun FolderTopBar(
    title: String,
    subtitle: String?,
    showBack: Boolean,
    showSort: Boolean,
    viewMode: ViewMode,
    sort: io.github.svenbuild.fylvo.android.data.SortOrder,
    onBack: () -> Unit,
    onSearch: () -> Unit,
    onToggleView: () -> Unit,
    onSort: (SortBy) -> Unit,
    onRefresh: () -> Unit,
    onTrash: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    var sortOpen by remember { mutableStateOf(false) }
    TopAppBar(
        title = {
            Column {
                Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (subtitle != null) {
                    Text(
                        subtitle,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        },
        navigationIcon = {
            if (showBack) {
                IconButton(onClick = onBack) {
                    Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.up))
                }
            }
        },
        actions = {
            IconButton(onClick = onSearch) {
                Icon(Icons.Default.Search, contentDescription = stringResource(R.string.search))
            }
            Box {
                IconButton(onClick = { menuOpen = true }) {
                    Icon(Icons.Default.MoreVert, contentDescription = stringResource(R.string.more_options))
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    if (showSort) DropdownMenuItem(
                        text = { Text(stringResource(R.string.sort_by)) },
                        leadingIcon = { Icon(Icons.AutoMirrored.Filled.Sort, null) },
                        onClick = { menuOpen = false; sortOpen = true },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(if (viewMode == ViewMode.LIST) R.string.grid_view else R.string.list_view)) },
                        leadingIcon = {
                            Icon(if (viewMode == ViewMode.LIST) Icons.Default.GridView else Icons.AutoMirrored.Filled.ViewList, null)
                        },
                        onClick = { menuOpen = false; onToggleView() },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.refresh)) },
                        leadingIcon = { Icon(Icons.Default.Refresh, null) },
                        onClick = { menuOpen = false; onRefresh() },
                    )
                    HorizontalDivider()
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.trash)) },
                        leadingIcon = { Icon(Icons.Default.Delete, null) },
                        onClick = { menuOpen = false; onTrash() },
                    )
                }
                DropdownMenu(expanded = sortOpen, onDismissRequest = { sortOpen = false }) {
                    SortBy.entries.forEach { by ->
                        val active = sort.by == by
                        DropdownMenuItem(
                            text = {
                                Text(
                                    stringResource(
                                        when (by) {
                                            SortBy.NAME -> R.string.sort_name
                                            SortBy.SIZE -> R.string.size
                                            SortBy.UPDATED -> R.string.sort_modified
                                            SortBy.CREATED -> R.string.sort_created
                                        },
                                    ),
                                )
                            },
                            leadingIcon = {
                                if (active) Icon(Icons.Default.Check, null, tint = MaterialTheme.colorScheme.primary)
                                else Box(Modifier.size(24.dp))
                            },
                            trailingIcon = {
                                if (active) Icon(
                                    if (sort.ascending) Icons.Default.ArrowUpward else Icons.Default.ArrowDownward,
                                    contentDescription = stringResource(if (sort.ascending) R.string.ascending else R.string.descending),
                                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.size(18.dp),
                                )
                            },
                            onClick = { sortOpen = false; onSort(by) },
                        )
                    }
                }
            }
        },
    )
}

/**
 * Fylvo: information only while selecting ("3 selected" and a second line); the actions sit on
 * the selection button, back leaves the selection.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SelectionTopBar(count: Int, detail: String?) {
    TopAppBar(
        title = {
            Column {
                Text(pluralStringResource(R.plurals.n_selected, count, count), maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (detail != null) {
                    Text(
                        detail,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        },
    )
}

/**
 * Fylvo: the info bar's second line. One file "name · size", one folder "name · Folder";
 * several: the size of the selected files together (folders not counted), none when that is 0.
 */
internal fun selectionDetail(selected: List<FileObject>, folderLabel: String): String? {
    val one = selected.singleOrNull()
    return when {
        one != null -> "${one.name} · ${if (one.isFolder) folderLabel else formatSize(one.size)}"
        else -> selected.filter { !it.isFolder }.sumOf { it.size }.takeIf { it > 0 }?.let(::formatSize)
    }
}
