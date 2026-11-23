package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Send
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.FolderOpen
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Group
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.ShareInfo
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.launch
import java.time.OffsetDateTime

/**
 * Fylvo: what can be done with the account's shares, shared by the page "Shared by me" and the
 * sheet of one file's shares. [knownUri] is the shared item's address when it is already known
 * (from the file's own sheet); otherwise it is looked up when needed.
 */
class ShareActions(
    /** "Show in folder": opens [uri]. */
    val openFolder: (uri: String) -> Unit,
    /** A share with people opens its own sheet (`member_only`). */
    val editPeople: (share: ShareInfo, uri: String) -> Unit,
    val message: (String) -> Unit,
    /** Group right to share known and given: editing is offered. */
    val canEdit: Boolean,
)

/** Fylvo: the page "Shared by me": the account's shares, newest first. */
@Composable
fun SharesPage(
    onBack: (() -> Unit)?,
    bottomBar: @Composable () -> Unit,
    actions: ShareActions,
) {
    val app = LocalContext.current.applicationContext as FylvoApp
    val shares = remember { mutableStateListOf<ShareInfo>() }
    var next by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(true) }
    var failed by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }
    /** Fylvo: a page that belongs to an older load (before a reload) is dropped. */
    var generation by remember { mutableStateOf(0) }
    var moreFailed by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    suspend fun load(token: String?) {
        val mine = if (token == null) ++generation else generation
        // A reload forgets the old next page at once (nothing of the old list is asked for any more).
        if (token == null) next = null
        loading = true
        runCatching { app.shares.list(token) }
            .onSuccess { page ->
                if (mine != generation) return@onSuccess
                if (token == null) shares.clear()
                shares.addAll(page.shares.filter { s -> shares.none { it.id == s.id } })
                next = page.next
                failed = false
                moreFailed = false
            }
            .onFailure { if (mine == generation) { if (token == null) failed = true else moreFailed = true } }
        // A failed reload with rows on screen: the footer offers to try again.
        if (mine == generation && token == null && failed && shares.isNotEmpty()) next = null
        if (mine == generation) loading = false
    }
    LaunchedEffect(reload) { load(null) }

    PlacePage(stringResource(R.string.shared_by_me), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when {
                shares.isEmpty() && loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                shares.isEmpty() && failed -> EmptyState(
                    icon = Icons.Outlined.Link,
                    title = stringResource(R.string.shares_load_failed),
                    action = stringResource(R.string.retry) to { reload++ },
                )
                shares.isEmpty() -> EmptyState(
                    icon = Icons.Outlined.Link,
                    title = stringResource(R.string.no_shares),
                    detail = stringResource(R.string.no_shares_hint),
                )
                else -> ShareList(
                    shares = shares,
                    knownUri = null,
                    actions = actions,
                    onChanged = { reload++ },
                    next = next,
                    moreFailed = moreFailed || (failed && !loading),
                    onMore = { token -> scope.launch { load(token) } },
                    contentPadding = PaddingValues(bottom = 96.dp),
                )
            }
        }
    }
}

