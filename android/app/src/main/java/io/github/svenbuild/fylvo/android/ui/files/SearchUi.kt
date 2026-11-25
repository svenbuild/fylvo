package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.SearchOff
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.outlined.FilterList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.asString

/** Search bar in the top-bar slot: plain text field, live results, clear button. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SearchTopBar(
    text: String,
    onTextChange: (String) -> Unit,
    onClose: () -> Unit,
    focus: FocusRequester,
) {
    TopAppBar(
        title = {
            val style = LocalTextStyle.current.merge(MaterialTheme.typography.bodyLarge)
            BasicTextField(
                value = text,
                onValueChange = onTextChange,
                singleLine = true,
                textStyle = style.copy(color = MaterialTheme.colorScheme.onSurface),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { }),
                modifier = Modifier
                    .fillMaxWidth()
                    .focusRequester(focus),
                decorationBox = { inner ->
                    Box {
                        if (text.isEmpty()) {
                            Text(stringResource(R.string.search_files), style = style, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        inner()
                    }
                },
            )
        },
        navigationIcon = {
            IconButton(onClick = onClose) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.close_search))
            }
        },
        actions = {
            if (text.isNotEmpty()) {
                IconButton(onClick = { onTextChange("") }) {
                    Icon(Icons.Default.Close, contentDescription = stringResource(R.string.clear))
                }
            }
        },
    )
}

/**
 * Results with scope and type chips above them. The scope chip pair only
 * appears when the search was opened from inside a folder.
 */
@Composable
fun SearchResults(
    state: FilesUiState,
    viewModel: FilesViewModel,
    listState: LazyListState,
    openedFrom: String?,
    actionsFor: (FileObject) -> FileActions,
    /** Fylvo: swipe actions per row. */
    swipeFor: (FileObject) -> RowSwipe? = { null },
    /** Fylvo 1.6: the server can search in contents; the account's tags for the filters. */
    fullTextAvailable: Boolean = false,
    tags: List<String> = emptyList(),
) {
    var filtersOpen by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            // Search in contents always covers the whole account: no folder choice then.
            if (openedFrom != null && !state.searchFullText) {
                FilterChip(
                    selected = state.searchScope != null,
                    onClick = { viewModel.setSearchScope(openedFrom) },
                    label = { Text(CrUri.displayName(openedFrom)) },
                )
                FilterChip(
                    selected = state.searchScope == null,
                    onClick = { viewModel.setSearchScope(null) },
                    label = { Text(stringResource(R.string.everywhere)) },
                )
                Box(Modifier.size(8.dp))
            }
            // Fylvo 1.6: search in contents (when the server has it), filters, then the kinds.
            if (fullTextAvailable) {
                FilterChip(
                    selected = state.searchFullText,
                    onClick = { viewModel.setSearchFullText(!state.searchFullText) },
                    label = { Text(stringResource(R.string.search_in_contents)) },
                )
            }
            if (!state.searchFullText) {
                FilterChip(
                    selected = state.searchFilters.any,
                    enabled = state.searchType == null,
                    onClick = { filtersOpen = true },
                    leadingIcon = { Icon(Icons.Outlined.FilterList, contentDescription = null, modifier = Modifier.size(18.dp)) },
                    label = {
                        Text(if (state.searchFilters.any) stringResource(R.string.search_filters_n, state.searchFilters.count) else stringResource(R.string.search_filters))
                    },
                )
                listOf(Category.IMAGES, Category.VIDEOS, Category.AUDIO, Category.DOCUMENTS).forEach { c ->
                    FilterChip(
                        selected = state.searchType == c,
                        onClick = { viewModel.setSearchType(if (state.searchType == c) null else c) },
                        label = { Text(stringResource(c.label)) },
                    )
                }
            }
        }
        if (state.searchType != null && state.searchFilters.any && !state.searchFullText) {
            Text(
                stringResource(R.string.search_kind_replaces_filters),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 2.dp),
            )
        }

        if (filtersOpen) {
            SearchFilterSheet(state.searchFilters, tags, onApply = viewModel::setSearchFilters, onDismiss = { filtersOpen = false })
        }
        val query = state.searchQuery.orEmpty().trim()
        // Filters or a kind alone are a search too (all images, all files over 100 MB, ...).
        val filtering = !state.searchFullText && (state.searchFilters.any || state.searchType != null)
        when {
            query.isEmpty() && !filtering -> Hint(Icons.Outlined.Search, stringResource(R.string.search_hint))
            state.searchLoading && state.searchResults.isEmpty() -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            state.searchError != null -> Hint(Icons.Outlined.SearchOff, state.searchError.asString())
            state.searchResults.isEmpty() -> Hint(Icons.Outlined.SearchOff, stringResource(R.string.no_files_match, query))
            else -> LazyColumn(state = listState, modifier = Modifier.fillMaxSize()) {
                items(state.searchResults, key = ::fileKey) { file ->
                    FileListItem(
                        file = file,
                        thumbUrl = rememberThumbUrl(viewModel, file),
                        selecting = false,
                        selected = false,
                        subtitle = state.searchSnippets[file.path]?.let { io.github.svenbuild.fylvo.android.util.SearchQuery.snippet(it).first.take(120) }
                            ?: "${fileSubtitle(file)} · ${CrUri.parentDisplayName(file.path)}",
                        actions = actionsFor(file),
                        edit = state.editOf(file),
                        swipe = swipeFor(file),
                    )
                }
                if (state.searchLoadingMore) {
                    item {
                        Row(Modifier.fillMaxWidth().padding(12.dp), horizontalArrangement = Arrangement.Center) {
                            CircularProgressIndicator(Modifier.size(28.dp))
                        }
                    }
                }
            }
        }
    }
}

