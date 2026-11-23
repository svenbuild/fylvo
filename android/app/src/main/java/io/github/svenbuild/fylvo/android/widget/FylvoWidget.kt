package io.github.svenbuild.fylvo.android.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.RemoteViews
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import androidx.core.net.toUri
import io.github.svenbuild.fylvo.android.MainActivity
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.prefs.Accents
import io.github.svenbuild.fylvo.android.data.prefs.FylvoActions
import io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs
import io.github.svenbuild.fylvo.android.util.localized
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File

/** Fylvo: one favourite as the widget shows it (no more than this is kept on the device). */
@Serializable
data class WidgetEntry(val name: String, val uri: String, val folder: Boolean, val color: String? = null)

@Serializable
private data class WidgetData(
    val account: String,
    val entries: List<WidgetEntry>,
    /** Signed in, but the server has no favourites (an older Fylvo): only search and upload. */
    val unsupported: Boolean = false,
)

/**
 * Fylvo: the home screen widget: search and upload; taller, up to four favourites of My files.
 * Its data is a small file written when the favourites load and removed at sign-out; every tap
 * is an explicit, immutable intent to MainActivity, which checks it again.
 */
class FylvoWidgetProvider : AppWidgetProvider() {

    override fun onUpdate(context: Context, manager: AppWidgetManager, ids: IntArray) {
        ids.forEach { FylvoWidget.draw(context, manager, it) }
    }

    override fun onAppWidgetOptionsChanged(context: Context, manager: AppWidgetManager, id: Int, newOptions: Bundle) {
        FylvoWidget.draw(context, manager, id)
    }
}

object FylvoWidget {
    private const val FILE = "widget_favorites.json"
    private const val SLOTS = 4
    /** From this height on the favourites are shown. */
    private const val TALL_DP = 110
    private val json = Json { ignoreUnknownKeys = true }
    private val lock = Any()

    /** Keeps up to four favourites of My files for [accountHash] and redraws every widget. */
    fun save(context: Context, accountHash: String, favorites: List<FileObject>) {
        if (accountHash.isEmpty()) return clear(context)
        val entries = favorites.filter { FylvoActions.openAllowed(it.path) }.take(SLOTS).map {
            WidgetEntry(it.name, it.path, it.isFolder, MobilePrefs.folderColor(it))
        }
        synchronized(lock) {
            File(context.filesDir, FILE).writeText(json.encodeToString(WidgetData.serializer(), WidgetData(accountHash, entries)))
        }
        redrawAll(context)
    }

    /** An older server: signed in, no favourites to show (no names stay either). */
    fun noFavorites(context: Context) {
        synchronized(lock) {
            File(context.filesDir, FILE).writeText(json.encodeToString(WidgetData.serializer(), WidgetData("", emptyList(), unsupported = true)))
        }
        redrawAll(context)
    }

    /** Sign-out: no names stay on the home screen. */
    fun clear(context: Context) {
        synchronized(lock) { File(context.filesDir, FILE).delete() }
        redrawAll(context)
    }

    private fun read(context: Context): WidgetData? = synchronized(lock) {
        val file = File(context.filesDir, FILE)
        if (!file.exists()) null else runCatching { json.decodeFromString(WidgetData.serializer(), file.readText()) }.getOrNull()
    }

    fun redrawAll(context: Context) {
        val manager = AppWidgetManager.getInstance(context) ?: return
        val ids = runCatching { manager.getAppWidgetIds(ComponentName(context, FylvoWidgetProvider::class.java)) }.getOrNull() ?: return
        ids.forEach { draw(context, manager, it) }
    }

