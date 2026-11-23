package io.github.svenbuild.fylvo.android.ui.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.outlined.RestartAlt
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FylvoPrefs
import io.github.svenbuild.fylvo.android.data.prefs.AccountPrefsRepository
import io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs
import io.github.svenbuild.fylvo.android.data.prefs.MobileView
import io.github.svenbuild.fylvo.android.data.prefs.Place
import io.github.svenbuild.fylvo.android.data.prefs.SwipeAction
import io.github.svenbuild.fylvo.android.data.prefs.SwipePrefs
import io.github.svenbuild.fylvo.android.ui.files.placeIcons
import io.github.svenbuild.fylvo.android.ui.files.placeLabel
import io.github.svenbuild.fylvo.android.util.CrUri
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/** Fylvo: a row with up/down and an optional remove button (sorting without dragging). */
@Composable
private fun OrderRow(
    label: String,
    leading: @Composable (() -> Unit)?,
    canUp: Boolean,
    canDown: Boolean,
    onUp: () -> Unit,
    onDown: () -> Unit,
    trailing: @Composable () -> Unit,
) {
    ListItem(
        headlineContent = { Text(label) },
        leadingContent = leading,
        trailingContent = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = onUp, enabled = canUp) {
                    Icon(Icons.Default.KeyboardArrowUp, contentDescription = stringResource(R.string.move_up, label))
                }
                IconButton(onClick = onDown, enabled = canDown) {
                    Icon(Icons.Default.KeyboardArrowDown, contentDescription = stringResource(R.string.move_down, label))
                }
                trailing()
            }
        },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
}

private fun <T> List<T>.moved(from: Int, to: Int): List<T> {
    if (to !in indices) return this
    val out = toMutableList()
    val item = out.removeAt(from)
    out.add(to, item)
    return out
}

