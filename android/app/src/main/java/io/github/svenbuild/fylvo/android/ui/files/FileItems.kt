package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.PlayCircle
import androidx.compose.material.icons.outlined.CheckBoxOutlineBlank
import androidx.compose.material.icons.outlined.CloudUpload
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onLongClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.platform.LocalContext
import coil.compose.SubcomposeAsyncImage
import coil.request.ImageRequest
import io.github.svenbuild.fylvo.android.api.FileObject

/**
 * Gestures a row or tile responds to. Fylvo: no menu button on rows any more; a long press
 * selects (folders) or opens the file's sheet (home, search).
 */
data class FileActions(
    val onOpen: () -> Unit,
    val onLongPress: () -> Unit,
    /** What TalkBack calls the long press ("Select", "Deselect", "Options"). */
    val longPressLabel: String,
    /**
     * True: the row itself detects the long press (home, search). False: the list detects it so
     * a drag can select several (see dragSelect); the row offers it to accessibility only.
     */
    val longPressOnRow: Boolean,
)

/** Fylvo: tap and long press of a row or tile, and its selected state for TalkBack. */
@OptIn(ExperimentalFoundationApi::class)
private fun Modifier.fileClicks(actions: FileActions, selectionState: String?): Modifier =
    if (actions.longPressOnRow) {
        combinedClickable(onClick = actions.onOpen, onLongClick = actions.onLongPress, onLongClickLabel = actions.longPressLabel)
    } else {
        clickable(onClick = actions.onOpen).semantics {
            onLongClick(label = actions.longPressLabel) { actions.onLongPress(); true }
            if (selectionState != null) stateDescription = selectionState
        }
    }

/** Fylvo: "Selected" / "Not selected" while selecting, read by TalkBack with the row. */
@Composable
private fun selectionState(selecting: Boolean, selected: Boolean): String? = when {
    !selecting -> null
    selected -> stringResource(R.string.state_selected)
    else -> stringResource(R.string.state_not_selected)
}

@Composable
fun rememberThumbUrl(viewModel: FilesViewModel, file: FileObject): String? {
    // Signed URLs expire; re-key on a coarse time bucket so any recomposition
    // past the TTL refetches instead of sticking for the VM's lifetime.
    val bucket = System.currentTimeMillis() / THUMB_TTL_MS
    val url by produceState<String?>(initialValue = null, file.id, bucket) {
        value = viewModel.thumbUrl(file)
    }
    return url
}

/**
 * Thumbnail when the server has one, otherwise the file-kind icon on a soft
 * tinted tile. Same shape in both cases so list rows stay aligned.
 */
@Composable
fun FileThumb(
    file: FileObject,
    thumbUrl: String?,
    /** Fixed square size, or null to fill the parent (icon then sized by [iconSize]). */
    size: Dp?,
    modifier: Modifier = Modifier,
    corner: Dp = 12.dp,
    iconSize: Dp = (size ?: 48.dp) / 2,
    /** Show a progress ring over the tile (determinate when a fraction is known). */
    downloading: Boolean = false,
    downloadProgress: Float? = null,
    /** Fylvo: list rows show the bare icon; only a real thumbnail gets a rounded frame. */
    plain: Boolean = false,
) {
    val shape = RoundedCornerShape(corner)
    Box(
        modifier
            .then(if (size != null) Modifier.size(size) else Modifier)
            .clip(shape)
            .background(if (plain && thumbUrl == null) Color.Transparent else MaterialTheme.colorScheme.surfaceContainer),
        contentAlignment = Alignment.Center,
    ) {
        if (thumbUrl != null) {
            SubcomposeAsyncImage(
                model = ImageRequest.Builder(LocalContext.current).data(thumbUrl).crossfade(180).build(),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
                loading = { KindIcon(file, iconSize) },
                error = { KindIcon(file, iconSize) },
            )
        } else {
            KindIcon(file, iconSize, uploading = file.isUploading)
        }
        if (downloading) {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(MaterialTheme.colorScheme.scrim.copy(alpha = 0.45f)),
                contentAlignment = Alignment.Center,
            ) {
                val ring = Modifier.size(iconSize * 1.2f)
                if (downloadProgress != null) {
                    CircularProgressIndicator(progress = { downloadProgress }, modifier = ring, strokeWidth = 3.dp, color = Color.White)
                } else {
                    CircularProgressIndicator(modifier = ring, strokeWidth = 3.dp, color = Color.White)
                }
            }
        }
    }
}

@Composable
private fun KindIcon(file: FileObject, size: Dp, uploading: Boolean = false) {
    // Fylvo: the web app's colour per file type; an unfinished upload stays neutral.
    Icon(
        if (uploading) Icons.Outlined.CloudUpload else fileIconOf(file),
        contentDescription = null,
        tint = if (uploading) MaterialTheme.colorScheme.onSurfaceVariant else fileIconTint(file),
        modifier = Modifier.size(size),
    )
}

