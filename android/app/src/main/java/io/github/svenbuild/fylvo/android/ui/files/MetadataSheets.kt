package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.FormatColorReset
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.TagInfo
import io.github.svenbuild.fylvo.android.data.prefs.Accents
import io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs

/** Fylvo: the folder colour of [folders]: one of the preset colours or none ([onPick] with null). */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun FolderColorSheet(folders: List<FileObject>, onPick: (String?) -> Unit, onDismiss: () -> Unit) {
    val current = folders.map { MobilePrefs.folderColor(it)?.lowercase() }.distinct().singleOrNull()
    val choices = (MobilePrefs.COLORS + listOfNotNull(current)).distinct()
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Text(
            stringResource(R.string.folder_color),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
        )
        FlowRow(
            Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            val noColor = stringResource(R.string.color_default)
            ColorDot(null, selected = current == null, label = noColor) { onPick(null) }
            choices.forEach { hex -> ColorDot(hex, selected = hex.equals(current, ignoreCase = true), label = hex) { onPick(hex) } }
        }
        Spacer(Modifier.height(28.dp))
    }
}

@Composable
private fun ColorDot(hex: String?, selected: Boolean, label: String, onClick: () -> Unit) {
    val color = colorOf(hex)
    Box(
        Modifier
            .size(40.dp)
            .then(if (selected) Modifier.border(2.dp, MaterialTheme.colorScheme.onSurface, CircleShape) else Modifier)
            .padding(5.dp)
            .background(color ?: Color.Transparent, CircleShape)
            .then(if (color == null) Modifier.border(1.dp, MaterialTheme.colorScheme.outline, CircleShape) else Modifier)
            .semantics {
                contentDescription = label
                this.selected = selected
            }
            .clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        when {
            color == null -> Icon(Icons.Default.FormatColorReset, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(16.dp))
            selected -> Icon(Icons.Default.Check, contentDescription = null, tint = Color(Accents.onColor(color.value.shr(32).toInt())), modifier = Modifier.size(16.dp))
        }
    }
}

/**
 * Fylvo: tags of [files]: every known tag as a chip (on when all files have it); tapping adds
 * or removes it at once ([onSet] name, colour, on). A new tag gets a name and a colour.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun TagsSheet(
    files: List<FileObject>,
    known: List<TagInfo>,
    onSet: (name: String, color: String, on: Boolean) -> Unit,
    onDismiss: () -> Unit,
) {
    // Tags of the account plus those on the files (an older server has no list).
    val onFiles = files.flatMap { MobilePrefs.tagsOf(it) }
    /** What was switched in this sheet (the rows on screen update after the sheet closes). */
    var changed by remember { mutableStateOf(mapOf<String, Pair<String, Boolean>>()) }
    val all = (changed.map { (n, v) -> n to v.first } + known.map { it.name to it.color } + onFiles)
        .distinctBy { it.first }.sortedBy { it.first.lowercase() }
    fun set(name: String, color: String, on: Boolean) {
        changed = changed + (name to (color to on))
        onSet(name, color, on)
    }
    var newName by remember { mutableStateOf("") }
    var newColor by remember { mutableStateOf(MobilePrefs.COLORS.first()) }
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Text(
            stringResource(R.string.tags),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
        )
        if (all.isEmpty()) {
            Text(
                stringResource(R.string.tags_none_yet),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
            )
        }
        FlowRow(
            Modifier.padding(horizontal = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            all.forEach { (name, color) ->
                val on = changed[name]?.second ?: (files.isNotEmpty() && files.all { f -> MobilePrefs.tagsOf(f).any { it.first == name } })
                FilterChip(
                    selected = on,
                    onClick = { set(name, color, !on) },
                    label = { Text(name) },
                    leadingIcon = {
                        Box(Modifier.size(8.dp).background(colorOf(color) ?: MaterialTheme.colorScheme.onSurfaceVariant, CircleShape))
                    },
                )
            }
        }
        Column(Modifier.padding(horizontal = 16.dp, vertical = 12.dp)) {
            Text(stringResource(R.string.tag_new), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
            Row(verticalAlignment = Alignment.CenterVertically) {
                OutlinedTextField(
                    value = newName,
                    onValueChange = { newName = it.take(MAX_TAG_LENGTH) },
                    singleLine = true,
                    placeholder = { Text(stringResource(R.string.tag_name)) },
                    modifier = Modifier.weight(1f),
                )
                TextButton(
                    enabled = newName.isNotBlank(),
                    onClick = {
                        set(newName.trim(), newColor, true)
                        newName = ""
                    },
                ) { Text(stringResource(R.string.add)) }
            }
            Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                MobilePrefs.COLORS.forEach { hex ->
                    val picked = hex == newColor
                    Box(
                        Modifier
                            .size(28.dp)
                            .then(if (picked) Modifier.border(2.dp, MaterialTheme.colorScheme.onSurface, CircleShape) else Modifier)
                            .padding(4.dp)
                            .background(colorOf(hex) ?: Color.Gray, CircleShape)
                            .semantics {
                                contentDescription = hex
                                selected = picked
                            }
                            .clickable { newColor = hex },
                    )
                }
            }
        }
        Spacer(Modifier.height(24.dp))
    }
}

private const val MAX_TAG_LENGTH = 64

/** Fylvo: a tag as a small chip with its colour and count (Home, Tags page). */
@Composable
fun TagChip(tag: TagInfo, onClick: () -> Unit) {
    androidx.compose.material3.Surface(
        onClick = onClick,
        shape = RoundedCornerShape(16.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        color = Color.Transparent,
    ) {
        Row(Modifier.padding(horizontal = 12.dp, vertical = 7.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(8.dp).background(colorOf(tag.color) ?: MaterialTheme.colorScheme.onSurfaceVariant, CircleShape))
            Text(tag.name, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 7.dp))
            Text(" ${tag.count}", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