/** Fylvo 1.6: the search filters (type, size, date, tags) as chips on a sheet. */
@OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
@Composable
private fun SearchFilterSheet(
    filters: io.github.svenbuild.fylvo.android.util.SearchFilters,
    tags: List<String>,
    onApply: (io.github.svenbuild.fylvo.android.util.SearchFilters) -> Unit,
    onDismiss: () -> Unit,
) {
    var f by remember { mutableStateOf(filters) }
    androidx.compose.material3.ModalBottomSheet(onDismissRequest = { onApply(f); onDismiss() }) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(bottom = 24.dp)) {
            FilterLabel(stringResource(R.string.search_filter_type))
            FilterRow {
                listOf(null to R.string.search_type_all, "file" to R.string.search_type_files, "folder" to R.string.search_type_folders).forEach { (v, label) ->
                    FilterChip(selected = f.type == v, onClick = { f = f.copy(type = v) }, label = { Text(stringResource(label)) })
                }
            }
            FilterLabel(stringResource(R.string.size))
            FilterRow {
                SIZE_RANGES.forEach { (range, label) ->
                    FilterChip(
                        selected = f.sizeMin == range.first && f.sizeMax == range.second,
                        onClick = { f = f.copy(sizeMin = range.first, sizeMax = range.second) },
                        label = { Text(stringResource(label)) },
                    )
                }
            }
            FilterLabel(stringResource(R.string.search_filter_modified))
            FilterRow {
                MODIFIED_DAYS.forEach { (days, label) ->
                    val after = days?.let { java.time.LocalDate.now().minusDays(it.toLong()).atStartOfDay(java.time.ZoneId.systemDefault()).toEpochSecond() }
                    FilterChip(
                        selected = (days == null && f.updatedAfter == null) || (days != null && f.updatedAfter == after),
                        onClick = { f = f.copy(updatedAfter = after) },
                        label = { Text(stringResource(label)) },
                    )
                }
            }
            if (tags.isNotEmpty()) {
                FilterLabel(stringResource(R.string.tags))
                FilterRow {
                    tags.forEach { tag ->
                        FilterChip(
                            selected = tag in f.tags,
                            onClick = { f = f.copy(tags = if (tag in f.tags) f.tags - tag else f.tags + tag) },
                            label = { Text(tag) },
                        )
                    }
                }
            }
            Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp), horizontalArrangement = Arrangement.End) {
                androidx.compose.material3.TextButton(onClick = { f = io.github.svenbuild.fylvo.android.util.SearchFilters() }) { Text(stringResource(R.string.search_filters_clear)) }
                androidx.compose.material3.Button(onClick = { onApply(f); onDismiss() }) { Text(stringResource(R.string.search_filters_apply)) }
            }
        }
    }
}

private const val MB = 1024L * 1024
/** Size ranges as (min, max) in bytes. */
private val SIZE_RANGES = listOf(
    (null to null) to R.string.search_any,
    (null to MB) to R.string.search_size_small,
    (MB to 100 * MB) to R.string.search_size_medium,
    (100 * MB to null) to R.string.search_size_large,
    (1024 * MB to null) to R.string.search_size_huge,
)
/** Changed within the last n days (from the start of that day); null = any time. */
private val MODIFIED_DAYS = listOf(null to R.string.search_any, 0 to R.string.search_today, 7 to R.string.search_7_days, 30 to R.string.search_30_days, 365 to R.string.search_year)

@Composable
private fun FilterLabel(text: String) {
    Text(text, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 16.dp, top = 12.dp, bottom = 6.dp))
}

@Composable
private fun FilterRow(content: @Composable () -> Unit) {
    Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) { content() }
}

@Composable
private fun Hint(icon: androidx.compose.ui.graphics.vector.ImageVector, text: String) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(32.dp)) {
            Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(56.dp))
            Text(text, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 12.dp))
        }
    }
}