/** Fylvo: the bottom tabs: order, remove, add (Files and Account always stay). */
@Composable
fun TabsSection(
    prefs: FylvoPrefs,
    repo: AccountPrefsRepository,
    /** Connected storages by id, for names and "add"; null while their list is not known yet. */
    storages: List<String>?,
    showMessage: (String?) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val links = MobilePrefs.links(prefs)
    val tabs = MobilePrefs.tabs(
        prefs.mobile,
        linkExists = { uri -> links.any { it.uri == uri } },
        storageExists = { id -> storages == null || id in storages },
    )
        .map { tab -> if (tab is Place.Link) links.firstOrNull { it.uri == tab.uri } ?: tab else tab }
    var adding by remember { mutableStateOf(false) }
    /** Applies [change] to the tabs the server has now, so a quick second change keeps the first. */
    fun save(change: (List<Place>) -> List<Place>) = scope.launch {
        showMessage(
            repo.updateMobile { fresh ->
                val current = MobilePrefs.tabs(fresh, linkExists = { uri -> links.any { it.uri == uri } })
                MobilePrefs.withTabs(fresh, change(current))
            },
        )
    }
    fun move(tab: Place, by: Int) = save { list ->
        val i = list.indexOf(tab)
        if (i < 0) list else list.moved(i, i + by)
    }

    CustomizeLabel(stringResource(R.string.customize_tabs))
    tabs.forEachIndexed { i, tab ->
        // Files and Account always stay; at three tabs none can go (fewer would mean the default).
        val fixed = tab == Place.Files || tab == Place.Account || tabs.size <= 3
        OrderRow(
            label = placeLabel(tab),
            leading = { io.github.svenbuild.fylvo.android.ui.files.PlaceIcon(tab, tint = MaterialTheme.colorScheme.onSurfaceVariant) },
            canUp = i > 0, canDown = i < tabs.lastIndex,
            onUp = { move(tab, -1) }, onDown = { move(tab, 1) },
        ) {
            if (fixed) {
                Icon(Icons.Default.Close, contentDescription = null, tint = Color.Transparent, modifier = Modifier.padding(12.dp).size(24.dp))
            } else {
                IconButton(onClick = { save { list -> if (list.size > 3) list - tab else list } }) {
                    Icon(Icons.Default.Close, contentDescription = stringResource(R.string.remove_tab, placeLabel(tab)))
                }
            }
        }
    }
    val full = tabs.size >= MobilePrefs.MAX_TABS
    ListItem(
        modifier = if (full) Modifier else Modifier.clickable { adding = true },
        headlineContent = { Text(stringResource(R.string.add_tab)) },
        supportingContent = { Text(stringResource(if (full) R.string.tabs_full else R.string.tabs_hint)) },
        leadingContent = { Icon(Icons.Default.Add, contentDescription = null) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
    if (adding) {
        val candidates = (
            listOf(Place.Home, Place.Shared, Place.Favorites, Place.Tags, Place.Recent, Place.Offline, Place.Trash) +
                Place.CATEGORIES.map { Place.Category(it) } + links + storages.orEmpty().map { Place.Storage(it) }
            ).filter { it !in tabs }
        AlertDialog(
            onDismissRequest = { adding = false },
            title = { Text(stringResource(R.string.add_tab)) },
            text = {
                Column(Modifier.verticalScroll(rememberScrollState())) {
                    candidates.forEach { place ->
                        ListItem(
                            modifier = Modifier.clickable {
                                adding = false
                                // New tabs go before Account (which stays last unless moved).
                                save { list ->
                                    if (place in list || list.size >= MobilePrefs.MAX_TABS) list
                                    else list.toMutableList().apply { add(indexOf(Place.Account).takeIf { it >= 0 } ?: size, place) }
                                }
                            },
                            headlineContent = { Text(placeLabel(place)) },
                            leadingContent = { io.github.svenbuild.fylvo.android.ui.files.PlaceIcon(place) },
                            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
                        )
                    }
                }
            },
            confirmButton = { TextButton(onClick = { adding = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

/** Fylvo: what swiping a row left or right does. */
@Composable
fun SwipeSection(prefs: FylvoPrefs, repo: AccountPrefsRepository, showMessage: (String?) -> Unit) {
    val scope = rememberCoroutineScope()
    val swipe = MobilePrefs.swipe(prefs.mobile)
    var choosing by remember { mutableStateOf<Boolean?>(null) } // true = right, false = left
    CustomizeLabel(stringResource(R.string.customize_swipe))
    listOf(true to swipe.right, false to swipe.left).forEach { (right, action) ->
        ListItem(
            modifier = Modifier.clickable { choosing = right },
            headlineContent = { Text(stringResource(if (right) R.string.swipe_right else R.string.swipe_left)) },
            supportingContent = { Text(stringResource(action.label)) },
            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        )
    }
    choosing?.let { right ->
        val current = if (right) swipe.right else swipe.left
        AlertDialog(
            onDismissRequest = { choosing = null },
            title = { Text(stringResource(if (right) R.string.swipe_right else R.string.swipe_left)) },
            text = {
                Column {
                    SwipeAction.entries.forEach { action ->
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .clickable {
                                    choosing = null
                                    // Only this direction, set on what the server has now (another change stays).
                                    scope.launch {
                                        showMessage(
                                            repo.updateMobile { fresh ->
                                                val now = MobilePrefs.swipe(fresh)
                                                MobilePrefs.withSwipe(fresh, if (right) now.copy(right = action) else now.copy(left = action))
                                            },
                                        )
                                    }
                                }
                                .padding(vertical = 4.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(selected = action == current, onClick = null)
                            Text(stringResource(action.label), modifier = Modifier.padding(start = 12.dp))
                        }
                    }
                }
            },
            confirmButton = { TextButton(onClick = { choosing = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

val SwipeAction.label: Int
    get() = when (this) {
        SwipeAction.NONE -> R.string.swipe_none
        SwipeAction.FAVORITE -> R.string.favorite
        SwipeAction.SHARE -> R.string.share_link
        SwipeAction.OFFLINE -> R.string.available_offline
        SwipeAction.DELETE -> R.string.delete
    }

/** Fylvo: the start page and the building blocks of Home. */
@Composable
fun StartSection(prefs: FylvoPrefs, repo: AccountPrefsRepository, showMessage: (String?) -> Unit) {
    val scope = rememberCoroutineScope()
    CustomizeLabel(stringResource(R.string.customize_start))
    Segmented(
        options = listOf("home" to stringResource(R.string.tab_home), "my_files" to stringResource(R.string.tab_files)),
        selected = if (prefs.startPage == "my_files") "my_files" else "home",
        onSelect = { v ->
            scope.launch { showMessage(repo.patchOptimistic({ it.copy(startPage = v) }, repo.startPagePatch(v))) }
        },
    )
    CustomizeLabel(stringResource(R.string.customize_home_widgets))
    val widgets = prefs.home.widgets.filter { it.type in MobilePrefs.HOME_WIDGETS }
    fun save(next: List<io.github.svenbuild.fylvo.android.api.HomeWidget>) = scope.launch {
        val json = JsonObject(mapOf("widgets" to JsonArray(next.map { w ->
            JsonObject(buildMap {
                put("type", JsonPrimitive(w.type))
                if (w.hidden) put("hidden", JsonPrimitive(true))
            })
        })))
        showMessage(
            repo.patchOptimistic(
                { it.copy(home = FylvoPrefs.Home(next), customized = (it.customized + "home").distinct()) },
                mapOf("home" to json),
            ),
        )
    }
    widgets.forEachIndexed { i, w ->
        OrderRow(
            label = stringResource(widgetLabel(w.type)),
            leading = { Checkbox(checked = !w.hidden, onCheckedChange = { on -> save(widgets.map { if (it.type == w.type) it.copy(hidden = !on) else it }) }) },
            canUp = i > 0, canDown = i < widgets.lastIndex,
            onUp = { save(widgets.moved(i, i - 1)) }, onDown = { save(widgets.moved(i, i + 1)) },
        ) {}
    }
    if ("home" !in prefs.customized) {
        Text(
            stringResource(R.string.customize_home_default),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 16.dp),
        )
    }
}

private fun widgetLabel(type: String): Int = when (type) {
    "recent" -> R.string.category_recent
    "favorites" -> R.string.favorites
    "links" -> R.string.home_links
    "tags" -> R.string.tags
    else -> R.string.storage
}

/** Fylvo: the default view of My files (list or grid, sort), unless the account keeps views per device. */
@Composable
fun ViewSection(prefs: FylvoPrefs, disableViewSync: Boolean, repo: AccountPrefsRepository, showMessage: (String?) -> Unit) {
    if (disableViewSync) return
    val scope = rememberCoroutineScope()
    CustomizeLabel(stringResource(R.string.customize_view))
    val layout = if (prefs.view.layout == "list") "list" else "grid"
    Segmented(
        options = listOf("list" to stringResource(R.string.list_view), "grid" to stringResource(R.string.grid_view)),
        selected = layout,
        onSelect = { v -> scope.launch { showMessage(repo.writeView(CrUri.MY_PREFIX) { MobileView.withLayout(it, v) }) } },
    )
    val sorts = listOf(
        "name" to R.string.sort_name, "size" to R.string.size, "updated_at" to R.string.sort_modified, "created_at" to R.string.sort_created,
    )
    var choosing by remember { mutableStateOf(false) }
    val ascending = prefs.view.sortDirection != "desc"
    ListItem(
        modifier = Modifier.clickable { choosing = true },
        headlineContent = { Text(stringResource(R.string.sort_by)) },
        supportingContent = {
            Text(
                stringResource(sorts.firstOrNull { it.first == prefs.view.sortBy }?.second ?: R.string.sort_created) + " · " +
                    stringResource(if (ascending) R.string.ascending else R.string.descending),
            )
        },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
    if (choosing) {
        AlertDialog(
            onDismissRequest = { choosing = false },
            title = { Text(stringResource(R.string.sort_by)) },
            text = {
                Column {
                    sorts.forEach { (key, label) ->
                        listOf(true, false).forEach { asc ->
                            Row(
                                Modifier
                                    .fillMaxWidth()
                                    .clickable {
                                        choosing = false
                                        scope.launch { showMessage(repo.writeView(CrUri.MY_PREFIX) { MobileView.withSort(it, key, asc) }) }
                                    }
                                    .padding(vertical = 4.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                RadioButton(selected = prefs.view.sortBy == key && ascending == asc, onClick = null)
                                Text(
                                    stringResource(label) + " · " + stringResource(if (asc) R.string.ascending else R.string.descending),
                                    modifier = Modifier.padding(start = 12.dp),
                                )
                            }
                        }
                    }
                }
            },
            confirmButton = { TextButton(onClick = { choosing = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

/** Fylvo: back to the defaults for the whole account (asked first). */
@Composable
fun ResetSection(repo: AccountPrefsRepository, showMessage: (String?) -> Unit) {
    val scope = rememberCoroutineScope()
    var asking by remember { mutableStateOf(false) }
    ListItem(
        modifier = Modifier
            .padding(top = 16.dp)
            .clickable { asking = true },
        headlineContent = { Text(stringResource(R.string.customize_reset), color = MaterialTheme.colorScheme.error) },
        leadingContent = { Icon(Icons.Outlined.RestartAlt, contentDescription = null, tint = MaterialTheme.colorScheme.error) },
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
    if (asking) {
        AlertDialog(
            onDismissRequest = { asking = false },
            title = { Text(stringResource(R.string.customize_reset)) },
            text = { Text(stringResource(R.string.customize_reset_text)) },
            confirmButton = {
                TextButton(onClick = {
                    asking = false
                    scope.launch { showMessage(repo.reset()) }
                }) { Text(stringResource(R.string.customize_reset_confirm)) }
            },
            dismissButton = { TextButton(onClick = { asking = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}
