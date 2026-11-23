package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.AddLink
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.LinkOff
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ConnectPreview
import io.github.svenbuild.fylvo.android.api.ConnectedStorage
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.launch

/**
 * Fylvo 1.6: Account › connected storages (design/android-parity-plan.md, 14): open, rename,
 * connect again, disconnect, and connect a new one in the browser (Custom Tab) when the server lets
 * apps do that; otherwise the web app opens as before.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConnectedPage(
    viewModel: FilesViewModel,
    onBack: () -> Unit,
    bottomBar: @Composable () -> Unit,
    onOpen: (ConnectedStorage) -> Unit,
    onMessage: (String) -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    val state by viewModel.connected.state.collectAsState()
    val connectUi by app.connect.ui.collectAsState()
    var target by remember { mutableStateOf<ConnectedStorage?>(null) }
    var renaming by remember { mutableStateOf<ConnectedStorage?>(null) }
    var removing by remember { mutableStateOf<ConnectedStorage?>(null) }
    LaunchedEffect(Unit) { viewModel.connected.load() }
    val overview = state.overview

    fun connect(provider: String, reconnect: String?) {
        scope.launch {
            if (overview?.appSignin != true) {
                openInBrowser(context, viewModel.connected.webSettingsUrl(reconnect))
                return@launch
            }
            app.connect.start(provider, reconnect)?.let { ConnectController.openInTab(context, it) }
        }
    }

    PlacePage(stringResource(R.string.connected_storages), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            // Fylvo 1.6.1: every state says what is the matter (never an empty page).
            if (overview == null) {
                if (state.failed) LoadFailed(onRetry = { viewModel.connected.load() })
                else CircularProgressIndicator(Modifier.align(Alignment.Center))
                return@Box
            }
            val configured = overview.providers.any { it.configured }
            Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                if (state.failed) LoadFailed(onRetry = { viewModel.connected.load() })
                when {
                    !overview.available -> Notice(stringResource(R.string.connected_unavailable))
                    !overview.allowed -> Notice(stringResource(if (overview.storages.isEmpty()) R.string.connect_not_allowed else R.string.connected_not_allowed_with))
                    !configured -> Notice(stringResource(R.string.connected_no_provider))
                    overview.storages.isEmpty() -> Notice(stringResource(R.string.connected_empty))
                }
                overview.storages.forEach { s ->
                    ListItem(
                        modifier = Modifier.clickable { target = s },
                        headlineContent = { Text(CrUri.rootLabel(s.uri), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        supportingContent = {
                            Text(
                                when {
                                    s.needsReconnect -> stringResource(R.string.connected_status_reconnect)
                                    !s.ok -> stringResource(R.string.connected_status_unavailable)
                                    else -> s.account
                                },
                                color = if (s.ok) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error,
                            )
                        },
                        leadingContent = { Icon(if (s.ok) Icons.Outlined.Cloud else Icons.Outlined.CloudOff, contentDescription = null) },
                    )
                }
                if (overview.allowed && overview.storages.size < overview.max.coerceAtLeast(1)) {
                    overview.providers.filter { it.configured }.forEach { p ->
                        ListItem(
                            modifier = Modifier.clickable(enabled = connectUi == ConnectController.Ui.Idle || connectUi is ConnectController.Ui.Waiting) { connect(p.type, null) },
                            headlineContent = { Text(stringResource(R.string.connect_provider, p.name)) },
                            supportingContent = {
                                Text(stringResource(if (overview.appSignin) R.string.connect_in_browser else R.string.connected_connect_hint))
                            },
                            leadingContent = { Icon(Icons.Outlined.AddLink, contentDescription = null) },
                        )
                    }
                }
                if (connectUi is ConnectController.Ui.Waiting) {
                    ListItem(
                        headlineContent = { Text(stringResource(R.string.connect_waiting)) },
                        supportingContent = { Text(stringResource(R.string.connect_waiting_hint)) },
                        trailingContent = { TextButton(onClick = { app.connect.cancelAttempt() }) { Text(stringResource(R.string.cancel)) } },
                    )
                }
                Spacer(Modifier.height(96.dp))
            }
        }
    }

    target?.let { s ->
        ModalBottomSheet(onDismissRequest = { target = null }) {
            Text(CrUri.rootLabel(s.uri), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
            if (s.ok && overview?.allowed == true) SheetAction(Icons.Outlined.FolderOpen, stringResource(R.string.open), { target = null; onOpen(s) })
            // Without the right to connect only disconnect is left.
            if (overview?.allowed == true) SheetAction(Icons.Outlined.DriveFileRenameOutline, stringResource(R.string.rename), { target = null; renaming = s })
            if (overview?.allowed == true) SheetAction(
                if (overview?.appSignin == true) Icons.Outlined.Sync else Icons.AutoMirrored.Outlined.OpenInNew,
                stringResource(R.string.connect_again),
                { target = null; connect(s.provider, s.id) },
            )
            SheetAction(Icons.Outlined.LinkOff, stringResource(R.string.disconnect), { target = null; removing = s }, tint = MaterialTheme.colorScheme.error)
            Spacer(Modifier.height(24.dp))
        }
    }

    renaming?.let { s ->
        NameDialog(
            title = stringResource(R.string.rename),
            label = stringResource(R.string.name),
            confirm = stringResource(R.string.save),
            initial = s.name.ifEmpty { CrUri.rootLabel(s.uri) },
            onConfirm = { name ->
                renaming = null
                val trimmed = name.trim()
                // The server takes at most 64 bytes.
                if (trimmed.isEmpty() || trimmed.toByteArray().size > 64) {
                    onMessage(context.getString(R.string.connect_name_rule))
                    return@NameDialog
                }
                scope.launch {
                    runCatching { app.connectRepo.rename(s.id, trimmed) }
                        .onSuccess { viewModel.connected.reload() }
                        .onFailure { onMessage(userError(it).resolve(context)) }
                }
            },
            onDismiss = { renaming = null },
        )
    }

    removing?.let { s ->
        AlertDialog(
            onDismissRequest = { removing = null },
            title = { Text(stringResource(R.string.disconnect_title, CrUri.rootLabel(s.uri))) },
            text = { Text(stringResource(R.string.disconnect_text)) },
            confirmButton = {
                TextButton(onClick = {
                    removing = null
                    scope.launch {
                        // Off the main thread, and finished also when the page is left meanwhile.
                        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO + kotlinx.coroutines.NonCancellable) {
                            runCatching {
                                app.connectRepo.disconnect(s.id)
                                // Its previews leave the cache, and uploads waiting for it go too (each on its own).
                                io.github.svenbuild.fylvo.android.data.ConnectedThumbs.forget(context.applicationContext, io.github.svenbuild.fylvo.android.util.Connected.storageId(s.uri) ?: s.id)
                                runCatching { io.github.svenbuild.fylvo.android.work.UploadWorker.cancelForRoot(context.applicationContext, CrUri.root(s.uri)) }
                            }
                        }
                            .onSuccess { onMessage(context.getString(R.string.disconnected)); viewModel.connected.reload() }
                            .onFailure { onMessage(userError(it).resolve(context)) }
                    }
                }) { Text(stringResource(R.string.disconnect), color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { removing = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

/** Fylvo 1.6.1: one calm sentence about the state of the page. */
@Composable
private fun Notice(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(horizontal = 20.dp, vertical = 16.dp),
    )
}

