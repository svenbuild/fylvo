package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.outlined.StarOutline
import androidx.compose.material.icons.outlined.Palette
import androidx.compose.material.icons.automirrored.outlined.Label
import androidx.compose.material.icons.automirrored.filled.DriveFileMove
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.filled.FileCopy
import androidx.compose.material.icons.filled.FolderOpen
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.DeleteForever
import androidx.compose.material.icons.filled.RestoreFromTrash
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.EditOff
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.TaskAlt
import androidx.compose.material.icons.outlined.OfflinePin
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.api.FileObject

/**
 * Per-file actions as a bottom sheet (the Files app pattern): the file's
 * identity on top so the user knows what they are acting on, then the
 * actions in order of how often they are reached for.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileActionSheet(
    file: FileObject,
    thumbUrl: String?,
    /** Fylvo: kept offline as its own root. */
    offlineOn: Boolean,
    /** Fylvo: inside an offline folder; uses the folder's copy and cannot be switched separately. */
    offlineCovered: Boolean,
    /** Fylvo: offline can be switched here (My files, shared folders). */
    offlineAllowed: Boolean,
    onDismiss: () -> Unit,
    onOpen: () -> Unit,
    onShare: () -> Unit,
    onDownload: () -> Unit,
    onToggleOffline: () -> Unit,
    onRename: () -> Unit,
    onDelete: () -> Unit,
    onMove: () -> Unit,
    onCopy: () -> Unit,
    onDetails: () -> Unit,
    /** Shown when the sheet was opened outside the file's own folder (home, categories, search). */
    onShowInFolder: (() -> Unit)? = null,
    /** Fylvo: rename, move, copy and delete; false inside a share without edit right. */
    canModify: Boolean = true,
    canShare: Boolean = true,
    inTrash: Boolean = false,
    onRestore: () -> Unit = {},
    onDeleteForever: () -> Unit = {},
    /** Fylvo: "Bearbeiten" is offered (FilesUiState.canEdit). */
    canEdit: Boolean = false,
    /** Fylvo: the file has a working copy in use: finish or discard it. */
    edited: Boolean = false,
    onEdit: () -> Unit = {},
    onFinishEdit: () -> Unit = {},
    onDiscardEdit: () -> Unit = {},
    /** Fylvo: the star of this file (add, remove or none) and its colour and tags. */
    star: io.github.svenbuild.fylvo.android.data.prefs.StarState = io.github.svenbuild.fylvo.android.data.prefs.StarState.NONE,
    onStar: (Boolean) -> Unit = {},
    canEditMetadata: Boolean = false,
    onColor: () -> Unit = {},
    onTags: () -> Unit = {},
) {
    // Every action should be reachable without a second drag, so skip the half-open state.
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 20.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FileThumb(file, thumbUrl, size = 56.dp)
            Column(Modifier.padding(start = 16.dp)) {
                Text(
                    file.name,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    fileSubtitle(file).ifEmpty { if (file.isFolder) stringResource(R.string.folder) else "" },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        HorizontalDivider(Modifier.padding(vertical = 8.dp))

        if (inTrash) {
            SheetAction(Icons.Default.RestoreFromTrash, stringResource(R.string.restore), onRestore)
            SheetAction(Icons.Default.DeleteForever, stringResource(R.string.delete_forever), onDeleteForever, tint = MaterialTheme.colorScheme.error)
            Spacer(Modifier.height(24.dp))
            return@ModalBottomSheet
        }

        // Fylvo: the server locks a file while its upload is not finished; offer only what still works.
        if (file.isUploading) {
            Text(
                stringResource(R.string.uploading_note),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
            )
            onShowInFolder?.let { SheetAction(Icons.Default.FolderOpen, stringResource(R.string.show_in_folder), it) }
            SheetAction(Icons.Outlined.Info, stringResource(R.string.details), onDetails)
            Spacer(Modifier.height(24.dp))
            return@ModalBottomSheet
        }

        SheetAction(Icons.AutoMirrored.Filled.OpenInNew, stringResource(if (file.isFolder) R.string.open_folder else R.string.open), onOpen)
        onShowInFolder?.let { SheetAction(Icons.Default.FolderOpen, stringResource(R.string.show_in_folder), it) }
        if (canEdit) SheetAction(Icons.Outlined.Edit, stringResource(R.string.edit), onEdit)
        if (edited) SheetAction(Icons.Outlined.TaskAlt, stringResource(R.string.edit_finish), onFinishEdit)
        when (star) {
            io.github.svenbuild.fylvo.android.data.prefs.StarState.CAN_ADD ->
                SheetAction(Icons.Outlined.StarOutline, stringResource(R.string.add_to_favorites), { onStar(true) })
            io.github.svenbuild.fylvo.android.data.prefs.StarState.CAN_REMOVE ->
                SheetAction(Icons.Default.Star, stringResource(R.string.remove_from_favorites), { onStar(false) })
            else -> Unit
        }
        if (canShare) SheetAction(Icons.Default.Link, stringResource(R.string.share_link), onShare)
        SheetAction(Icons.Default.Download, stringResource(if (file.isFolder) R.string.download_as_zip else R.string.download), onDownload)
        when {
            offlineCovered -> OfflineSwitchRow(checked = true, enabled = false, subtitle = stringResource(R.string.offline_covered), onToggle = {})
            !offlineAllowed -> Unit
            file.isFolder -> OfflineSwitchRow(checked = offlineOn, enabled = true, subtitle = null, onToggle = onToggleOffline)
            else -> SheetAction(
                if (offlineOn) Icons.Default.OfflinePin else Icons.Outlined.OfflinePin,
                stringResource(if (offlineOn) R.string.remove_offline_copy else R.string.keep_offline),
                onToggleOffline,
            )
        }
        if (canModify) {
            SheetAction(Icons.AutoMirrored.Filled.DriveFileMove, stringResource(R.string.move_to), onMove)
            SheetAction(Icons.Default.FileCopy, stringResource(R.string.copy_to), onCopy)
            SheetAction(Icons.Default.DriveFileRenameOutline, stringResource(R.string.rename), onRename)
        }
        if (canEditMetadata) {
            if (file.isFolder) SheetAction(Icons.Outlined.Palette, stringResource(R.string.folder_color), onColor)
            SheetAction(Icons.AutoMirrored.Outlined.Label, stringResource(R.string.tags), onTags)
        }
        SheetAction(Icons.Outlined.Info, stringResource(R.string.details), onDetails)
        if (edited) SheetAction(Icons.Outlined.EditOff, stringResource(R.string.edit_discard), onDiscardEdit, tint = MaterialTheme.colorScheme.error)
        if (canModify) SheetAction(Icons.Default.Delete, stringResource(R.string.delete), onDelete, tint = MaterialTheme.colorScheme.error)
        Spacer(Modifier.height(24.dp))
    }
}

/** Fylvo: "Available offline" for a folder, or shown switched on for anything in an offline folder. */
@Composable
private fun OfflineSwitchRow(checked: Boolean, enabled: Boolean, subtitle: String?, onToggle: () -> Unit) {
    ListItem(
        modifier = if (enabled) Modifier.clickable(onClick = onToggle) else Modifier,
        headlineContent = { Text(stringResource(R.string.available_offline)) },
        supportingContent = subtitle?.let { { Text(it) } },
        leadingContent = {
            Icon(
                if (checked) Icons.Default.OfflinePin else Icons.Outlined.OfflinePin, contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        },
        trailingContent = { Switch(checked = checked, onCheckedChange = { onToggle() }, enabled = enabled) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
}

@Composable
private fun SheetAction(
    icon: ImageVector,
    label: String,
    onClick: () -> Unit,
    tint: Color = MaterialTheme.colorScheme.onSurfaceVariant,
) {
    ListItem(
        modifier = Modifier.clickable(onClick = onClick),
        headlineContent = { Text(label, color = if (tint == MaterialTheme.colorScheme.error) tint else Color.Unspecified) },
        leadingContent = { Icon(icon, contentDescription = null, tint = tint) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
}
