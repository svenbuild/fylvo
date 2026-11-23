package io.github.svenbuild.fylvo.android.ui.files

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.clickable
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.automirrored.filled.DriveFileMove
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.AddLink
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Language
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalContext
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.CapacityResponse
import io.github.svenbuild.fylvo.android.api.ConnectedOverview
import io.github.svenbuild.fylvo.android.api.ConnectedStorage
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.ConnectedRepository
import io.github.svenbuild.fylvo.android.data.OverviewResult
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Fylvo: usage of a storage is asked again after this. */
private const val CAPACITY_TTL_MS = 5L * 60 * 1000

/**
 * Fylvo: the account's connected storages for the app (owned by FilesViewModel). A server
 * without the feature gives no overview; a failure keeps the one shown before.
 */
class ConnectedStorages(
    private val repo: ConnectedRepository,
    private val scope: CoroutineScope,
    private val onSessionExpired: suspend () -> Unit,
) {
    data class State(
        val overview: ConnectedOverview? = null,
        /** Usage by storage id, only for storages that answered. */
        val capacity: Map<String, CapacityResponse> = emptyMap(),
        /** Fylvo 1.6.1: the last load failed (an overview shown before stays). */
        val failed: Boolean = false,
        /** Fylvo 1.6.1: the server has no connected storages at all (an older server). */
        val missing: Boolean = false,
        /** Fylvo 1.6.1: a load is under way. */
        val loading: Boolean = false,
    ) {
        /**
         * Fylvo 1.6.1: Account shows the storages page like the web app shows its tab: when the
         * account can connect one (a provider is set up), has storages, or the load failed (so
         * its retry can be reached).
         */
        val pageWanted: Boolean get() = failed || overview?.let {
            (it.available && it.allowed && it.providers.any { p -> p.configured }) || it.storages.isNotEmpty()
        } == true

        /** Shown on Home: the feature is there and this account may use it. */
        val visible: Boolean get() = overview != null && overview.available && overview.allowed &&
            (overview.storages.isNotEmpty() || overview.providers.any { it.configured })
        val storages: List<ConnectedStorage> get() = if (visible) overview!!.storages else emptyList()
    }

    private val _state = MutableStateFlow(State())
    val state: StateFlow<State> = _state

    private var loading: Job? = null
    private val capacityAt = HashMap<String, Long>()

    /** Loads the overview again (Home, pull to refresh, back in the foreground); one at a time. */
    fun load(): Job {
        loading?.takeIf { it.isActive }?.let { return it }
        return scope.launch { fetch() }.also { loading = it }
    }

    /** Like [load], but waits for the answer (sharing a request already under way). */
    suspend fun reload() = load().join()

    private suspend fun fetch() {
        _state.update { it.copy(loading = true) }
        try {
            fetchOnce()
        } finally {
            _state.update { it.copy(loading = false) }
        }
    }

    private suspend fun fetchOnce() {
        val result = try {
            repo.overview()
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            if (e.code == ApiException.CODE_LOGIN_REQUIRED) onSessionExpired()
            else _state.update { it.copy(failed = true) }
            return
        }
        when (result) {
            is OverviewResult.Found -> {
                Connected.nameStorages(result.overview.storages)
                _state.update { it.copy(overview = result.overview, failed = false, missing = false) }
                loadCapacities()
            }
            OverviewResult.Missing -> {
                Connected.nameStorages(emptyList())
                _state.value = State(missing = true)
            }
            OverviewResult.Failed -> _state.update { it.copy(failed = true) }
        }
    }

    /** The storage [uri] belongs to, as the last overview describes it. */
    fun storageOf(uri: String): ConnectedStorage? {
        val root = CrUri.root(uri)
        return _state.value.overview?.storages?.firstOrNull { CrUri.root(it.uri) == root }
    }

    suspend fun webSettingsUrl(reconnect: String? = null): String = repo.webSettingsUrl(reconnect)

    private fun loadCapacities() {
        val now = System.currentTimeMillis()
        _state.value.storages.filter { it.ok }.forEach { s ->
            if (now - (capacityAt[s.id] ?: 0L) < CAPACITY_TTL_MS) return@forEach
            capacityAt[s.id] = now
            scope.launch {
                val capacity = repo.capacity(s.id)
                if (capacity == null) {
                    // An old figure is not shown as if it were current.
                    capacityAt.remove(s.id)
                    _state.update { it.copy(capacity = it.capacity - s.id) }
                } else _state.update { it.copy(capacity = it.capacity + (s.id to capacity)) }
            }
        }
    }
}

