package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.outlined.History
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.EntityDto
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.HistoryRepository
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.launch

/**
 * Fylvo 1.6: the stored versions of an own file (design/android-parity-plan.md, 6), newest first.
 * An older one can be downloaded, made the current one again, or deleted.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VersionsSheet(
    file: FileObject,
    onDownload: (FileObject, entity: String) -> Unit,
    onChanged: () -> Unit,
    onMessage: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    var now by remember { mutableStateOf<FileObject?>(null) }
    var failed by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }
    var picked by remember { mutableStateOf<EntityDto?>(null) }
    var confirm by remember { mutableStateOf<Pair<EntityDto, Boolean>?>(null) }
    var busy by remember { mutableStateOf(false) }
    LaunchedEffect(reload) {
        runCatching { app.history.details(file.path) }
            .onSuccess { now = it; failed = false }
            .onFailure { failed = true }
    }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.verticalScroll(rememberScrollState())) {
            Text(stringResource(R.string.versions), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp))
            Text(file.name, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1,
                overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(horizontal = 20.dp))
            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            val current = now
            when {
                current == null && failed -> Text(stringResource(R.string.versions_load_failed), modifier = Modifier.padding(20.dp))
                current == null -> Box(Modifier.fillMaxWidth().padding(24.dp)) { CircularProgressIndicator(Modifier.align(Alignment.Center)) }
                else -> HistoryRepository.versions(current).forEach { v ->
                    val isCurrent = v.id == current.primaryEntity
                    Row(
                        Modifier.fillMaxWidth().clickable { picked = v }.padding(horizontal = 20.dp, vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column(Modifier.weight(1f)) {
                            Text(ActivityText.time(v.createdAt), style = MaterialTheme.typography.bodyLarge)
                            Text(formatSize(v.size), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        if (isCurrent) Text(stringResource(R.string.version_current), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                    }
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }

    picked?.let { v ->
        val isCurrent = v.id == now?.primaryEntity
        ModalBottomSheet(onDismissRequest = { picked = null }) {
            Text(ActivityText.time(v.createdAt), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
            SheetAction(Icons.Default.Download, stringResource(R.string.download), { picked = null; onDownload(file, v.id) })
            if (!isCurrent) {
                SheetAction(Icons.Outlined.History, stringResource(R.string.version_restore), { picked = null; confirm = v to true })
                SheetAction(Icons.Default.Delete, stringResource(R.string.version_delete), { picked = null; confirm = v to false }, tint = MaterialTheme.colorScheme.error)
            }
            Spacer(Modifier.height(24.dp))
        }
    }

    confirm?.let { (v, restore) ->
        AlertDialog(
            onDismissRequest = { confirm = null },
            title = { Text(stringResource(if (restore) R.string.version_restore_title else R.string.version_delete_title)) },
            text = { Text(stringResource(if (restore) R.string.version_restore_text else R.string.version_delete_text, ActivityText.time(v.createdAt))) },
            confirmButton = {
                TextButton(enabled = !busy, onClick = {
                    busy = true
                    scope.launch {
                        runCatching { if (restore) app.history.makeCurrent(file.path, v.id) else app.history.deleteVersion(file.path, v.id) }
                            .onSuccess {
                                onMessage(context.getString(if (restore) R.string.version_restored else R.string.version_deleted))
                                onChanged()
                                reload++
                            }
                            .onFailure { onMessage(userError(it).resolve(context)) }
                        busy = false
                        confirm = null
                    }
                }) {
                    Text(stringResource(if (restore) R.string.version_restore else R.string.delete),
                        color = if (restore) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = { TextButton(onClick = { confirm = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}
