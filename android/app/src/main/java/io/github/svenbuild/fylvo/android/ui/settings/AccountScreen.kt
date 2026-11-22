package io.github.svenbuild.fylvo.android.ui.settings

import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Language
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material.icons.filled.SystemUpdate
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.produceState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import kotlinx.coroutines.launch
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.data.offline.OfflineEdits
import io.github.svenbuild.fylvo.android.ui.files.EditChoiceDialog
import io.github.svenbuild.fylvo.android.ui.files.notUploadedText
import io.github.svenbuild.fylvo.android.util.asString
import androidx.compose.ui.res.pluralStringResource
import io.github.svenbuild.fylvo.android.BuildConfig
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.AppUpdater
import androidx.lifecycle.compose.LifecycleResumeEffect
import io.github.svenbuild.fylvo.android.api.CapacityResponse
import io.github.svenbuild.fylvo.android.ui.files.formatSize
import io.github.svenbuild.fylvo.android.util.AppLanguage
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.data.SyncFolderSettings
import io.github.svenbuild.fylvo.android.work.SyncFolderWorker
import io.github.svenbuild.fylvo.android.work.TaskPollWorker
import java.text.DateFormat
import java.util.Date

/**
 * Fylvo: the Account tab. Who is signed in, storage, offline files, folder sync,
 * notifications and sign-out. A full screen instead of the old
 * cramped dialog; each section is a group of list rows with a toggle, like
 * the system Settings app.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AccountScreen(
    offlineCount: Int,
    onOpenOffline: () -> Unit,
    onBack: () -> Unit,
    bottomBar: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val notify = app.taskNotifySettings
    val sync = app.syncFolderSettings
    val scope = rememberCoroutineScope()

    val notifyEnabled by notify.enabled.collectAsState(initial = false)
    val syncSnap by sync.snapshot.collectAsState(initial = null)
    val serverUrl by app.sessionManager.serverUrl.collectAsState(initial = "")
    val email by app.sessionManager.userEmail.collectAsState(initial = "")
    val offlineMobileData by app.offlineManager.mobileData.collectAsState(initial = false)

    // Fylvo: the user edits a plain path below My files ("Sync/Phone"), stored as a cloudreve:// URI.
    var syncFolder by remember(syncSnap?.remoteFolder) {
        mutableStateOf(
            (syncSnap?.remoteFolder ?: SyncFolderSettings.DEFAULT_FOLDER).removePrefix(CrUri.MY_PREFIX).trim('/')
        )
    }
    fun syncFolderUri() = CrUri.join(CrUri.MY_PREFIX, syncFolder.trim('/').ifEmpty { "Sync" })

    val treeLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        if (uri != null) {
            context.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
            scope.launch {
                sync.setTreeUri(uri.toString())
                sync.setRemoteFolder(syncFolderUri())
                sync.setEnabled(true)
                SyncFolderWorker.apply(context, syncSnap?.wifiOnly ?: true)
            }
        }
    }
    val notifPermLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) scope.launch {
            notify.setEnabled(true)
            TaskPollWorker.apply(context)
        }
    }
    fun persistFolders() = scope.launch {
        sync.setRemoteFolder(syncFolderUri())
    }

    // System back bypasses the TopAppBar button; persist there too.
    BackHandler { persistFolders(); onBack() }

    var confirmSignOut by remember { mutableStateOf(false) }
    /** Fylvo: edited files with changes not on the server yet ask before the sign-out. */
    var editSignOut by remember { mutableStateOf<EditSignOut?>(null) }

    /** Fylvo: signs out with [choice]; asks (again) when working copies still hold changes. */
    fun signOut(choice: OfflineEdits.SignOutChoice) {
        editSignOut = editSignOut?.copy(busy = true)
        scope.launch {
            editSignOut = when (val result = app.signOut(choice, editSignOut?.ids.orEmpty())) {
                OfflineEdits.SignOutResult.Done -> null
                is OfflineEdits.SignOutResult.NeedsChoice -> EditSignOut(result.ids)
                is OfflineEdits.SignOutResult.Failed -> EditSignOut(result.ids, result.reason, code = result.code)
            }
        }
    }
    var chooseLanguage by remember { mutableStateOf(false) }
    // A change usually restarts the screen; kept as state for when the shown language stays the same.
    var appLanguage by remember { mutableStateOf(AppLanguage.current()) }

    Scaffold(
        topBar = {
            TopAppBar(title = { Text(stringResource(R.string.tab_account)) })
        },
        bottomBar = bottomBar,
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState()),
        ) {
            val capacity by produceState<CapacityResponse?>(initialValue = null) {
                value = app.fileRepository.capacity()
            }
            ListItem(
                headlineContent = { Text(email.ifEmpty { stringResource(R.string.signed_in) }) },
                supportingContent = { Text(serverUrl) },
                leadingContent = { Icon(Icons.Default.AccountCircle, contentDescription = null) },
            )
            capacity?.let { cap ->
                if (cap.total > 0) {
                    val pct = (cap.used.toDouble() / cap.total * 100).toInt().coerceIn(0, 100)
                    Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) {
                        Text(stringResource(R.string.storage), style = MaterialTheme.typography.labelLarge)
                        LinearProgressIndicator(
                            progress = { pct / 100f },
                            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                        )
                        Text(
                            stringResource(R.string.storage_used, formatSize(cap.used), formatSize(cap.total)),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 4.dp),
                        )
                    }
                }
            }
            ListItem(
                modifier = Modifier.clickable(onClick = onOpenOffline),
                headlineContent = { Text(stringResource(R.string.offline_files)) },
                supportingContent = {
                    Text(
                        if (offlineCount == 0) stringResource(R.string.offline_files_hint)
                        else stringResource(R.string.offline_count, offlineCount)
                    )
                },
                leadingContent = { Icon(Icons.Default.OfflinePin, contentDescription = null) },
            )
            if (offlineCount > 0) {
                SwitchRow(
                    title = stringResource(R.string.offline_mobile_data),
                    subtitle = if (offlineMobileData) null else stringResource(R.string.offline_mobile_data_hint),
                    checked = offlineMobileData,
                    onCheckedChange = { on -> scope.launch { app.offlineManager.setMobileData(on) } },
                )
            }

            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            SectionLabel(stringResource(R.string.folder_sync))
            SwitchRow(
                title = stringResource(R.string.mirror_local_folder),
                subtitle = syncSnap?.treeUri?.let { stringResource(R.string.local_folder, it.toUri().lastPathSegment.orEmpty()) }
                    ?: stringResource(R.string.pick_device_folder),
                checked = syncSnap?.enabled == true && syncSnap?.treeUri != null,
                onCheckedChange = { want ->
                    if (want) treeLauncher.launch(null)
                    else scope.launch {
                        sync.setEnabled(false)
                        SyncFolderWorker.cancel(context)
                    }
                },
            )
            // Fylvo: the details only matter once a folder is mirrored.
            if (syncSnap?.enabled == true && syncSnap?.treeUri != null) {
                SwitchRow(
                    title = stringResource(R.string.wifi_only),
                    checked = syncSnap?.wifiOnly ?: true,
                    onCheckedChange = { wifiOnly ->
                        scope.launch {
                            sync.setWifiOnly(wifiOnly)
                            if (syncSnap?.enabled == true) SyncFolderWorker.apply(context, wifiOnly)
                        }
                    },
                )
                FolderField(stringResource(R.string.folder_in_my_files), syncFolder) { syncFolder = it }
                syncSnap?.lastSyncAt?.takeIf { it > 0 }?.let {
                    Text(
                        stringResource(R.string.last_folder_sync, formatDateTime(it)),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                    )
                }
                FilledTonalButton(
                    onClick = {
                        persistFolders()
                        if (syncSnap?.enabled == true) SyncFolderWorker.syncNow(context, syncSnap?.wifiOnly ?: true)
                    },
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                ) {
                    Icon(Icons.Default.Sync, contentDescription = null)
                    Text("  " + stringResource(R.string.sync_now))
                }

            }

            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            SectionLabel(stringResource(R.string.notifications))
            SwitchRow(
                title = stringResource(R.string.notify_finished_tasks),
                subtitle = stringResource(R.string.notify_finished_tasks_hint),
                checked = notifyEnabled,
                onCheckedChange = { want ->
                    when {
                        !want -> scope.launch {
                            notify.setEnabled(false)
                            TaskPollWorker.cancel(context)
                        }
                        android.os.Build.VERSION.SDK_INT >= 33 ->
                            notifPermLauncher.launch(android.Manifest.permission.POST_NOTIFICATIONS)
                        else -> scope.launch {
                            notify.setEnabled(true)
                            TaskPollWorker.apply(context)
                        }
                    }
                },
            )

            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            ListItem(
                modifier = Modifier.clickable { chooseLanguage = true },
                headlineContent = { Text(stringResource(R.string.language)) },
                supportingContent = { Text(stringResource(appLanguage.label)) },
                leadingContent = { Icon(Icons.Default.Language, contentDescription = null) },
            )
            UpdateRows(app.appUpdater)

            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            ListItem(
                modifier = Modifier.clickable { confirmSignOut = true },
                headlineContent = { Text(stringResource(R.string.sign_out), color = MaterialTheme.colorScheme.error) },
                leadingContent = {
                    Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = null, tint = MaterialTheme.colorScheme.error)
                },
            )
        }
    }

    if (confirmSignOut) {
        AlertDialog(
            onDismissRequest = { confirmSignOut = false },
            title = { Text(stringResource(R.string.sign_out_title)) },
            text = { Text(stringResource(R.string.sign_out_text)) },
            confirmButton = {
                TextButton(onClick = { confirmSignOut = false; signOut(OfflineEdits.SignOutChoice.None) }) {
                    Text(stringResource(R.string.sign_out), color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmSignOut = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }

    editSignOut?.let { prompt ->
        EditChoiceDialog(
            title = pluralStringResource(R.plurals.edit_unuploaded_title, prompt.ids.size, prompt.ids.size),
            text = stringResource(R.string.edit_sign_out_text),
            busy = prompt.busy,
            error = prompt.reason?.let { stringResource(R.string.edit_upload_failed, notUploadedText(it, prompt.code).asString()) },
            onUpload = { signOut(OfflineEdits.SignOutChoice.Upload) },
            onDiscard = { signOut(OfflineEdits.SignOutChoice.Discard) },
            onDismiss = { editSignOut = null },
        )
    }

    if (chooseLanguage) {
        AlertDialog(
            onDismissRequest = { chooseLanguage = false },
            title = { Text(stringResource(R.string.language)) },
            text = {
                Column {
                    AppLanguage.entries.forEach { language ->
                        ListItem(
                            modifier = Modifier.clickable {
                                chooseLanguage = false
                                // Restarts the screen in the new language when it changes.
                                if (language != appLanguage) {
                                    appLanguage = language
                                    language.apply()
                                }
                            },
                            headlineContent = { Text(stringResource(language.label)) },
                            leadingContent = { RadioButton(selected = language == appLanguage, onClick = null) },
                            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
                        )
                    }
                }
            },
            confirmButton = {},
            dismissButton = {
                TextButton(onClick = { chooseLanguage = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }
}

/**
 * Fylvo: the installed version (tap to check for updates) and, when the server offers a newer
 * release, a row to download and install it.
 */
@Composable
private fun UpdateRows(updater: AppUpdater) {
    val state by updater.state.collectAsState()
    LifecycleResumeEffect(updater) {
        updater.onResume()
        onPauseOrDispose { }
    }
    val status = when (state) {
        AppUpdater.State.Checking -> stringResource(R.string.update_checking)
        AppUpdater.State.UpToDate -> stringResource(R.string.update_up_to_date)
        AppUpdater.State.CheckFailed -> stringResource(R.string.update_check_failed)
        else -> null
    }
    ListItem(
        modifier = Modifier.clickable { updater.checkNow() },
        headlineContent = { Text(stringResource(R.string.app_version)) },
        supportingContent = { Text(listOfNotNull(BuildConfig.VERSION_NAME, status).joinToString(" · ")) },
        leadingContent = { Icon(Icons.Outlined.Info, contentDescription = null) },
        trailingContent = {
            if (state == AppUpdater.State.Idle) {
                TextButton(onClick = { updater.checkNow() }) { Text(stringResource(R.string.update_check)) }
            }
        },
    )
    val release = when (val s = state) {
        is AppUpdater.State.Available -> s.release
        is AppUpdater.State.Downloading -> s.release
        else -> return
    }
    val downloading = state as? AppUpdater.State.Downloading
    val error = (state as? AppUpdater.State.Available)?.error
    ListItem(
        headlineContent = { Text(stringResource(R.string.update_to, release.versionName)) },
        supportingContent = {
            when {
                downloading != null -> Text(stringResource(R.string.update_downloading, downloading.percent))
                error != null -> Text(stringResource(error), color = MaterialTheme.colorScheme.error)
                else -> Text(formatSize(release.size))
            }
        },
        leadingContent = { Icon(Icons.Default.SystemUpdate, contentDescription = null, tint = MaterialTheme.colorScheme.primary) },
        trailingContent = {
            FilledTonalButton(onClick = { updater.downloadAndInstall() }, enabled = downloading == null) {
                Text(stringResource(R.string.update_install))
            }
        },
    )
}

/** Name of the language in the settings row and the choice dialog. */
private val AppLanguage.label: Int
    get() = when (this) {
        AppLanguage.DEVICE -> R.string.language_device
        AppLanguage.ENGLISH -> R.string.language_english
        AppLanguage.GERMAN -> R.string.language_german
    }

@Composable
private fun SectionLabel(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 16.dp, top = 16.dp, bottom = 4.dp),
    )
}

@Composable
private fun SwitchRow(
    title: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    subtitle: String? = null,
) {
    ListItem(
        modifier = Modifier.clickable { onCheckedChange(!checked) },
        headlineContent = { Text(title) },
        supportingContent = subtitle?.let { { Text(it) } },
        trailingContent = { Switch(checked = checked, onCheckedChange = onCheckedChange) },
    )
}

@Composable
private fun FolderField(label: String, value: String, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label) },
        singleLine = true,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 4.dp),
    )
}

private fun formatDateTime(millis: Long): String =
    DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT).format(Date(millis))


/** Fylvo: the question before a sign-out with the edited files [ids] not uploaded; [reason] after a failed upload. */
private data class EditSignOut(
    val ids: Set<String>,
    val reason: OfflineEdits.NotUploaded? = null,
    /** The server's code of a refusal. */
    val code: Int = 0,
    val busy: Boolean = false,
)