/** Fylvo: opens [url] in the browser; quietly nothing when no browser is there. */
fun openInBrowser(context: Context, url: String) {
    try {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (_: ActivityNotFoundException) {
    }
}

/**
 * Fylvo 1.6.3: opens [url] of a provider (a document that exists only there, "open at the
 * provider") in the provider's own app when one is installed that takes the address (Google
 * Docs, Sheets, Slides, Drive; the Microsoft apps), else in the browser as before.
 *
 * A plain VIEW goes to the browser on Android 12 and later unless the app's links are verified
 * or the user allowed them, which the provider apps often are not. So the app is chosen here:
 * the apps that take this address, without those that take any web address (browsers) and
 * without Fylvo; one is opened directly, several are offered to choose from, none: browser.
 * No list of package names.
 */
fun openAtProvider(context: Context, url: String) {
    val uri = Uri.parse(url)
    val apps = runCatching { providerApps(context, uri) }.getOrDefault(emptyList())
    val intents = apps.map { a ->
        Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE).setClassName(a.packageName, a.name).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    try {
        when (intents.size) {
            0 -> return openInBrowser(context, url)
            1 -> context.startActivity(intents[0])
            else -> context.startActivity(
                Intent.createChooser(intents[0], null)
                    .putExtra(Intent.EXTRA_INITIAL_INTENTS, intents.drop(1).toTypedArray())
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        }
    } catch (_: ActivityNotFoundException) {
        openInBrowser(context, url)
    } catch (_: SecurityException) {
        openInBrowser(context, url)
    }
}

/** The activities other than browsers and Fylvo that take [uri] (see [openAtProvider]). */
private fun providerApps(context: Context, uri: Uri): List<android.content.pm.ActivityInfo> {
    val pm = context.packageManager
    fun handlers(u: Uri) = pm.queryIntentActivities(
        Intent(Intent.ACTION_VIEW, u).addCategory(Intent.CATEGORY_BROWSABLE),
        android.content.pm.PackageManager.MATCH_ALL,
    ).map { it.activityInfo }
    // A browser takes any web address, also one no app could claim.
    val browsers = handlers(Uri.parse("https://example.invalid/")).map { it.packageName }.toSet()
    return handlers(uri).filter { it.packageName !in browsers && it.packageName != context.packageName && it.exported }
        .distinctBy { it.packageName to it.name }
}

/**
 * Fylvo: the section "Connected storages" (on Home today; the customisation work may place it
 * elsewhere, one row per storage). Reloads when the app comes back to the foreground, so a
 * storage connected again in the web app shows as usable.
 */
@Composable
fun ConnectedSection(
    state: ConnectedStorages.State,
    onOpen: (ConnectedStorage) -> Unit,
    onOpenWeb: (reconnect: String?) -> Unit,
    onResume: () -> Unit,
) {
    LifecycleResumeEffect(Unit) {
        onResume()
        onPauseOrDispose { }
    }
    if (!state.visible) return
    var troubled by remember { mutableStateOf<ConnectedStorage?>(null) }

    Text(
        stringResource(R.string.connected_storages),
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 20.dp, end = 12.dp, top = 20.dp, bottom = 2.dp),
    )
    if (state.storages.isEmpty()) {
        ListItem(
            modifier = Modifier.clickable { onOpenWeb(null) },
            headlineContent = { Text(stringResource(R.string.connected_connect_title)) },
            supportingContent = { Text(stringResource(R.string.connected_connect_hint)) },
            leadingContent = { Icon(Icons.Outlined.AddLink, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant) },
            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        )
    }
    state.storages.forEach { s ->
        val label = CrUri.rootLabel(s.uri)
        val detail = when {
            s.needsReconnect -> stringResource(R.string.connected_status_reconnect)
            !s.ok -> stringResource(R.string.connected_status_unavailable)
            else -> listOfNotNull(s.account.takeIf { it.isNotEmpty() && !label.contains(it) }, usageText(state.capacity[s.id]))
                .joinToString(" · ")
        }
        ListItem(
            modifier = Modifier.clickable { if (s.ok) onOpen(s) else troubled = s },
            headlineContent = { Text(label, maxLines = 1, overflow = TextOverflow.Ellipsis) },
            supportingContent = if (detail.isEmpty()) null else ({
                Text(
                    detail,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    color = if (s.ok) Color.Unspecified else MaterialTheme.colorScheme.error,
                )
            }),
            leadingContent = {
                // Fylvo 1.6.2: the provider's logo; a storage in trouble keeps the crossed-out cloud.
                if (s.ok) StorageIcon(s.provider, Icons.Outlined.Cloud, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                else Icon(Icons.Outlined.CloudOff, contentDescription = null, tint = MaterialTheme.colorScheme.error)
            },
            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        )
    }

    troubled?.let { s ->
        ConnectedTroubleDialog(
            storage = s,
            onOpenWeb = { troubled = null; onOpenWeb(s.id) },
            onDismiss = { troubled = null },
        )
    }
}

/** Fylvo: [ConnectedSection] wired to the files screen: a storage opens as a folder, the web app in the browser. */
@Composable
fun ConnectedHomeSection(viewModel: FilesViewModel) {
    val state by viewModel.connected.state.collectAsState()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    ConnectedSection(
        state = state,
        onOpen = { viewModel.openPlace(it.uri, origin = ORIGIN_HOME) },
        onOpenWeb = { id ->
            scope.launch {
                val app = context.applicationContext as io.github.svenbuild.fylvo.android.FylvoApp
                val overview = state.overview
                // Fylvo 1.6: in the app when the server allows it (a reconnect of that storage, or the first provider).
                val provider = id?.let { s -> overview?.storages?.firstOrNull { it.id == s }?.provider }
                    ?: overview?.providers?.firstOrNull { it.configured }?.type
                if (overview?.appSignin == true && provider != null && (id != null || overview.providers.count { it.configured } == 1)) {
                    app.connect.start(provider, id)?.let { ConnectController.openInTab(context, it) }
                } else openInBrowser(context, viewModel.connected.webSettingsUrl(id))
            }
        },
        onResume = { viewModel.connected.load() },
    )
}

@Composable
private fun usageText(capacity: CapacityResponse?): String? = when {
    capacity == null -> null
    capacity.total > 0 -> stringResource(R.string.connected_usage, formatSize(capacity.used), formatSize(capacity.total))
    else -> stringResource(R.string.connected_usage_only, formatSize(capacity.used))
}

/** Fylvo: a storage that needs to be connected again (or cannot be used): it is done in the web app. */
@Composable
fun ConnectedTroubleDialog(storage: ConnectedStorage, onOpenWeb: () -> Unit, onDismiss: () -> Unit) {
    val label = CrUri.rootLabel(storage.uri)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (storage.needsReconnect) stringResource(R.string.connected_reconnect_title, label) else label) },
        text = { Text(stringResource(if (storage.needsReconnect) R.string.connected_reconnect_text else R.string.connected_unavailable_text)) },
        confirmButton = { TextButton(onClick = onOpenWeb) { Text(stringResource(R.string.connected_open_web_app)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.close)) } },
    )
}