    private fun action(context: Context, action: String, request: Int, change: Intent.() -> Unit = {}): PendingIntent =
        PendingIntent.getActivity(
            context, request,
            Intent(context, MainActivity::class.java).setAction(action).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK).apply(change),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    fun draw(context: Context, manager: AppWidgetManager, id: Int) {
        val text = context.localized()
        val views = RemoteViews(context.packageName, R.layout.widget_fylvo)
        views.setOnClickPendingIntent(R.id.widget_search, action(context, FylvoActions.SEARCH, 1))
        views.setOnClickPendingIntent(R.id.widget_upload, action(context, FylvoActions.UPLOAD, 2))
        views.setTextViewText(R.id.widget_fav_title, text.getString(R.string.favorites))
        val height = manager.getAppWidgetOptions(id)?.getInt(AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT) ?: 0
        // As tall as at least one favourite row needs.
        // As many rows as fit below the search bar and the title (60 + 30 dp), 36 dp each.
        val rows = ((height - 90) / 36).coerceIn(0, SLOTS)
        val tall = height >= TALL_DP && rows > 0
        val stored = read(context)
        val slots = listOf(
            Triple(R.id.widget_fav1, R.id.widget_fav1_icon, R.id.widget_fav1_name),
            Triple(R.id.widget_fav2, R.id.widget_fav2_icon, R.id.widget_fav2_name),
            Triple(R.id.widget_fav3, R.id.widget_fav3_icon, R.id.widget_fav3_name),
            Triple(R.id.widget_fav4, R.id.widget_fav4_icon, R.id.widget_fav4_name),
        )
        val entries = if (tall) stored?.entries.orEmpty().take(rows) else emptyList()
        views.setViewVisibility(R.id.widget_fav_title, if (entries.isNotEmpty()) View.VISIBLE else View.GONE)
        // Tall but nothing to show: say why (signed out, or no favourites yet).
        val hint = when {
            !tall -> null
            stored == null -> text.getString(R.string.widget_signed_out)
            stored.unsupported -> null
            stored.entries.isEmpty() -> text.getString(R.string.no_favorites)
            else -> null
        }
        views.setViewVisibility(R.id.widget_hint, if (hint != null) View.VISIBLE else View.GONE)
        views.setTextViewText(R.id.widget_hint, hint.orEmpty())
        slots.forEachIndexed { i, (row, icon, name) ->
            val entry = entries.getOrNull(i)
            if (entry == null) {
                views.setViewVisibility(row, View.GONE)
                return@forEachIndexed
            }
            views.setViewVisibility(row, View.VISIBLE)
            views.setTextViewText(name, entry.name)
            views.setImageViewResource(icon, if (entry.folder) R.drawable.ic_widget_folder else R.drawable.ic_widget_file)
            val tint = entry.color?.let(Accents::parseColor) ?: 0xFF8A8A93.toInt()
            views.setInt(icon, "setColorFilter", tint)
            // Own request code and data per slot, so each opens its own favourite.
            views.setOnClickPendingIntent(row, action(context, FylvoActions.OPEN, 10 + i) {
                data = entry.uri.toUri()
                putExtra(FylvoActions.EXTRA_ACCOUNT, stored?.account)
                putExtra(FylvoActions.EXTRA_FOLDER, entry.folder)
            })
        }
        manager.updateAppWidget(id, views)
    }
}

/** Fylvo: a folder as its own icon on the home screen ("Add to home screen"). */
object FolderShortcuts {
    private const val PREFIX = "folder-"

    fun canPin(context: Context): Boolean = ShortcutManagerCompat.isRequestPinShortcutSupported(context)

    /** Asks the launcher to pin [folder] (own folder below My files) for [accountHash]. */
    fun pin(context: Context, folder: FileObject, accountHash: String): Boolean {
        if (accountHash.isEmpty() || !folder.isFolder || !FylvoActions.openAllowed(folder.path)) return false
        val intent = Intent(context, MainActivity::class.java)
            .setAction(FylvoActions.OPEN)
            .setData(folder.path.toUri())
            .putExtra(FylvoActions.EXTRA_ACCOUNT, accountHash)
            .putExtra(FylvoActions.EXTRA_FOLDER, true)
        val info = ShortcutInfoCompat.Builder(context, "$PREFIX$accountHash-${folder.id}")
            .setShortLabel(folder.name)
            .setIcon(IconCompat.createWithResource(context, R.drawable.ic_shortcut_folder))
            .setIntent(intent)
            .build()
        return runCatching { ShortcutManagerCompat.requestPinShortcut(context, info, null) }.getOrDefault(false)
    }

    private fun manager(context: Context): android.content.pm.ShortcutManager? =
        context.getSystemService(android.content.pm.ShortcutManager::class.java)

    private fun pinnedIds(context: Context, prefix: String): List<String> =
        runCatching { manager(context)?.pinnedShortcuts.orEmpty().map { it.id }.filter { it.startsWith(prefix) } }.getOrNull().orEmpty()

    /** Sign-out or expired session: the pinned folders of the app stop working (they stay visible). */
    fun disableAll(context: Context) {
        val ids = pinnedIds(context, PREFIX)
        if (ids.isNotEmpty()) runCatching { manager(context)?.disableShortcuts(ids, context.localized().getString(R.string.shortcut_signed_out)) }
    }

    /** After a sign-in: the folders pinned for this account work again. */
    fun enableFor(context: Context, accountHash: String) {
        if (accountHash.isEmpty()) return
        val ids = pinnedIds(context, "$PREFIX$accountHash-")
        if (ids.isNotEmpty()) runCatching { manager(context)?.enableShortcuts(ids) }
    }
}
