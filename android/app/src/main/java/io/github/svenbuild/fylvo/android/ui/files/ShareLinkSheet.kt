package io.github.svenbuild.fylvo.android.ui.files

import android.content.Context
import android.content.Intent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SelectableDates
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ShareInfo
import io.github.svenbuild.fylvo.android.data.LinkOptions
import io.github.svenbuild.fylvo.android.data.UploadMode
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/** Link lifetimes in days; 0 = never expires. */
private val EXPIRY_DAYS = listOf(0, 1, 7, 30)

/** Download limits; 0 = unlimited. */
private val DOWNLOAD_OPTIONS = listOf(0, 1, 5, 25)

/** Fylvo: upload limits of a folder link in GB; 0 = no limit (as the web, `ShareSetting.tsx`). */
private val UPLOAD_LIMIT_GB = listOf(0L, 1L, 5L, 20L, 100L)
private const val GB = 1024L * 1024 * 1024

/**
 * Share-link sheet: choose how open the link is, save, then copy or hand it to the system share
 * sheet. Fylvo: also edits an [existing] link with all its settings (expiry, downloads, uploads,
 * view, README); the password of an existing link cannot be changed (the server keeps it).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ShareLinkSheet(
    name: String,
    isFolder: Boolean,
    thumb: @Composable () -> Unit,
    existing: ShareInfo?,
    save: suspend (LinkOptions) -> String,
    onDismiss: () -> Unit,
    onMessage: (String) -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var options by remember(existing?.id) { mutableStateOf(existing?.let { LinkOptions.of(it) } ?: LinkOptions()) }
    var usePassword by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    var link by remember { mutableStateOf<String?>(null) }
    var pickDate by remember { mutableStateOf(false) }
    var pickDownloads by remember { mutableStateOf(false) }
    // The choice as shown; a value that is none of the chips gets a chip of its own.
    val customExpiry = options.expireSeconds?.takeIf { s -> EXPIRY_DAYS.none { it * 86_400L == s } }
    val customDownloads = options.downloads?.takeIf { it !in DOWNLOAD_OPTIONS }
    val customLimit = options.uploadLimit.takeIf { l -> l > 0 && UPLOAD_LIMIT_GB.none { it * GB == l } }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.verticalScroll(rememberScrollState())) {
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                thumb()
                Column(Modifier.padding(start = 16.dp)) {
                    Text(stringResource(if (existing == null) R.string.share_link else R.string.share_edit_link), style = MaterialTheme.typography.titleMedium)
                    Text(name, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }

            val made = link
            if (made == null) {
                OptionLabel(stringResource(R.string.link_expires))
                ChipRow {
                    EXPIRY_DAYS.forEach { days ->
                        val seconds = days * 86_400L
                        val label = if (days == 0) stringResource(R.string.never) else pluralStringResource(R.plurals.days, days, days)
                        val selected = !options.expireToChoose && if (days == 0) options.expireSeconds == null else options.expireSeconds == seconds
                        FilterChip(selected = selected, onClick = {
                            options = options.copy(expireSeconds = seconds.takeIf { days > 0 }, expireToChoose = false, keepEnd = null)
                        }, label = { Text(label) })
                    }
                    customExpiry?.takeIf { !options.expireToChoose }?.let { s ->
                        FilterChip(selected = true, onClick = { pickDate = true }, label = { Text(stringResource(R.string.share_until, endDateText(s))) })
                    }
                    FilterChip(selected = false, onClick = { pickDate = true }, label = { Text(stringResource(R.string.share_pick_date)) })
                }
                if (options.expireToChoose) Hint(stringResource(R.string.share_expired_choose))

                if (!isFolder) {
                    OptionLabel(stringResource(R.string.download_limit))
                    ChipRow {
                        DOWNLOAD_OPTIONS.forEach { count ->
                            val label = if (count == 0) stringResource(R.string.unlimited) else count.toString()
                            val selected = !options.downloadsToChoose && if (count == 0) options.downloads == null else options.downloads == count
                            FilterChip(selected = selected, onClick = {
                                options = options.copy(downloads = count.takeIf { it > 0 }, downloadsToChoose = false)
                            }, label = { Text(label) })
                        }
                        customDownloads?.takeIf { !options.downloadsToChoose }?.let { n ->
                            FilterChip(selected = true, onClick = { pickDownloads = true }, label = { Text(n.toString()) })
                        }
                        FilterChip(selected = false, onClick = { pickDownloads = true }, label = { Text(stringResource(R.string.share_pick_count)) })
                    }
                    if (options.downloadsToChoose) Hint(stringResource(R.string.share_downloads_choose))
                }

                if (isFolder) {
                    OptionLabel(stringResource(R.string.share_uploads))
                    ChipRow {
                        listOf(
                            UploadMode.OFF to R.string.share_uploads_off,
                            UploadMode.VIEW_AND_UPLOAD to R.string.share_view_and_upload,
                            UploadMode.UPLOAD_ONLY to R.string.share_upload_only,
                        ).forEach { (mode, label) ->
                            FilterChip(selected = options.upload == mode, onClick = { options = options.copy(upload = mode) }, label = { Text(stringResource(label)) })
                        }
                    }
                    if (options.upload != UploadMode.OFF) {
                        OptionLabel(stringResource(R.string.share_upload_limit))
                        ChipRow {
                            UPLOAD_LIMIT_GB.forEach { gb ->
                                val label = if (gb == 0L) stringResource(R.string.unlimited) else stringResource(R.string.share_gb, gb)
                                FilterChip(selected = options.uploadLimit == gb * GB, onClick = { options = options.copy(uploadLimit = gb * GB) }, label = { Text(label) })
                            }
                            customLimit?.let { bytes -> FilterChip(selected = true, onClick = {}, label = { Text(formatSize(bytes)) }) }
                        }
                    }
                    SwitchLine(stringResource(R.string.share_keep_view), null, options.shareView) { options = options.copy(shareView = it) }
                    SwitchLine(stringResource(R.string.share_show_readme), null, options.showReadme) { options = options.copy(showReadme = it) }
                }

                if (existing == null) {
                    SwitchLine(
                        stringResource(R.string.require_password),
                        stringResource(if (usePassword) R.string.password_in_link else R.string.anyone_with_link),
                        usePassword,
                    ) { usePassword = it }
                    if (usePassword) {
                        OutlinedTextField(
                            value = options.password,
                            // Only letters and digits, at most 32 (the server's rule).
                            onValueChange = { v -> options = options.copy(password = v.filter { it.isLetterOrDigit() && it.code < 128 }.take(32)) },
                            label = { Text(stringResource(R.string.password)) },
                            supportingText = { Text(stringResource(R.string.share_password_rule)) },
                            singleLine = true,
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                            modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp),
                        )
                    }
                } else if (existing.passwordProtected || existing.password.isNotEmpty()) {
                    Hint(stringResource(R.string.share_password_kept))
                }

                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 20.dp), horizontalArrangement = Arrangement.End) {
                    Button(
                        enabled = !saving && options.complete && (!usePassword || LinkOptions.passwordValid(options.password)),
                        onClick = {
                            saving = true
                            scope.launch {
                                runCatching { save(if (usePassword) options else options.copy(password = "")) }
                                    .onSuccess { link = it }
                                    .onFailure { onMessage(userError(it, fallback = R.string.create_link_failed).resolve(context)) }
                                saving = false
                            }
                        },
                    ) {
                        if (saving) CircularProgressIndicator(Modifier.height(18.dp), strokeWidth = 2.dp)
                        else Text(stringResource(if (existing == null) R.string.create_link else R.string.save))
                    }
                }
            } else {
                Text(
                    made,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.primary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(horizontal = 20.dp, vertical = 20.dp),
                )
                LinkButtons(made, name, onDone = onDismiss, onMessage = onMessage)
                Spacer(Modifier.height(20.dp))
            }
            Spacer(Modifier.height(12.dp))
        }
    }

    if (pickDate) {
        val zone = ZoneId.systemDefault()
        val today = LocalDate.now(zone)
        val last = today.plusYears(10).minusDays(1)
        val state = rememberDatePickerState(
            initialSelectedDateMillis = today.plusDays(7).atStartOfDay(ZoneOffset.UTC).toInstant().toEpochMilli(),
            selectableDates = object : SelectableDates {
                override fun isSelectableDate(utcTimeMillis: Long): Boolean {
                    val day = Instant.ofEpochMilli(utcTimeMillis).atZone(ZoneOffset.UTC).toLocalDate()
                    return !day.isBefore(today) && !day.isAfter(last)
                }
            },
        )
        DatePickerDialog(
            onDismissRequest = { pickDate = false },
            confirmButton = {
                TextButton(enabled = state.selectedDateMillis != null, onClick = {
                    state.selectedDateMillis?.let { millis ->
                        val day = Instant.ofEpochMilli(millis).atZone(ZoneOffset.UTC).toLocalDate()
                        // The end of the chosen day on this phone.
                        val end = day.plusDays(1).atStartOfDay(zone).toInstant()
                        val seconds = LinkOptions.remainingSeconds(end, Instant.now())
                        options = options.copy(expireSeconds = seconds.coerceIn(1, LinkOptions.MAX_EXPIRE_SECONDS), expireToChoose = false, keepEnd = null)
                    }
                    pickDate = false
                }) { Text(stringResource(R.string.ok)) }
            },
            dismissButton = { TextButton(onClick = { pickDate = false }) { Text(stringResource(R.string.cancel)) } },
        ) { DatePicker(state) }
    }

    if (pickDownloads) {
        var text by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { pickDownloads = false },
            title = { Text(stringResource(R.string.download_limit)) },
            text = {
                OutlinedTextField(
                    value = text,
                    onValueChange = { v -> text = v.filter(Char::isDigit).take(6) },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                )
            },
            confirmButton = {
                TextButton(enabled = (text.toIntOrNull() ?: 0) > 0, onClick = {
                    options = options.copy(downloads = text.toInt(), downloadsToChoose = false)
                    pickDownloads = false
                }) { Text(stringResource(R.string.ok)) }
            },
            dismissButton = { TextButton(onClick = { pickDownloads = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

/** Fylvo: copy or hand on a finished link. */
@Composable
fun LinkButtons(link: String, subject: String, onDone: () -> Unit, onMessage: (String) -> Unit) {
    val context = LocalContext.current
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 20.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End),
    ) {
        OutlinedButton(onClick = {
            copySensitive(context, context.getString(R.string.share_link), link)
            onMessage(context.getString(R.string.link_copied))
            onDone()
        }) {
            Icon(Icons.Default.ContentCopy, contentDescription = null)
            Spacer(Modifier.padding(4.dp))
            Text(stringResource(R.string.copy))
        }
        Button(onClick = {
            sendLink(context, subject, link)
            onDone()
        }) {
            Icon(Icons.Default.Share, contentDescription = null)
            Spacer(Modifier.padding(4.dp))
            Text(stringResource(R.string.share))
        }
    }
}

