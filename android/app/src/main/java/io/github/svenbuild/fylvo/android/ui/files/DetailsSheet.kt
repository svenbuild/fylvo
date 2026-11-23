package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.runtime.getValue
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

/**
 * Read-only facts about a file, laid out as label/value rows like the Files app's Info.
 * Fylvo 1.6: also camera, place, the storage a folder uses, its shares and its history
 * (design/android-parity-plan.md, 7). [mapEnabled]: the server has a map provider; only then a
 * map app is offered for the place. [onShares]: opens the list of the file's shares.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DetailsSheet(
    file: FileObject,
    thumbUrl: String?,
    onDismiss: () -> Unit,
    mapEnabled: Boolean? = null,
    onShares: (() -> Unit)? = null,
) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as io.github.svenbuild.fylvo.android.FylvoApp
    val context = androidx.compose.ui.platform.LocalContext.current
    // Own files in My files and the trash have extended details and a history.
    val own = file.owned == true && (file.path.startsWith(CrUri.MY_PREFIX) || file.path.startsWith("cloudreve://trash")) &&
        !Connected.isConnected(file.path)
    val extended by androidx.compose.runtime.produceState<FileObject?>(null, file.path) {
        if (own) value = runCatching { app.history.details(file.path) }.getOrNull()
    }
    val history by androidx.compose.runtime.produceState<List<io.github.svenbuild.fylvo.android.api.ActivityEntry>?>(null, file.path) {
        if (own) value = runCatching { app.history.activity(file.path).entries.take(10) }.getOrNull()
    }
    val meta = extended?.metadata ?: file.metadata
    ModalBottomSheet(onDismissRequest = onDismiss) {
      Column(Modifier.verticalScroll(rememberScrollState())) {
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
                // Fylvo: connected storages may not know the time (0001-01-01).
                if (Connected.knownTime(file.updatedAt)) DetailRow(stringResource(R.string.modified), longDate(file.updatedAt))
                // Fylvo: connected storages report no creation date (the server repeats the modification).
                if (!Connected.isConnected(file.path)) DetailRow(stringResource(R.string.created), longDate(file.createdAt))
                val w = file.metadata?.get("exif:x")
                val h = file.metadata?.get("exif:y")
                if (w != null && h != null) DetailRow(stringResource(R.string.dimensions), "$w × $h")
                file.metadata?.get("stream:duration")?.let { DetailRow(stringResource(R.string.duration), it) }
                if (file.shared == true && onShares == null) DetailRow(stringResource(R.string.sharing), stringResource(R.string.shared_via_link))
                // Fylvo 1.6: what a folder takes up in storage.
                extended?.extendedInfo?.storageUsed?.takeIf { file.isFolder && it > 0 }?.let { DetailRow(stringResource(R.string.storage_used_label), formatSize(it)) }
            }
        }
        // Fylvo 1.6: the file's shares.
        val shareCount = extended?.extendedInfo?.shares?.size ?: 0
        if (onShares != null && file.shared == true) {
            androidx.compose.material3.ListItem(
                modifier = Modifier.clickable(onClick = onShares),
                headlineContent = { Text(stringResource(R.string.manage_shares)) },
                supportingContent = { if (shareCount > 0) Text(androidx.compose.ui.res.pluralStringResource(R.plurals.share_count, shareCount, shareCount)) },
                colors = androidx.compose.material3.ListItemDefaults.colors(containerColor = androidx.compose.ui.graphics.Color.Transparent),
            )
        }
        CameraSection(meta)
        PlaceSection(meta, mapEnabled == true) { lat, lng ->
            val uri = android.net.Uri.parse("geo:$lat,$lng?q=$lat,$lng")
            runCatching { context.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW, uri)) }
        }
        history?.takeIf { it.isNotEmpty() }?.let { entries ->
            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            SectionTitle(stringResource(R.string.history))
            entries.forEach { ActivityRow(it) }
        }
        Spacer(Modifier.height(24.dp))
      }
    }
}

/** Fylvo 1.6: camera facts (EXIF) when the server read them. */
@Composable
private fun CameraSection(meta: Map<String, String>?) {
    if (meta == null) return
    val camera = listOfNotNull(meta["exif:camera_make"], meta["exif:camera_model"]).joinToString(" ").trim()
    val rows = listOfNotNull(
        camera.takeIf { it.isNotEmpty() }?.let { R.string.exif_camera to it },
        meta["exif:lens_model"]?.let { R.string.exif_lens to it },
        meta["exif:f"]?.let { R.string.exif_aperture to "f/" + trimNumber(it) },
        meta["exif:exposure_time"]?.let { R.string.exif_exposure to exposure(it) },
        meta["exif:iso"]?.let { R.string.exif_iso to it },
        meta["exif:focal_length"]?.let { R.string.exif_focal to trimNumber(it) + " mm" },
        // The camera's clock has no time zone: shown as written, not converted.
        meta["exif:taken_at"]?.let { R.string.exif_taken to cameraTime(it) },
    )
    if (rows.isEmpty()) return
    HorizontalDivider(Modifier.padding(vertical = 8.dp))
    SectionTitle(stringResource(R.string.exif_title))
    SelectionContainer { Column { rows.forEach { (label, value) -> DetailRow(stringResource(label), value) } } }
}

/** Fylvo 1.6: where a photo was taken; a map app only when the server has a map provider. */
@Composable
private fun PlaceSection(meta: Map<String, String>?, mapEnabled: Boolean, onMap: (String, String) -> Unit) {
    if (meta == null) return
    val place = listOf("geocoding:place", "geocoding:street", "geocoding:locality", "geocoding:district", "geocoding:region", "geocoding:country")
        .mapNotNull { meta[it]?.takeIf { v -> v.isNotBlank() } }.distinct().joinToString(", ")
    val lat = meta["exif:latitude"]
    val lng = meta["exif:longitude"]
    if (place.isEmpty() && (lat == null || lng == null)) return
    HorizontalDivider(Modifier.padding(vertical = 8.dp))
    SectionTitle(stringResource(R.string.exif_place))
    SelectionContainer {
        Column {
            if (place.isNotEmpty()) DetailRow(stringResource(R.string.exif_place), place)
            if (lat != null && lng != null) DetailRow(stringResource(R.string.exif_coordinates), "$lat, $lng")
        }
    }
    if (mapEnabled && lat != null && lng != null) {
        androidx.compose.material3.TextButton(onClick = { onMap(lat, lng) }, modifier = Modifier.padding(horizontal = 12.dp)) {
            Text(stringResource(R.string.open_in_map_app))
        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(horizontal = 20.dp, vertical = 6.dp))
}

/** "1/250 s" for "1/250" or 0.004, "2 s" for 2. */
internal fun exposure(value: String): String {
    if ('/' in value) return "$value s"
    val v = value.toDoubleOrNull() ?: return value
    if (v <= 0) return value
    return if (v >= 1) "${trimNumber(value)} s" else "1/${Math.round(1 / v)} s"
}

/** The time of a photo as the camera wrote it. */
internal fun cameraTime(iso: String): String = runCatching {
    OffsetDateTime.parse(iso).toLocalDateTime().format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT))
}.getOrDefault(iso)

/** "2.8" for "2.800000", "35" for "35.0". */
internal fun trimNumber(value: String): String {
    val v = value.toDoubleOrNull() ?: return value
    return java.math.BigDecimal(v).setScale(2, java.math.RoundingMode.HALF_UP).stripTrailingZeros().toPlainString()
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
