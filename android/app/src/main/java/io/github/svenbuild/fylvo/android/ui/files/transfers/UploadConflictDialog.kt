package io.github.svenbuild.fylvo.android.ui.files.transfers

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import android.net.Uri
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.work.UploadWorker
import io.github.svenbuild.fylvo.android.util.Connected

/** A picked batch with its resolved target names, waiting for a conflict decision. */
data class PendingUpload(
    val uris: List<Uri>,
    val names: List<String>,
    val parentUri: String,
    val existing: Set<String>,
) {
    val conflicts: List<String> get() = names.filter { it in existing }

    /** Only the files the dialog listed replace a file; any other name never does. */
    fun replace() = Resolution(uris, names, overwrite = true, overwriteOnly = conflicts.toSet())

    fun keepBoth(): Resolution {
        val taken = existing.toMutableSet()
        val renamed = names.map { n -> UploadWorker.uniqueName(n, taken).also { taken += it } }
        return Resolution(uris, renamed, overwrite = false)
    }

    fun skip(): Resolution {
        val keep = names.indices.filter { names[it] !in existing }
        return Resolution(keep.map { uris[it] }, keep.map { names[it] }, overwrite = false)
    }

    data class Resolution(val uris: List<Uri>, val names: List<String>, val overwrite: Boolean, val overwriteOnly: Set<String>? = null)
}

/**
 * Asked once per batch before anything is queued, like Drive: the user
 * decides how every clashing name is handled instead of discovering a
 * failure in the notification afterwards.
 */
@Composable
fun UploadConflictDialog(
    pending: PendingUpload,
    onResolve: (PendingUpload.Resolution) -> Unit,
    onDismiss: () -> Unit,
) {
    val conflicts = pending.conflicts
    val connected = Connected.isConnected(pending.parentUri)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                if (conflicts.size == 1) stringResource(R.string.already_exists_one, conflicts.first())
                else stringResource(R.string.already_exist_many, conflicts.size),
            )
        },
        text = {
            Column {
                Text(stringResource(if (connected) R.string.upload_conflict_note_connected else R.string.upload_conflict_note))
                if (conflicts.size > 1) {
                    Text(
                        conflicts.take(5).joinToString("\n") + if (conflicts.size > 5) "\n…" else "",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 6,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(top = 12.dp),
                    )
                }
            }
        },
        // Fylvo: no replacing in connected storages in this version (keep both or skip).
        confirmButton = {
            if (connected) TextButton(onClick = { onResolve(pending.keepBoth()) }) { Text(stringResource(R.string.keep_both)) }
            else TextButton(onClick = { onResolve(pending.replace()) }) { Text(stringResource(R.string.replace)) }
        },
        dismissButton = {
            if (!connected) TextButton(onClick = { onResolve(pending.keepBoth()) }) { Text(stringResource(R.string.keep_both)) }
            TextButton(onClick = { onResolve(pending.skip()) }) { Text(stringResource(R.string.skip)) }
        },
    )
}
