package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.util.CrUri
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import io.github.svenbuild.fylvo.android.util.Connected

/** Read-only facts about a file, laid out as label/value rows like the Files app's Info. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DetailsSheet(file: FileObject, thumbUrl: String?, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 20.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FileThumb(file, thumbUrl, size = 56.dp)
            Column(Modifier.padding(start = 16.dp)) {
                Text(file.name, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                Text(kindLabel(file), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        HorizontalDivider(Modifier.padding(vertical = 8.dp))

        SelectionContainer {
            Column {
                if (!file.isFolder && !Connected.sizeUnknown(file)) {
                    DetailRow(stringResource(R.string.size), stringResource(R.string.size_with_bytes, formatSize(file.size), "%,d".format(file.size)))
                }
                DetailRow(stringResource(R.string.location), CrUri.displayPath(CrUri.parent(file.path)))
                DetailRow(stringResource(R.string.modified), longDate(file.updatedAt))
                // Fylvo: connected storages report no creation date (the server repeats the modification).
                if (!Connected.isConnected(file.path)) DetailRow(stringResource(R.string.created), longDate(file.createdAt))
                val w = file.metadata?.get("exif:x")
                val h = file.metadata?.get("exif:y")
                if (w != null && h != null) DetailRow(stringResource(R.string.dimensions), "$w × $h")
                file.metadata?.get("stream:duration")?.let { DetailRow(stringResource(R.string.duration), it) }
                if (file.shared == true) DetailRow(stringResource(R.string.sharing), stringResource(R.string.shared_via_link))
            }
        }
        Spacer(Modifier.height(24.dp))
    }
}

@Composable
private fun DetailRow(label: String, value: String) {
    Row(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 20.dp, vertical = 10.dp),
    ) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.width(110.dp),
        )
        Text(value, style = MaterialTheme.typography.bodyMedium)
    }
}

/** "JPG image", or just "Image" without an extension; PDFs are always "PDF document". */
@Composable
private fun kindLabel(file: FileObject): String {
    if (file.isFolder) return stringResource(R.string.folder)
    val ext = file.name.substringAfterLast('.', "").uppercase()
    val (plain, withExt) = when (FileKind.of(file)) {
        FileKind.IMAGE -> R.string.kind_image to R.string.kind_image_ext
        FileKind.VIDEO -> R.string.kind_video to R.string.kind_video_ext
        FileKind.AUDIO -> R.string.kind_audio to R.string.kind_audio_ext
        FileKind.PDF -> R.string.kind_pdf to null
        FileKind.DOCUMENT -> R.string.kind_document to R.string.kind_document_ext
        FileKind.SPREADSHEET -> R.string.kind_spreadsheet to R.string.kind_spreadsheet_ext
        FileKind.PRESENTATION -> R.string.kind_presentation to R.string.kind_presentation_ext
        FileKind.ARCHIVE -> R.string.kind_archive to R.string.kind_archive_ext
        FileKind.CODE -> R.string.kind_code to R.string.kind_code_ext
        FileKind.APK -> R.string.kind_apk to R.string.kind_apk_ext
        else -> R.string.kind_file to R.string.kind_file_ext
    }
    return if (ext.isEmpty() || withExt == null) stringResource(plain) else stringResource(withExt, ext)
}

private fun longDate(iso: String): String = runCatching {
    OffsetDateTime.parse(iso).atZoneSameInstant(java.time.ZoneId.systemDefault())
        .format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT))
}.getOrDefault(iso.ifEmpty { "–" })
