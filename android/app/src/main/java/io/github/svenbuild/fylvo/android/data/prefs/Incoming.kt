package io.github.svenbuild.fylvo.android.data.prefs

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import io.github.svenbuild.fylvo.android.util.CrUri
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import java.security.MessageDigest

/**
 * Fylvo: what another app, the launcher (shortcuts) or the widget asks the app to show. It waits
 * here until the screen is ready and is taken exactly once (see FilesScreen).
 */
sealed interface Incoming {
    /** Files shared from another app: the sheet "Save in Fylvo" asks for the folder first. */
    data class Share(val uris: List<Uri>) : Incoming
    data object Search : Incoming
    /** Pick files on the phone, then the same sheet as [Share]. */
    data object Upload : Incoming
    data object Favorites : Incoming
    /** Open a folder of My files (or show a file in its folder). */
    data class Open(val uri: String, val folder: Boolean) : Incoming
}

/** Fylvo: the app's own actions for shortcuts and the widget; only navigation, never a change. */
object FylvoActions {
    const val SEARCH = "io.github.svenbuild.fylvo.android.action.SEARCH"
    const val UPLOAD = "io.github.svenbuild.fylvo.android.action.UPLOAD"
    const val FAVORITES = "io.github.svenbuild.fylvo.android.action.FAVORITES"
    const val OPEN = "io.github.svenbuild.fylvo.android.action.OPEN"
    const val EXTRA_ACCOUNT = "account"
    const val EXTRA_FOLDER = "folder"

    /** Short hash of "server|user id": binds a shortcut or widget entry to the account. */
    fun accountHash(accountKey: String): String {
        if (accountKey.isEmpty()) return ""
        val digest = MessageDigest.getInstance("SHA-256").digest(accountKey.toByteArray())
        return digest.take(8).joinToString("") { "%02x".format(it) }
    }

    /**
     * The request of [intent], or null when it is none of ours or not allowed: OPEN only for an
     * address below My files and only for the signed-in account ([accountHash]).
     */
    fun parse(intent: Intent?, accountHash: String): Incoming? = when (intent?.action) {
        SEARCH -> Incoming.Search
        UPLOAD -> Incoming.Upload
        FAVORITES -> Incoming.Favorites
        OPEN -> {
            val uri = intent.dataString?.takeIf { openAllowed(it) }
            val hash = intent.getStringExtra(EXTRA_ACCOUNT)
            if (uri != null && accountHash.isNotEmpty() && hash == accountHash) {
                Incoming.Open(uri, intent.getBooleanExtra(EXTRA_FOLDER, true))
            } else {
                null
            }
        }
        else -> null
    }

    /** Only a plain address below My files (no search, no other root). */
    fun openAllowed(uri: String): Boolean =
        uri.startsWith(CrUri.MY_PREFIX + "/") && !CrUri.isSearch(uri) && !uri.contains("/../") && !uri.endsWith("/..")
}

private val Context.shareTargetStore by preferencesDataStore(name = "share_target")

/** Fylvo: the folder chosen last when sharing into Fylvo (this device only; gone at sign-out). */
class ShareTargetStore(private val context: Context) {
    private val uriKey = stringPreferencesKey("uri")
    private val idKey = stringPreferencesKey("id")

    /** The folder and its id (null for My files itself, which cannot be replaced). */
    suspend fun get(): Pair<String, String?>? {
        val p = context.shareTargetStore.data.first()
        val uri = p[uriKey] ?: return null
        val id = p[idKey]?.ifEmpty { null }
        if (uri != CrUri.MY_PREFIX && (id == null || !FylvoActions.openAllowed(uri))) return null
        return uri to id
    }

    suspend fun set(uri: String, id: String) {
        context.shareTargetStore.edit {
            it[uriKey] = uri
            it[idKey] = id
        }
    }

    suspend fun clear() {
        context.shareTargetStore.edit { it.clear() }
    }
}

/** Fylvo: requests that wait for the screen (process lifetime, survives a rebuilt activity). */
object IncomingQueue {
    val pending = MutableStateFlow<Incoming?>(null)
    /** The session the waiting request (and the share sheet) belongs to; another one drops them. */
    @Volatile var session: io.github.svenbuild.fylvo.android.api.SessionTag? = null
    /** Files waiting in the sheet "Save in Fylvo" (kept while the screen is rebuilt). */
    val share = MutableStateFlow<List<Uri>?>(null)
    /** The folder chosen in that sheet (address and id; the id is null only for My files). */
    val shareTarget = MutableStateFlow<Pair<String, String?>?>(null)
}