/** Fylvo 1.6.1: the storages could not be read; a retry. */
@Composable
private fun LoadFailed(onRetry: () -> Unit) {
    Column(Modifier.padding(horizontal = 20.dp, vertical = 16.dp)) {
        Text(stringResource(R.string.connected_load_failed), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
        TextButton(onClick = onRetry, contentPadding = androidx.compose.foundation.layout.PaddingValues(0.dp)) { Text(stringResource(R.string.retry)) }
    }
}

/**
 * Fylvo 1.6: the question before a new storage is saved: which provider account and which Fylvo
 * account (the server's contract asks for both).
 */
@Composable
fun ConnectConfirmDialog(preview: ConnectPreview, myName: String, onAnswer: (Boolean) -> Unit) {
    val provider = when (preview.provider) {
        "drive" -> "Google Drive"
        "onedrive" -> "OneDrive"
        else -> preview.provider
    }
    val text = when (preview.accountKind) {
        "address" -> stringResource(R.string.connect_confirm_address, provider, preview.account, myName)
        "sign_in_name" -> stringResource(R.string.connect_confirm_sign_in, provider, preview.account, myName)
        "name" -> stringResource(R.string.connect_confirm_name, provider, preview.account, myName)
        else -> stringResource(R.string.connect_confirm_none, provider, myName)
    }
    AlertDialog(
        onDismissRequest = {},
        title = { Text(stringResource(R.string.connect_confirm_title, provider)) },
        text = { Text(text) },
        confirmButton = { TextButton(onClick = { onAnswer(true) }) { Text(stringResource(R.string.connect_yes)) } },
        dismissButton = { TextButton(onClick = { onAnswer(false) }) { Text(stringResource(R.string.connect_no)) } },
    )
}