/** Fylvo: second line of a file: date and size, or "Uploading…" while its upload is not finished. */
@Composable
fun fileSubtitle(file: FileObject): String =
    if (file.isUploading) stringResource(R.string.uploading_file) else subtitleFor(file.size, file.updatedAt, file.isFolder)

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun FileListItem(
    file: FileObject,
    thumbUrl: String?,
    selecting: Boolean,
    selected: Boolean,
    actions: FileActions,
    downloading: Boolean = false,
    downloadProgress: Float? = null,
    /** Fylvo: a copy is kept on this phone ("Keep offline"); shown as a small pin. */
    offline: Boolean = false,
    subtitle: String = fileSubtitle(file),
) {
    val shape = RoundedCornerShape(12.dp)
    val background by animateColorAsState(
        if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent,
        animationSpec = tween(160), label = "rowBg",
    )
    Row(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 8.dp, vertical = 1.dp)
            .clip(shape)
            .background(background)
            .fileClicks(actions, selectionState(selecting, selected))
            .padding(start = 10.dp, top = 6.dp, bottom = 6.dp, end = 0.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        FileThumb(
            file, thumbUrl, size = 36.dp, corner = 8.dp, iconSize = 22.dp, plain = true,
            downloading = downloading, downloadProgress = downloadProgress,
        )
        Column(
            Modifier
                .weight(1f)
                .padding(horizontal = 14.dp),
        ) {
            Text(
                file.name,
                style = MaterialTheme.typography.bodyLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (subtitle.isNotEmpty() || offline) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (offline) {
                        Icon(
                            Icons.Default.OfflinePin, contentDescription = stringResource(R.string.available_offline),
                            tint = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.padding(end = 4.dp).size(14.dp),
                        )
                    }
                    Text(
                        subtitle,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
        if (selecting) {
            SelectionMark(selected, Modifier.padding(12.dp))
        } else {
            // Fylvo: keeps the row as high as with the selection mark (the menu button is gone).
            Box(Modifier.size(width = 12.dp, height = 48.dp))
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun FileGridItem(
    file: FileObject,
    thumbUrl: String?,
    selecting: Boolean,
    selected: Boolean,
    actions: FileActions,
    downloading: Boolean = false,
    downloadProgress: Float? = null,
    /** Photo-gallery look: edge-to-edge thumbnail, no caption, play badge on videos. */
    gallery: Boolean = false,
    /** Fylvo: kept on this phone; a small pin before the caption's second line. */
    offline: Boolean = false,
) {
    val shape = RoundedCornerShape(if (gallery) 6.dp else 16.dp)
    val background = if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent
    Column(
        Modifier
            .clip(shape)
            .background(background)
            // Gallery tiles render no text; give TalkBack the file name.
            .then(if (gallery) Modifier.semantics { contentDescription = file.name } else Modifier)
            .fileClicks(actions, selectionState(selecting, selected))
            .padding(if (gallery) 0.dp else 6.dp),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(1f)) {
            FileThumb(
                file, thumbUrl, size = null, modifier = Modifier.fillMaxSize(), corner = if (gallery) 6.dp else 12.dp, iconSize = 44.dp,
                downloading = downloading, downloadProgress = downloadProgress,
            )
            if (FileKind.of(file) == FileKind.VIDEO) {
                Icon(
                    Icons.Default.PlayCircle,
                    contentDescription = stringResource(R.string.video),
                    tint = Color.White.copy(alpha = 0.9f),
                    modifier = Modifier
                        .align(if (gallery) Alignment.Center else Alignment.BottomStart)
                        .padding(6.dp)
                        .size(if (gallery) 36.dp else 22.dp),
                )
            }
            // The selection mark sits on the tile so the caption keeps its full width.
            if (selecting) {
                SelectionMark(selected, Modifier.align(Alignment.TopEnd).padding(6.dp))
            }
        }
        if (!gallery) Column(Modifier.fillMaxWidth().padding(top = 6.dp, start = 4.dp, end = 4.dp)) {
            Text(
                file.name,
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            val subtitle = fileSubtitle(file)
            if (subtitle.isNotEmpty() || offline) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (offline) {
                        Icon(
                            Icons.Default.OfflinePin, contentDescription = stringResource(R.string.available_offline),
                            tint = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.padding(end = 4.dp).size(12.dp),
                        )
                    }
                    Text(
                        subtitle,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

/** Fylvo: decorative; the row itself tells TalkBack whether it is selected. */
@Composable
private fun SelectionMark(selected: Boolean, modifier: Modifier = Modifier) {
    if (selected) {
        Icon(
            Icons.Default.CheckCircle,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary,
            modifier = modifier.size(24.dp),
        )
    } else {
        Icon(
            Icons.Outlined.CheckBoxOutlineBlank,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = modifier.size(24.dp),
        )
    }
}