/** Fylvo: the shares of one file, from its own sheet ("Manage shares"). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileSharesSheet(file: FileObject, actions: ShareActions, onDismiss: () -> Unit) {
    val app = LocalContext.current.applicationContext as FylvoApp
    var shares by remember { mutableStateOf<List<ShareInfo>?>(null) }
    var reload by remember { mutableStateOf(0) }
    var failed by remember { mutableStateOf(false) }
    LaunchedEffect(reload) {
        runCatching { app.shares.sharesOf(file.path) }
            .onSuccess { shares = it; failed = false }
            .onFailure { failed = true }
    }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Text(
            stringResource(R.string.manage_shares),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
        )
        val list = shares
        when {
            list == null && failed -> Text(stringResource(R.string.shares_load_failed), modifier = Modifier.padding(20.dp))
            list == null -> Box(Modifier.fillMaxWidth().padding(24.dp)) { CircularProgressIndicator(Modifier.align(Alignment.Center)) }
            list.isEmpty() -> Text(stringResource(R.string.no_shares), modifier = Modifier.padding(20.dp))
            else -> ShareList(list, knownUri = file.path, actions = actions, onChanged = { reload++ })
        }
        Spacer(Modifier.height(24.dp))
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ShareList(
    shares: List<ShareInfo>,
    knownUri: String?,
    actions: ShareActions,
    onChanged: () -> Unit,
    /** The token of the next page (null: none), loaded when the list end is reached. */
    next: String? = null,
    moreFailed: Boolean = false,
    onMore: (String) -> Unit = {},
    contentPadding: PaddingValues = PaddingValues(0.dp),
) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    var target by remember { mutableStateOf<ShareInfo?>(null) }
    var editing by remember { mutableStateOf<Pair<ShareInfo, String>?>(null) }
    var deleting by remember { mutableStateOf<ShareInfo?>(null) }
    var busy by remember { mutableStateOf(false) }

    /** The shared item's address: known from the file, or looked up (fails for expired shares). */
    fun withSource(share: ShareInfo, then: (String) -> Unit) {
        knownUri?.let { return then(it) }
        busy = true
        scope.launch {
            runCatching { app.shares.sourceOf(share) }
                .onSuccess(then)
                .onFailure { actions.message(userError(it).resolve(context)) }
            busy = false
        }
    }

    LazyColumn(Modifier.fillMaxWidth(), contentPadding = contentPadding) {
        items(shares, key = { it.id }) { share ->
            ShareRow(share) { target = share }
        }
        if (next != null) item(key = "more") {
            // Each page token is asked for once; after a failure a tap asks again.
            if (moreFailed) {
                androidx.compose.material3.TextButton(onClick = { onMore(next) }, modifier = Modifier.padding(horizontal = 12.dp)) {
                    Text(stringResource(R.string.retry))
                }
            } else {
                LaunchedEffect(next) { onMore(next) }
                Box(Modifier.fillMaxWidth().padding(16.dp)) { CircularProgressIndicator(Modifier.size(24.dp).align(Alignment.Center)) }
            }
        }
    }

    target?.let { share ->
        ModalBottomSheet(onDismissRequest = { target = null }) {
            Text(
                share.name,
                style = MaterialTheme.typography.titleMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
            )
            HorizontalDivider(Modifier.padding(vertical = 4.dp))
            val link = share.url.takeIf { !share.memberOnly && !share.expired && it.isNotEmpty() }
            if (link != null) {
                SheetAction(Icons.Default.ContentCopy, stringResource(R.string.copy_link), {
                    copySensitive(context, context.getString(R.string.share_link), link)
                    actions.message(context.getString(R.string.link_copied))
                    target = null
                })
                SheetAction(Icons.AutoMirrored.Outlined.Send, stringResource(R.string.send_link), {
                    sendLink(context, share.name, link)
                    target = null
                })
            }
            // An expired share has no address any more on the server's side; it can be switched
            // on again from the file itself.
            if (actions.canEdit && (!share.expired || knownUri != null)) {
                val label = if (share.memberOnly) R.string.share_edit_people else if (share.expired) R.string.share_edit_reactivate else R.string.share_edit_link
                SheetAction(if (share.memberOnly) Icons.Outlined.Group else Icons.Outlined.Edit, stringResource(label), {
                    target = null
                    withSource(share) { uri -> if (share.memberOnly) actions.editPeople(share, uri) else editing = share to uri }
                })
            }
            if (!share.expired && knownUri == null) {
                SheetAction(Icons.Default.FolderOpen, stringResource(R.string.show_in_folder), {
                    target = null
                    withSource(share) { uri -> actions.openFolder(app.shares.folderOf(share, uri)) }
                })
            }
            if (share.expired && knownUri == null) {
                Text(
                    stringResource(R.string.share_reactivate_from_file),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                )
            }
            SheetAction(Icons.Default.Delete, stringResource(R.string.delete_share), { target = null; deleting = share }, tint = MaterialTheme.colorScheme.error)
            Spacer(Modifier.height(24.dp))
        }
    }

    editing?.let { (share, uri) ->
        ShareLinkSheet(
            name = share.name,
            isFolder = share.isFolder,
            thumb = { FileThumb(shareAsFile(share), null, size = 48.dp) },
            existing = share,
            save = { options -> app.shares.edit(share, uri, options).also { onChanged() } },
            onDismiss = { editing = null },
            onMessage = actions.message,
        )
    }

    deleting?.let { share ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text(stringResource(R.string.delete_share_title)) },
            text = { Text(stringResource(if (share.memberOnly) R.string.delete_share_people_text else R.string.delete_share_text, share.name)) },
            confirmButton = {
                TextButton(enabled = !busy, onClick = {
                    busy = true
                    scope.launch {
                        runCatching { app.shares.delete(share.id) }
                            .onSuccess { actions.message(context.getString(R.string.share_deleted)); onChanged() }
                            .onFailure { actions.message(userError(it).resolve(context)) }
                        busy = false
                        deleting = null
                    }
                }) { Text(stringResource(R.string.delete), color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

/** A share as a file row for its icon. */
private fun shareAsFile(share: ShareInfo) = FileObject(type = if (share.isFolder) 1 else 0, name = share.name)

@Composable
private fun ShareRow(share: ShareInfo, onClick: () -> Unit) {
    val faded = MaterialTheme.colorScheme.onSurfaceVariant
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        FileThumb(shareAsFile(share), null, size = 40.dp)
        Column(Modifier.weight(1f).padding(start = 16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    share.name,
                    style = MaterialTheme.typography.bodyLarge,
                    color = if (share.expired) faded else MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (share.passwordProtected || share.password.isNotEmpty()) {
                    Icon(Icons.Outlined.Lock, contentDescription = stringResource(R.string.share_password_protected), tint = faded, modifier = Modifier.padding(start = 6.dp).size(14.dp))
                }
            }
            Text(shareMeta(share), style = MaterialTheme.typography.bodySmall, color = faded, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** "until 12.12.2026 · 14 views · 3 downloads", "Expired", "With 2 people". */
@Composable
private fun shareMeta(share: ShareInfo): String {
    val parts = mutableListOf<String>()
    when {
        share.expired -> parts += stringResource(R.string.share_expired)
        share.memberOnly -> parts += pluralStringResource(R.plurals.share_people, share.members.size, share.members.size)
    }
    if (!share.expired) share.expires?.let { iso ->
        runCatching { lastDayText(OffsetDateTime.parse(iso).toInstant()) }
            .getOrNull()?.let { parts += stringResource(R.string.share_until, it) }
    }
    if (!share.memberOnly) {
        parts += pluralStringResource(R.plurals.share_views, share.visited, share.visited)
        parts += pluralStringResource(R.plurals.share_downloads, share.downloaded, share.downloaded)
        share.remainDownloads?.let { parts += pluralStringResource(R.plurals.share_downloads_left, it, it) }
    }
    return parts.joinToString(" · ")
}
