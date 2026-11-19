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
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import kotlinx.coroutines.launch
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.api.CapacityResponse
import io.github.svenbuild.fylvo.android.ui.files.formatSize
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
    onSignOut: () -> Unit,
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

    Scaffold(
        topBar = {
            TopAppBar(title = { Text("Account") })
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
                headlineContent = { Text(email.ifEmpty { "Signed in" }) },
                supportingContent = { Text(serverUrl) },
                leadingContent = { Icon(Icons.Default.AccountCircle, contentDescription = null) },
            )
            capacity?.let { cap ->
                if (cap.total > 0) {
                    val pct = (cap.used.toDouble() / cap.total * 100).toInt().coerceIn(0, 100)
                    Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) {
                        Text("Storage", style = MaterialTheme.typography.labelLarge)
                        LinearProgressIndicator(
                            progress = { pct / 100f },
                            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                        )
                        Text(
                            "${formatSize(cap.used)} of ${formatSize(cap.total)} used",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 4.dp),
                        )
                    }
                }
            }
            ListItem(
                modifier = Modifier.clickable(onClick = onOpenOffline),
                headlineContent = { Text("Offline files") },
                supportingContent = {
                    Text(if (offlineCount == 0) "Files you keep on this phone" else "$offlineCount on this phone")
                },
                leadingContent = { Icon(Icons.Default.OfflinePin, contentDescription = null) },
            )

            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            SectionLabel("Folder sync")
            SwitchRow(
                title = "Mirror a local folder",
                subtitle = syncSnap?.treeUri?.let { "Local: " + it.toUri().lastPathSegment.orEmpty() }
                    ?: "Pick a device folder to keep uploaded",
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
                    title = "Wi-Fi only",
                    checked = syncSnap?.wifiOnly ?: true,
                    onCheckedChange = { wifiOnly ->
                        scope.launch {
                            sync.setWifiOnly(wifiOnly)
                            if (syncSnap?.enabled == true) SyncFolderWorker.apply(context, wifiOnly)
                        }
                    },
                )
                FolderField("Folder in My files", syncFolder) { syncFolder = it }
                syncSnap?.lastSyncAt?.takeIf { it > 0 }?.let {
                    Text(
                        "Last folder sync ${formatDateTime(it)}",
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
                    Text("  Sync now")
                }

            }

            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            SectionLabel("Notifications")
            SwitchRow(
                title = "Notify on finished tasks",
                subtitle = "When a job on the server finishes",
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
                modifier = Modifier.clickable { confirmSignOut = true },
                headlineContent = { Text("Sign out", color = MaterialTheme.colorScheme.error) },
                leadingContent = {
                    Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = null, tint = MaterialTheme.colorScheme.error)
                },
            )
        }
    }

    if (confirmSignOut) {
        AlertDialog(
            onDismissRequest = { confirmSignOut = false },
            title = { Text("Sign out?") },
            text = { Text("You will need to sign in again to sync your files.") },
            confirmButton = {
                TextButton(onClick = { confirmSignOut = false; onSignOut() }) {
                    Text("Sign out", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmSignOut = false }) { Text("Cancel") }
            },
        )
    }
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

