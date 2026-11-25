package io.github.svenbuild.fylvo.android.ui.files.transfers

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.CloudUpload
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.ui.files.formatSize
import io.github.svenbuild.fylvo.android.work.UploadStatus
import java.util.UUID

/**
 * Inline transfer status shown above the files of the destination folder,
 * one row per batch: what is uploading, how far along, and a cancel button.
 */
@Composable
fun UploadsCard(uploads: List<UploadStatus>, onCancel: (UUID) -> Unit) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 6.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh),
    ) {
        uploads.forEach { u -> UploadRow(u, onCancel) }
    }
}

/** What an upload is doing right now, also used by the activity card of the home page. */
@Composable
fun uploadHeadline(u: UploadStatus): String = when {
    u.waitingForNetwork -> stringResource(R.string.waiting_for_connection)
    u.total == 0 -> stringResource(R.string.waiting_to_upload)
    u.total > 1 -> stringResource(R.string.uploading_n_of, u.index + 1, u.total)
    else -> stringResource(R.string.uploading)
}

@Composable
private fun UploadRow(u: UploadStatus, onCancel: (UUID) -> Unit) {
    val headline = uploadHeadline(u)
    val detail = when {
        u.total == 0 -> ""
        u.size > 0 && u.permille == 0 -> stringResource(R.string.upload_progress, u.name, formatSize(u.sent), formatSize(u.size))
        else -> u.name
    }
    Row(
        Modifier
            .fillMaxWidth()
            .padding(start = 16.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            Icons.Outlined.CloudUpload,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary,
            modifier = Modifier.size(28.dp),
        )
        Column(
            Modifier
                .weight(1f)
                .padding(horizontal = 16.dp),
        ) {
            Text(headline, style = MaterialTheme.typography.bodyLarge)
            if (detail.isNotEmpty()) {
                Text(
                    detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            val fraction = u.fraction
            if (fraction != null) {
                LinearProgressIndicator(
                    progress = { fraction },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 8.dp),
                )
            } else {
                LinearProgressIndicator(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 8.dp),
                )
            }
        }
        IconButton(onClick = { onCancel(u.id) }) {
            Icon(Icons.Default.Close, contentDescription = stringResource(R.string.cancel_upload))
        }
    }
}