/** Fylvo: one line above a connected listing when the server left entries out. */
@Composable
fun ConnectedBanner(omitted: Int) {
    if (omitted <= 0) return
    Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Outlined.Info, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(end = 12.dp))
        Text(
            pluralStringResource(R.plurals.connected_omitted, omitted, omitted),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** Fylvo: the row's second line inside a connected storage. */
@Composable
fun connectedSubtitle(file: FileObject): String {
    val date = if (Connected.knownTime(file.updatedAt)) formatDate(file.updatedAt) else ""
    return when {
        Connected.isUnusable(file) -> stringResource(R.string.connected_duplicate_subtitle)
        Connected.isLink(file) -> listOf(stringResource(R.string.connected_link), date).filter { it.isNotEmpty() }.joinToString(" · ")
        file.isFolder || Connected.sizeUnknown(file) -> date
        date.isEmpty() -> formatSize(file.size)
        else -> subtitleFor(file.size, file.updatedAt, false)
    }
}

/**
 * Fylvo: the sheet of a row inside a connected storage. Offers only what the row's bits allow
 * allow (reading, and writing as the bits of util/Connected say).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConnectedFileSheet(
    file: FileObject,
    onDismiss: () -> Unit,
    onEnter: () -> Unit,
    onOpen: () -> Unit,
    onOpenExternal: (String) -> Unit,
    onDownload: () -> Unit,
    onDetails: () -> Unit,
    onRename: () -> Unit = {},
    onMove: () -> Unit = {},
    onDelete: () -> Unit = {},
) {
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        // Scrolls when the actions do not fit (landscape).
        Column(Modifier.verticalScroll(rememberScrollState())) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                FileThumb(file, null, size = 56.dp)
                Column(Modifier.padding(start = 16.dp)) {
                    Text(file.name, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(connectedSubtitle(file), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            val note = when {
                Connected.isUnusable(file) -> stringResource(R.string.connected_duplicate_note)
                Connected.isLink(file) && Connected.readOnly(file) -> stringResource(R.string.connected_link_note)
                Connected.isLink(file) -> stringResource(R.string.connected_link)
                !file.isFolder && !Connected.canDownload(file) && Connected.externalUrl(file) == null -> stringResource(R.string.connected_not_downloadable)
                else -> null
            }
            if (note != null) {
                Text(
                    note,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                )
            }
            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            if (Connected.canEnter(file)) SheetRow(Icons.Outlined.FolderOpen, stringResource(R.string.open_folder), onEnter)
            Connected.externalUrl(file)?.takeIf { !Connected.isUnusable(file) }?.let { url ->
                SheetRow(Icons.Outlined.Language, stringResource(R.string.connected_open_at_provider), onClick = { onOpenExternal(url) })
            }
            if (Connected.canDownload(file)) {
                val export = Connected.exportExt(file)
                SheetRow(
                    Icons.AutoMirrored.Filled.OpenInNew,
                    if (export != null) stringResource(R.string.connected_open_office, export) else stringResource(R.string.open),
                    onOpen,
                )
                SheetRow(Icons.Default.Download, stringResource(R.string.download), onDownload)
            }
            // Writing as the row's bits allow (move stays inside the storage, delete goes to the provider's trash).
            if (Connected.canRename(file)) {
                SheetRow(Icons.AutoMirrored.Filled.DriveFileMove, stringResource(R.string.move_to), onMove)
                SheetRow(Icons.Default.DriveFileRenameOutline, stringResource(R.string.rename), onRename)
            }
            if (Connected.canInfo(file)) SheetRow(Icons.Outlined.Info, stringResource(R.string.details), onDetails)
            if (Connected.canDelete(file)) SheetRow(Icons.Default.Delete, stringResource(R.string.connected_delete), onDelete, error = true)
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun SheetRow(icon: ImageVector, label: String, onClick: () -> Unit, error: Boolean = false) {
    val tint = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant
    ListItem(
        modifier = Modifier.clickable(onClick = onClick),
        headlineContent = { Text(label, color = if (error) tint else Color.Unspecified) },
        leadingContent = { Icon(icon, contentDescription = null, tint = tint) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
}

/**
 * Fylvo: the selection button inside a connected storage: each action only when every selected
 * row allows it (download, move, delete; rename and details for one). No copy.
 */
fun connectedSelectionActions(selected: List<FileObject>, count: Int): SelectionActions {
    val one = selected.singleOrNull()?.takeIf { count == 1 }
    val all = selected.size == count
    return SelectionActions(
        quick = listOfNotNull(
            SelectionAction.MOVE.takeIf { all && selected.all(Connected::canRename) },
            SelectionAction.DOWNLOAD.takeIf { all && selected.all(Connected::canDownload) },
            SelectionAction.DELETE.takeIf { all && selected.all(Connected::canDelete) },
        ),
        menu = listOfNotNull(
            SelectionAction.SELECT_ALL,
            SelectionAction.DETAILS.takeIf { one != null && Connected.canInfo(one) },
            SelectionAction.RENAME.takeIf { one != null && Connected.canRename(one) },
        ),
    )
}

/** Fylvo: the text of the delete question inside a connected storage: it goes to the provider's trash. */
@Composable
fun connectedDeleteNote(path: String): String? =
    if (Connected.isConnected(path)) stringResource(R.string.connected_delete_note, CrUri.rootLabel(path)) else null
