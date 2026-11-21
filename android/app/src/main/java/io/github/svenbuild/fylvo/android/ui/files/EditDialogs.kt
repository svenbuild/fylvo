package io.github.svenbuild.fylvo.android.ui.files

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.offline.EditEntry
import io.github.svenbuild.fylvo.android.data.offline.OfflineEdits
import java.io.File

/** Fylvo: what took a working copy. */
enum class Editor { EDIT, VIEW, NONE }

/**
 * Fylvo: hands a working copy to an editor with read and write access (ACTION_EDIT); when no app
 * edits it, to one that views it, with write access too.
 */
fun openForEdit(context: Context, local: File): Editor {
    val uri = OfflineEdits.uriOf(context, local)
    val type = context.contentResolver.getType(uri) ?: "*/*"
    for ((action, result) in listOf(Intent.ACTION_EDIT to Editor.EDIT, Intent.ACTION_VIEW to Editor.VIEW)) {
        val intent = Intent(action).setDataAndType(uri, type)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        try {
            context.startActivity(intent)
            return result
        } catch (_: ActivityNotFoundException) {
            // Next try.
        }
    }
    return Editor.NONE
}

/** Fylvo: why changes are not on the server yet, as a sentence. */
@StringRes
fun notUploadedText(reason: OfflineEdits.NotUploaded): Int = when (reason) {
    OfflineEdits.NotUploaded.Offline -> R.string.edit_reason_offline
    OfflineEdits.NotUploaded.Pending -> R.string.edit_reason_pending
    OfflineEdits.NotUploaded.Missing -> R.string.edit_reason_missing
    OfflineEdits.NotUploaded.Failed -> R.string.edit_reason_failed
}

/** Fylvo: [text], and when [unsent] that a stand not on the server goes with a discard. */
@Composable
private fun unsentNote(unsent: Boolean, text: String): String =
    if (unsent) text + " " + stringResource(R.string.edit_unsent_lost) else text

/** Fylvo: "Änderungen an X verwerfen?" before a working copy goes. */
@Composable
internal fun DiscardEditDialog(entry: EditEntry, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.edit_discard_title, entry.name)) },
        text = { Text(unsentNote(entry.pending != null, stringResource(R.string.edit_discard_text))) },
        confirmButton = {
            TextButton(onClick = onConfirm) { Text(stringResource(R.string.edit_discard), color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

/** Fylvo: "Noch nicht hochgeladen" after "Bearbeitung abschliessen": try later, discard, or cancel. */
@Composable
internal fun NotUploadedDialog(prompt: EditPrompt, onRetryLater: () -> Unit, onDiscard: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.edit_not_uploaded_title)) },
        text = {
            Text(unsentNote(prompt.unsent, stringResource(R.string.edit_not_uploaded_text, prompt.name, stringResource(notUploadedText(prompt.reason)))))
        },
        confirmButton = { TextButton(onClick = onRetryLater) { Text(stringResource(R.string.edit_retry_later)) } },
        dismissButton = {
            Row {
                TextButton(onClick = onDiscard) { Text(stringResource(R.string.edit_discard), color = MaterialTheme.colorScheme.error) }
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
            }
        },
    )
}

/**
 * Fylvo: edited files with changes not on the server, before a root is switched off or the
 * account signs out: "Erst hochladen", "Änderungen verwerfen" or cancel. [error] says why an
 * upload did not work; [busy] while it runs.
 */
@Composable
fun EditChoiceDialog(
    title: String,
    text: String,
    busy: Boolean,
    onUpload: () -> Unit,
    onDiscard: () -> Unit,
    onDismiss: () -> Unit,
    error: String? = null,
) {
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(title) },
        text = {
            Column {
                Text(text)
                if (error != null) Text(error, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(top = 8.dp))
                if (busy) Row(Modifier.padding(top = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(20.dp))
                    Text(stringResource(R.string.edit_uploading_now), modifier = Modifier.padding(start = 12.dp))
                }
            }
        },
        confirmButton = { TextButton(onClick = onUpload, enabled = !busy) { Text(stringResource(R.string.edit_upload_first)) } },
        dismissButton = {
            Row {
                TextButton(onClick = onDiscard, enabled = !busy) {
                    Text(stringResource(R.string.edit_discard), color = if (busy) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error)
                }
                TextButton(onClick = onDismiss, enabled = !busy) { Text(stringResource(R.string.cancel)) }
            }
        },
    )
}
