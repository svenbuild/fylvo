package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.PersonAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ShareInfo
import io.github.svenbuild.fylvo.android.api.ShareMemberDto
import io.github.svenbuild.fylvo.android.api.User
import io.github.svenbuild.fylvo.android.data.LinkOptions
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Fylvo 1.6: a folder to share with people, and its share with people when it has one. */
data class PeopleTarget(val uri: String, val share: ShareInfo?)

/** One person of a share: [name] as far as known. */
private data class Person(val id: String, val name: String, val canEdit: Boolean)

/**
 * Fylvo 1.6: share a folder with chosen people, each reading or editing
 * (design/android-parity-plan.md, 12). Saving sends the whole list; the share's other settings
 * are sent as they are (the server replaces them all). Everyone removed: sharing ends.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeopleSheet(target: PeopleTarget, onDone: (String?) -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    val myId = app.account.state.value.me?.id
    var share by remember { mutableStateOf(target.share) }
    var loading by remember { mutableStateOf(true) }
    val people = remember { mutableStateListOf<Person>() }
    var query by remember { mutableStateOf("") }
    var found by remember { mutableStateOf<List<User>>(emptyList()) }
    var saving by remember { mutableStateOf(false) }
    var askEnd by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    // An expired share must get a new end (sending none would make it last for ever).
    var expireSeconds by remember { mutableStateOf<Long?>(null) }
    var expireToChoose by remember { mutableStateOf(false) }
    var expireChanged by remember { mutableStateOf(false) }
    // The existing share could not be read: saving might make a second one, so nothing is offered but a retry.
    var loadFailed by remember { mutableStateOf(false) }
    var loadRound by remember { mutableStateOf(0) }

    LaunchedEffect(target, loadRound) {
        loading = true
        loadFailed = false
        error = null
        runCatching {
            val s = target.share ?: app.shares.sharesOf(target.uri).firstOrNull { it.memberOnly }
            share = s
            s?.let { LinkOptions.of(it) }?.let { expireSeconds = it.expireSeconds; expireToChoose = it.expireToChoose }
            // The people picked here stay when there is no share yet (a retry after a failed save).
            if (s != null || people.isEmpty()) people.clear()
            s?.members.orEmpty().forEach { m ->
                val name = runCatching { app.shares.user(m.userId).nickname }.getOrNull().orEmpty()
                people += Person(m.userId, name, m.canEdit)
            }
        }.onFailure { error = userError(it).resolve(context); loadFailed = true }
        loading = false
    }
    LaunchedEffect(query) {
        found = emptyList()
        if (query.trim().length < 2) return@LaunchedEffect
        delay(300)
        found = runCatching { app.shares.searchUsers(query.trim()) }.getOrDefault(emptyList())
            .filter { u -> u.id != myId && people.none { it.id == u.id } }
    }

    fun save() {
        if (people.isEmpty()) {
            if (share != null) askEnd = true else onDismiss()
            return
        }
        saving = true
        scope.launch {
            runCatching { app.shares.savePeople(target.uri, share, people.map { ShareMemberDto(it.id, it.canEdit) }, expireSeconds, expireChanged) }
                .onSuccess { onDone(context.getString(R.string.people_saved)) }
                .onFailure {
                    if (it is io.github.svenbuild.fylvo.android.data.ShareEndPassed) expireToChoose = true
                    else {
                        error = userError(it).resolve(context)
                        // A new share whose answer was lost may exist anyway: the next save edits it
                        // (never a second share). When that cannot be told, only a retry is offered.
                        // Without a clear refusal the first request may still be running at the server:
                        // only a fresh read (Retry) is offered, never a second save at once.
                        if (share == null) {
                            val clear = (it as? io.github.svenbuild.fylvo.android.data.ApiException)?.let { e -> !e.isTransient && e.code > 0 } == true
                            runCatching { app.shares.sharesOf(target.uri).firstOrNull { s -> s.memberOnly } }
                                .onSuccess { found -> share = found; if (found == null && !clear) loadFailed = true }
                                .onFailure { loadFailed = true }
                        }
                    }
                }
            saving = false
        }
    }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.verticalScroll(rememberScrollState())) {
            Text(stringResource(R.string.share_with_people), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp))
            Text(CrUri.displayName(target.uri), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(horizontal = 20.dp))
            HorizontalDivider(Modifier.padding(vertical = 8.dp))
            if (loading) {
                Box(Modifier.fillMaxWidth().padding(24.dp)) { CircularProgressIndicator(Modifier.align(Alignment.Center)) }
            } else if (loadFailed) {
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 20.dp)) }
                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 16.dp), horizontalArrangement = androidx.compose.foundation.layout.Arrangement.End) {
                    Button(onClick = { loadRound++ }) { Text(stringResource(R.string.retry)) }
                }
            } else {
                people.forEachIndexed { index, p ->
                    ListItem(
                        headlineContent = { Text(p.name.ifEmpty { stringResource(R.string.people_unknown) }, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        supportingContent = {
                            Row(horizontalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(8.dp)) {
                                FilterChip(selected = !p.canEdit, onClick = { people[index] = p.copy(canEdit = false) }, label = { Text(stringResource(R.string.people_read)) })
                                FilterChip(selected = p.canEdit, onClick = { people[index] = p.copy(canEdit = true) }, label = { Text(stringResource(R.string.people_edit)) })
                            }
                        },
                        trailingContent = {
                            IconButton(onClick = { people.removeAt(index) }) { Icon(Icons.Outlined.Close, contentDescription = stringResource(R.string.people_remove)) }
                        },
                        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
                    )
                }
                if (people.isEmpty()) Text(stringResource(R.string.people_none), style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
                OutlinedTextField(
                    value = query,
                    onValueChange = { query = it.take(64) },
                    label = { Text(stringResource(R.string.people_add)) },
                    leadingIcon = { Icon(Icons.Outlined.PersonAdd, contentDescription = null) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp),
                )
                found.forEach { u ->
                    ListItem(
                        modifier = Modifier.clickable {
                            people += Person(u.id, u.nickname, canEdit = false)
                            query = ""
                        },
                        headlineContent = { Text(u.nickname.ifEmpty { u.id }) },
                        supportingContent = { if (u.email.isNotEmpty()) Text(u.email) },
                        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
                    )
                }
                if (expireToChoose) {
                    Text(stringResource(R.string.people_expired), style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 20.dp, vertical = 6.dp))
                    Row(Modifier.padding(horizontal = 20.dp), horizontalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(8.dp)) {
                        listOf(0L to R.string.never, 7L to R.string.search_7_days, 30L to R.string.search_30_days).forEach { (days, label) ->
                            FilterChip(selected = false, onClick = { expireSeconds = (days * 86_400).takeIf { it > 0 }; expireToChoose = false; expireChanged = true },
                                label = { Text(stringResource(label)) })
                        }
                    }
                }
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 20.dp)) }
                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 16.dp), horizontalArrangement = androidx.compose.foundation.layout.Arrangement.End) {
                    if (share != null) TextButton(enabled = !saving, onClick = { askEnd = true }) {
                        Text(stringResource(R.string.people_end), color = MaterialTheme.colorScheme.error)
                    }
                    Spacer(Modifier.weight(1f))
                    Button(enabled = !saving && !expireToChoose && (people.isNotEmpty() || share != null), onClick = ::save) {
                        if (saving) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.save))
                    }
                }
            }
            Spacer(Modifier.height(12.dp))
        }
    }

    if (askEnd) {
        AlertDialog(
            onDismissRequest = { askEnd = false },
            title = { Text(stringResource(R.string.people_end_title)) },
            text = { Text(stringResource(R.string.delete_share_people_text, CrUri.displayName(target.uri))) },
            confirmButton = {
                TextButton(onClick = {
                    askEnd = false
                    val s = share ?: return@TextButton
                    scope.launch {
                        runCatching { app.shares.delete(s.id) }
                            .onSuccess { onDone(context.getString(R.string.share_deleted)) }
                            .onFailure { error = userError(it).resolve(context) }
                    }
                }) { Text(stringResource(R.string.people_end), color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { askEnd = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}
