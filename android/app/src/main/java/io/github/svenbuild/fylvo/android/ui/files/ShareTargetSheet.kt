package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.util.CrUri

/** Fylvo: a folder the shared files can go to; [id] is checked before the upload (null: My files). */
data class ShareTarget(val uri: String, val id: String?, val color: String? = null)

/**
 * Fylvo: "Save in Fylvo" for files shared from another app: the folder used last, the favourite
 * folders, My files, or any other folder; nothing is uploaded until "Upload" is tapped.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ShareTargetSheet(
    count: Int,
    last: ShareTarget?,
    favorites: List<ShareTarget>,
    chosen: ShareTarget?,
    /** A folder chosen in the picker that is none of the rows above (shown first). */
    extra: ShareTarget? = null,
    busy: Boolean,
    /** A problem with the chosen folder, shown in the sheet (a snackbar would sit behind it). */
    notice: String?,
    onChoose: (ShareTarget) -> Unit,
    onPickOther: () -> Unit,
    onUpload: () -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState())) {
            Text(
                stringResource(R.string.share_into_fylvo),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 20.dp),
            )
            Text(
                pluralStringResource(R.plurals.share_files_count, count, count),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 2.dp),
            )
            if (notice != null) {
                Text(
                    notice,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(horizontal = 20.dp, vertical = 6.dp),
                )
            }
            val root = ShareTarget(CrUri.MY_PREFIX, null)
            if (extra != null) {
                Label(stringResource(R.string.share_chosen))
                TargetRow(extra, chosen, onChoose)
            }
            if (last != null) {
                Label(stringResource(R.string.share_last_used))
                TargetRow(last, chosen, onChoose)
            }
            if (favorites.isNotEmpty()) {
                Label(stringResource(R.string.favorites))
                favorites.forEach { TargetRow(it, chosen, onChoose) }
            }
            Label(stringResource(R.string.share_other_places))
            if (last?.uri != root.uri) TargetRow(root, chosen, onChoose)
            ListItem(
                modifier = Modifier.clickable(onClick = onPickOther),
                headlineContent = { Text(stringResource(R.string.share_pick_folder)) },
                leadingContent = { Icon(Icons.Outlined.Folder, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant) },
                trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                colors = ListItemDefaults.colors(containerColor = Color.Transparent),
            )
            Button(
                onClick = onUpload,
                enabled = chosen != null && !busy,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 12.dp),
            ) { Text(stringResource(R.string.upload)) }
            Spacer(Modifier.height(16.dp))
        }
    }
}

@Composable
private fun Label(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 20.dp, end = 20.dp, top = 14.dp, bottom = 2.dp),
    )
}

@Composable
private fun TargetRow(target: ShareTarget, chosen: ShareTarget?, onChoose: (ShareTarget) -> Unit) {
    val selected = chosen?.uri == target.uri
    ListItem(
        modifier = Modifier.clickable { onChoose(target) },
        headlineContent = { Text(CrUri.displayName(target.uri)) },
        supportingContent = if (CrUri.isRoot(target.uri)) null else ({ Text(CrUri.displayPath(CrUri.parent(target.uri))) }),
        leadingContent = {
            val color = colorOf(target.color)
            Icon(
                if (color != null) Icons.Filled.Folder else Icons.Outlined.Folder,
                contentDescription = null,
                tint = color ?: MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(24.dp),
            )
        },
        trailingContent = { RadioButton(selected = selected, onClick = { onChoose(target) }) },
        colors = ListItemDefaults.colors(containerColor = if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent),
    )
}