/** Fylvo: a link in the clipboard, marked as sensitive (no preview of it on screen). */
fun copySensitive(context: Context, label: String, text: String) {
    val cm = context.getSystemService(Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
    val clip = android.content.ClipData.newPlainText(label, text)
    clip.description.extras = android.os.PersistableBundle().apply {
        putBoolean("android.content.extra.IS_SENSITIVE", true)
    }
    cm.setPrimaryClip(clip)
}

fun sendLink(context: Context, subject: String, link: String) {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_SUBJECT, subject)
        putExtra(Intent.EXTRA_TEXT, link)
    }
    context.startActivity(Intent.createChooser(send, context.getString(R.string.share_file, subject)))
}

/** "12.12.2026" for a time [seconds] from now. */
private fun endDateText(seconds: Long): String = lastDayText(Instant.now().plusSeconds(seconds))

/** The last day a link works on: an end at (or seconds after) midnight belongs to the day before. */
fun lastDayText(end: Instant): String =
    end.minusSeconds(60).atZone(ZoneId.systemDefault()).toLocalDate()
        .format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM))

@Composable
private fun OptionLabel(text: String) {
    Text(text, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 20.dp, top = 16.dp, bottom = 8.dp))
}

@Composable
private fun Hint(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(horizontal = 20.dp, vertical = 6.dp),
    )
}

@Composable
private fun ChipRow(content: @Composable () -> Unit) {
    Row(
        Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 20.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) { content() }
}

@Composable
private fun SwitchLine(title: String, subtitle: String?, checked: Boolean, onChange: (Boolean) -> Unit) {
    ListItem(
        modifier = Modifier.padding(top = 4.dp).clickable { onChange(!checked) },
        headlineContent = { Text(title) },
        supportingContent = subtitle?.let { { Text(it) } },
        trailingContent = { Switch(checked = checked, onCheckedChange = onChange) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
}
