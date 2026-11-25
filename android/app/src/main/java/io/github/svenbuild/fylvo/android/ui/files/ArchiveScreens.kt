package io.github.svenbuild.fylvo.android.ui.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Inventory2
import androidx.compose.material.icons.outlined.TaskAlt
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.annotation.StringRes
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ArchiveEntry
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.TaskItem
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.ArchiveRepository
import io.github.svenbuild.fylvo.android.util.ErrorText
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive

/**
 * Fylvo 1.6: the contents of a zip or 7z archive, browsed like a folder (design/android-parity-plan.md,
 * 11). "Extract" extracts all of it (no single entries).
 */
@Composable
fun ArchivePage(file: FileObject, canExtract: Boolean, onExtract: () -> Unit, onClose: () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    var entries by remember { mutableStateOf<List<ArchiveEntry>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var dir by remember { mutableStateOf("") }
    // Fylvo 1.6.5: an archive with encrypted names asks for its password. [ask]: null = no
    // question, false = needed, true = the last one did not fit. The typed password is used for
    // the one request that follows and then dropped; it is never saved or logged.
    var ask by remember { mutableStateOf<Boolean?>(null) }
    var typed by remember { mutableStateOf<String?>(null) }
    var attempt by remember { mutableIntStateOf(0) }
    LaunchedEffect(file.path, attempt) {
        entries = null
        error = null
        val password = typed
        typed = null
        try {
            entries = app.archives.entries(file.path, password)
            ask = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            when ((e as? ApiException)?.code) {
                ErrorText.CODE_ARCHIVE_ENCRYPTED -> ask = false
                ErrorText.CODE_ARCHIVE_WRONG_PASSWORD -> ask = true
                else -> error = userError(e).resolve(context)
            }
        }
    }
    ask?.let { wrong ->
        ArchivePasswordDialog(
            name = file.name,
            wrong = wrong,
            onOpen = { pw -> typed = pw; ask = null; attempt++ },
            onDismiss = onClose,
        )
    }
    BackHandler { if (dir.isNotEmpty()) dir = dir.substringBeforeLast('/', "") else onClose() }
    val title = if (dir.isEmpty()) file.name else dir.substringAfterLast('/')
    PlacePage(title, onBack = { if (dir.isNotEmpty()) dir = dir.substringBeforeLast('/', "") else onClose() }, bottomBar = {
        if (canExtract) Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
            Button(onClick = onExtract) { Text(stringResource(R.string.archive_extract)) }
        }
    }) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            val list = entries
            when {
                error != null -> Text(error!!, color = MaterialTheme.colorScheme.error, modifier = Modifier.align(Alignment.Center).padding(24.dp))
                list == null && ask != null -> Text(stringResource(R.string.archive_encrypted), modifier = Modifier.align(Alignment.Center).padding(24.dp))
                list == null -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                else -> {
                    val shown = ArchiveRepository.childrenOf(list, dir)
                    if (shown.isEmpty()) EmptyState(icon = Icons.Outlined.Inventory2, title = stringResource(R.string.archive_empty))
                    else LazyColumn(Modifier.fillMaxSize()) {
                        items(shown, key = { it.name }) { entry ->
                            val name = entry.name.substringAfterLast('/')
                            Row(
                                Modifier.fillMaxWidth()
                                    .clickable(enabled = entry.isDirectory) { dir = entry.name.trimEnd('/') }
                                    .padding(horizontal = 20.dp, vertical = 10.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                FileThumb(FileObject(type = if (entry.isDirectory) 1 else 0, name = name), null, size = 40.dp)
                                Column(Modifier.padding(start = 16.dp)) {
                                    Text(name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    if (!entry.isDirectory) Text(formatSize(entry.size), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

/** Fylvo 1.6.5: asks for the password of an encrypted archive; the text lives only in this dialog. */
@Composable
private fun ArchivePasswordDialog(name: String, wrong: Boolean, onOpen: (String) -> Unit, onDismiss: () -> Unit) {
    var text by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        text = {
            Column {
                Text(stringResource(if (wrong) R.string.archive_password_wrong else R.string.archive_encrypted))
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it },
                    label = { Text(stringResource(R.string.password)) },
                    singleLine = true,
                    isError = wrong,
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    textStyle = MaterialTheme.typography.bodyLarge,
                )
            }
        },
        confirmButton = { TextButton(enabled = text.isNotEmpty(), onClick = { onOpen(text) }) { Text(stringResource(R.string.open)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

/**
 * Fylvo 1.6: Account › tasks: archive and other jobs of the account on the server. While one runs
 * and the page is open, the first page is read again every 3 seconds. Tasks cannot be cancelled
 * (the server has no way for that).
 */
@Composable
fun TasksPage(onBack: () -> Unit, bottomBar: @Composable () -> Unit, onOpenFolder: (String) -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    val tasks = remember { mutableStateListOf<TaskItem>() }
    var next by remember { mutableStateOf<String?>(null) }
    var loaded by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(Unit) {
        while (true) {
            runCatching { app.archives.tasks() }
                .onSuccess { page ->
                    // Rows of the first page are updated in place, new ones come on top; pages
                    // loaded by hand stay below.
                    val byId = page.tasks.associateBy { it.id }
                    val updated = tasks.map { byId[it.id] ?: it }
                    val fresh = page.tasks.filter { t -> tasks.none { it.id == t.id } }
                    tasks.clear()
                    tasks.addAll(fresh + updated)
                    if (!loaded) next = page.next
                    loaded = true
                    error = null
                }
                .onFailure { error = userError(it).resolve(context) }
            if (tasks.none(ArchiveRepository::running)) break
            delay(3000)
        }
    }

    PlacePage(stringResource(R.string.tasks), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when {
                !loaded && error != null -> Text(error!!, modifier = Modifier.align(Alignment.Center).padding(24.dp))
                !loaded -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                tasks.isEmpty() -> EmptyState(icon = Icons.Outlined.TaskAlt, title = stringResource(R.string.tasks_none))
                else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
                    items(tasks, key = { it.id }) { task -> TaskRow(task, onOpenFolder) }
                    next?.let { token ->
                        item(key = "more") {
                            TextButton(onClick = {
                                scope.launch {
                                    runCatching { app.archives.tasks(token) }.onSuccess { page ->
                                        tasks.addAll(page.tasks.filter { t -> tasks.none { it.id == t.id } })
                                        next = page.next
                                    }
                                }
                            }, modifier = Modifier.padding(horizontal = 12.dp)) { Text(stringResource(R.string.load_more)) }
                        }
                    }
                }
            }
        }
    }
}

@StringRes
fun taskTypeLabel(type: String): Int = when (type) {
    "create_archive" -> R.string.task_create_archive
    "extract_archive" -> R.string.task_extract_archive
    "relocate" -> R.string.task_relocate
    "import" -> R.string.task_import
    "remote_download" -> R.string.task_remote_download
    else -> R.string.task_other
}

@Composable
private fun TaskRow(task: TaskItem, onOpenFolder: (String) -> Unit) {
    val props = task.summary?.props
    fun prop(key: String) = (props?.get(key) as? JsonPrimitive)?.content
    val sources = (props?.get("src_multiple") as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.content } ?: listOfNotNull(prop("src"))
    val dst = prop("dst")
    val what = stringResource(taskTypeLabel(task.type))
    val status = stringResource(
        when (task.status) {
            "queued" -> R.string.task_queued
            "processing", "suspending" -> R.string.task_running
            "completed" -> R.string.task_done
            "error" -> R.string.task_status_failed
            "canceled" -> R.string.task_status_canceled
            else -> R.string.task_other
        },
    )
    // A finished extraction opens its target folder; a finished archive the folder it lies in.
    val open = when {
        task.status != "completed" -> null
        task.type == "extract_archive" -> ArchiveRepository.openable(dst)
        task.type == "create_archive" -> ArchiveRepository.openable(dst?.let(CrUri::parent))
        else -> null
    }
    Column(
        Modifier.fillMaxWidth().clickable(enabled = open != null) { open?.let(onOpenFolder) }.padding(horizontal = 20.dp, vertical = 10.dp),
    ) {
        Text(
            what + ": " + sources.joinToString(", ") { CrUri.fileName(it) }.ifEmpty { "…" },
            style = MaterialTheme.typography.bodyLarge, maxLines = 2, overflow = TextOverflow.Ellipsis,
        )
        Text(
            listOfNotNull(status, dst?.let { stringResource(R.string.task_to, CrUri.displayPath(it)) }, ActivityText.time(task.createdAt.ifEmpty { task.updatedAt }))
                .joinToString(" · "),
            style = MaterialTheme.typography.bodySmall,
            color = if (task.status == "error") MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2, overflow = TextOverflow.Ellipsis,
        )
        task.error?.takeIf { it.isNotBlank() && task.status == "error" }?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error, maxLines = 3, overflow = TextOverflow.Ellipsis)
        }
        if (ArchiveRepository.running(task)) LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 6.dp))
    }
}
