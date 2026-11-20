package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material.icons.outlined.OfflinePin
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.offline.OfflineRoot
import io.github.svenbuild.fylvo.android.data.offline.OfflineRootInfo
import io.github.svenbuild.fylvo.android.data.offline.OfflineState
import java.text.DateFormat
import java.util.Date

/**
 * Fylvo: the folders and files kept on this phone, with size, last sync and state. Tapping a
 * folder opens it in the Files tab (also without a connection); the menu removes it from the phone.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun OfflineScreen(
    roots: List<OfflineRootInfo>,
    syncing: String?,
    onBack: () -> Unit,
    onOpen: (OfflineRoot) -> Unit,
    onSyncNow: () -> Unit,
    onRemove: (OfflineRoot) -> Unit,
    bottomBar: @Composable () -> Unit,
) {
    BackHandler(onBack = onBack)
    val app = LocalContext.current.applicationContext as FylvoApp
    val mobileData by app.offlineManager.mobileData.collectAsState(initial = false)
    Surface(Modifier.fillMaxSize()) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = {
                        Column {
                            Text(stringResource(R.string.offline_files))
                            val files = roots.sumOf { it.files }
                            Text(
                                if (roots.isEmpty()) stringResource(R.string.nothing_saved_yet)
                                else pluralStringResource(R.plurals.offline_summary, files, files, formatSize(roots.sumOf { it.bytes })),
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    },
                    navigationIcon = {
                        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
                    },
                    actions = {
                        if (roots.isNotEmpty()) {
                            IconButton(onClick = onSyncNow) { Icon(Icons.Default.Sync, contentDescription = stringResource(R.string.sync_now)) }
                        }
                    },
                )
            },
            bottomBar = bottomBar,
        ) { padding ->
            if (roots.isEmpty()) {
                Box(Modifier.fillMaxSize().padding(padding)) {
                    EmptyState(
                        icon = Icons.Outlined.OfflinePin,
                        title = stringResource(R.string.no_offline_files),
                        detail = stringResource(R.string.no_offline_files_hint),
                    )
                }
            } else {
                LazyColumn(Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(bottom = 24.dp)) {
                    items(roots, key = { it.root.id }) { info ->
                        OfflineRow(info, syncing == info.root.id, mobileData, { onOpen(info.root) }, onSyncNow, { onRemove(info.root) })
                    }
                }
            }
        }
    }
}

@Composable
private fun OfflineRow(
    info: OfflineRootInfo,
    syncing: Boolean,
    mobileData: Boolean,
    onOpen: () -> Unit,
    onSyncNow: () -> Unit,
    onRemove: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    val root = info.root
    val kind = FileKind.of(FileObject(name = root.name))
    val state = when {
        syncing -> stringResource(R.string.offline_state_syncing)
        root.state == OfflineState.Synced -> stringResource(R.string.offline_state_synced)
        root.state == OfflineState.Pending -> stringResource(if (mobileData) R.string.offline_state_waiting else R.string.offline_state_waiting_wifi)
        root.state == OfflineState.Error -> stringResource(R.string.offline_state_error)
        root.state == OfflineState.Gone -> stringResource(R.string.offline_state_gone)
        else -> stringResource(R.string.offline_state_storage_full)
    }
    val problem = !syncing && root.state in setOf(OfflineState.Error, OfflineState.Gone, OfflineState.StorageFull)
    Row(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 8.dp, vertical = 2.dp)
            .clip(RoundedCornerShape(12.dp))
            .clickable(onClick = onOpen)
            .padding(start = 12.dp, top = 8.dp, bottom = 8.dp, end = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier
                .size(48.dp)
                .clip(RoundedCornerShape(12.dp))
                .background(MaterialTheme.colorScheme.surfaceContainer),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                if (root.folder) Icons.Default.Folder else kind.icon, contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(24.dp),
            )
        }
        Column(Modifier.weight(1f).padding(horizontal = 16.dp)) {
            Text(root.name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                listOfNotNull(
                    if (root.folder) pluralStringResource(R.plurals.offline_root_files, info.files, info.files, formatSize(info.bytes))
                    else formatSize(info.bytes),
                    state,
                ).joinToString(" • "),
                style = MaterialTheme.typography.bodyMedium,
                color = if (problem) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (root.lastSyncAt > 0) {
                Text(
                    stringResource(R.string.offline_last_sync, DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT).format(Date(root.lastSyncAt))),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                )
            }
        }
        Box {
            IconButton(onClick = { menuOpen = true }) {
                Icon(Icons.Default.MoreVert, contentDescription = stringResource(R.string.more), tint = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.sync_now)) },
                    leadingIcon = { Icon(Icons.Default.Sync, null) },
                    onClick = { menuOpen = false; onSyncNow() },
                )
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.remove_from_device)) },
                    leadingIcon = { Icon(Icons.Default.Delete, null, tint = MaterialTheme.colorScheme.error) },
                    onClick = { menuOpen = false; onRemove() },
                )
            }
        }
    }
}

/** Fylvo: above a folder shown from the copies on this phone. */
@Composable
internal fun OfflineBanner() {
    Row(
        Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceContainerHigh)
            .padding(horizontal = 20.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Default.CloudOff, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(16.dp))
        Text(
            stringResource(R.string.offline_banner),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(start = 8.dp),
        )
    }
}

/** Fylvo: size and free space before a folder is kept offline; refuses when too little would stay free. */
@Composable
internal fun OfflinePromptDialog(prompt: OfflinePrompt, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.offline_prompt_title, prompt.name)) },
        text = {
            when {
                prompt.loading -> Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(20.dp))
                    Text(stringResource(R.string.offline_counting), modifier = Modifier.padding(start = 12.dp))
                }
                prompt.refusal == OfflinePrompt.Refusal.TooManyFolders -> Text(stringResource(R.string.offline_too_many_folders))
                else -> Column {
                    Text(pluralStringResource(R.plurals.offline_root_files, prompt.files, prompt.files, formatSize(prompt.bytes)))
                    Text(stringResource(R.string.offline_free_space, formatSize(prompt.freeBytes)))
                    if (prompt.refusal == OfflinePrompt.Refusal.NoRoom) {
                        Text(
                            stringResource(R.string.offline_no_room),
                            color = MaterialTheme.colorScheme.error,
                            modifier = Modifier.padding(top = 8.dp),
                        )
                    }
                }
            }
        },
        confirmButton = {
            if (!prompt.loading && prompt.refusal == null) {
                TextButton(onClick = onConfirm) { Text(stringResource(R.string.keep_offline)) }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text(stringResource(if (!prompt.loading && prompt.refusal != null) R.string.ok else R.string.cancel))
            }
        },
    )
}
