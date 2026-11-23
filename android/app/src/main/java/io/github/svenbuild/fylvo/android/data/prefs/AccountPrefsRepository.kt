package io.github.svenbuild.fylvo.android.data.prefs

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ApiResponse
import io.github.svenbuild.fylvo.android.api.FavoritesAddRequest
import io.github.svenbuild.fylvo.android.api.FavoritesList
import io.github.svenbuild.fylvo.android.api.FavoritesRemoveRequest
import io.github.svenbuild.fylvo.android.api.FylvoPrefs
import io.github.svenbuild.fylvo.android.api.MetadataPatch
import io.github.svenbuild.fylvo.android.api.MetadataPatchRequest
import io.github.svenbuild.fylvo.android.api.SessionTag
import io.github.svenbuild.fylvo.android.api.TagInfo
import io.github.svenbuild.fylvo.android.api.ViewPatchRequest
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.SessionManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.encodeToString
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import retrofit2.Response

private val Context.accountPrefsStore by preferencesDataStore(name = "account_prefs")

/** Fylvo: the account's customisation as the app holds it. */
data class AccountPrefsState(
    /** The effective settings; null before the first answer or cached copy, or on an older server. */
    val prefs: FylvoPrefs? = null,
    /** True: the server has the routes; false: an older server (everything as before); null: unknown yet. */
    val supported: Boolean? = null,
    /** The resolved favourites (newest first) and how many could not be resolved. */
    val favorites: FavoritesList? = null,
    val tags: List<TagInfo> = emptyList(),
    val accents: List<Accent> = emptyList(),
    /** The account does not sync the explorer view (GET /user/setting). */
    val disableViewSync: Boolean = false,
    /** Answers of `share/info`: true when the share is member-only and not the user's own. */
    val memberShares: Map<String, Boolean> = emptyMap(),
    /** The cached copy was read (or there was none): the screen may draw with these values. */
    val restored: Boolean = false,
    /** The last load reached no answer (network, server trouble). */
    val loadFailed: Boolean = false,
    /** The favourites could not be loaded the last time they were asked for. */
    val favoritesFailed: Boolean = false,
) {
    val favoriteIds: Set<String> get() = prefs?.favorites?.toSet().orEmpty()
    val usable: Boolean get() = prefs != null && supported != false
}

/** Fylvo: what an answer of the server means for the customisation (unit-tested). */
sealed interface Outcome<out T> {
    data class Found<T>(val value: T) : Outcome<T>
    /** HTTP 404 or an envelope 404: the server does not have the route. */
    data object Missing : Outcome<Nothing>
    /** The server refused the request with a message (e.g. 40001 for an invalid value). */
    data class Refused(val code: Int, val message: String) : Outcome<Nothing>
    /** Network trouble, server error, unreadable answer, or the session ended meanwhile. */
    data object Failed : Outcome<Nothing>
}

internal fun <T> outcomeOf(response: Response<ApiResponse<T>>, emptyOk: Boolean = false): Outcome<T> {
    if (response.code() == 404) return Outcome.Missing
    if (response.code() == 401) throw ApiException(ApiException.CODE_LOGIN_REQUIRED, "HTTP 401")
    if (!response.isSuccessful) return Outcome.Failed
    val body = response.body() ?: return Outcome.Failed
    return when (body.code) {
        0 -> {
            @Suppress("UNCHECKED_CAST")
            val data = body.data ?: (if (emptyOk) Unit as T else return Outcome.Failed)
            Outcome.Found(data)
        }
        ApiException.CODE_LOGIN_REQUIRED -> throw ApiException(body.code, body.msg)
        404 -> Outcome.Missing
        else -> Outcome.Refused(body.code, body.msg)
    }
}

/**
 * Fylvo: loads, caches and changes the account's customisation (design/customize-plan.md).
 * Every read and write runs through one queue ([queue]), answer included, so answers cannot
 * overtake each other; every request is bound to the session it was made in ([SessionTag]).
 */
class AccountPrefsRepository(
    private val context: Context,
    private val api: ApiClient,
    private val session: SessionManager,
    private val onSessionExpired: suspend () -> Unit,
) {
    private val _state = MutableStateFlow(AccountPrefsState())
    val state: StateFlow<AccountPrefsState> = _state.asStateFlow()

    private val queue = Mutex()
    /** Short lock for the files on this device (cache); also taken by [clear]. */
    private val local = Mutex()
    private var lastLoad = 0L

    @Serializable
    private data class Cached(
        val prefs: FylvoPrefs? = null,
        val accents: List<Accent> = emptyList(),
        val disableViewSync: Boolean = false,
        val favorites: FavoritesList? = null,
        val tags: List<TagInfo> = emptyList(),
    )

    private val key = stringPreferencesKey("cache")

    /** Fylvo: short hash of [accountKey] for shortcuts and the widget; empty while signed out. */
    suspend fun accountHash(): String = FylvoActions.accountHash(accountKey())

    /** "server|user id"; empty while signed out. */
    suspend fun accountKey(): String {
        val server = session.serverUrlNow()
        val user = session.userId.first()
        return if (server.isEmpty() || user.isEmpty()) "" else "$server|$user"
    }

    /** Draws from the cached copy of this account, if any; call once the session is known. */
    suspend fun restore() {
        val account = accountKey()
        val raw = local.withLock { context.accountPrefsStore.data.first()[key] }
        val cached = raw?.let { text ->
            runCatching { api.json.decodeFromString<Map<String, Cached>>(text)[account] }.getOrNull()
        }
        _state.update {
            if (it.prefs != null) it.copy(restored = true)
            else it.copy(
                prefs = cached?.prefs, accents = cached?.accents.orEmpty(), disableViewSync = cached?.disableViewSync ?: false,
                favorites = cached?.favorites, tags = cached?.tags.orEmpty(), restored = true,
            )
        }
    }

    /** The screen may draw even when the cached copy could not be read. */
    fun markRestored() = _state.update { it.copy(restored = true) }

    /** Loads everything again; [ifOlderThanMs] skips it when the last load is more recent. */
    suspend fun refresh(ifOlderThanMs: Long = 0) {
        // Checked in the queue: two calls at the same time load once.
        queue.withLock {
            if (ifOlderThanMs > 0 && System.currentTimeMillis() - lastLoad < ifOlderThanMs) return@withLock
            loadLocked()
        }
    }

    /**
     * Runs [block] in the queue for the session that was current when the change was asked for;
     * a change that waited past a sign-out or account change is dropped.
     */
    private suspend fun <T> queued(failed: T, block: suspend (SessionTag) -> T): T {
        val tag = session.sessionTag()
        return kotlinx.coroutines.withContext(kotlinx.coroutines.NonCancellable) {
            queue.withLock { if (session.isCurrent(tag)) block(tag) else failed }
        }
    }

    private suspend fun loadLocked() {
        val tag = session.sessionTag()
        val account = accountKey()
        if (account.isEmpty()) return
        when (val prefs = call(tag) { api.service().prefs(tag) }) {
            is Outcome.Found -> {
                lastLoad = System.currentTimeMillis()
                // Favourites right after prefs (in that order), then the rest; each optional.
                val favorites = (call(tag) { api.service().favorites(tag) } as? Outcome.Found)?.value
                val tags = (call(tag) { api.service().tags(tag) } as? Outcome.Found)?.value
                val basic = (call(tag) { api.service().siteBasic(tag) } as? Outcome.Found)?.value
                val settings = (call(tag) { api.service().userSettings(tag) } as? Outcome.Found)?.value
                if (!session.isCurrent(tag)) return
                val accents = basic?.let { Accents.parse(it.themes) }?.takeIf { it.isNotEmpty() } ?: _state.value.accents
                _state.update {
                    it.copy(
                        prefs = prefs.value,
                        supported = true,
                        favorites = favorites ?: it.favorites,
                        favoritesFailed = favorites == null,
                        tags = tags ?: it.tags,
                        accents = accents,
                        disableViewSync = settings?.disableViewSync ?: it.disableViewSync,
                        restored = true,
                        loadFailed = false,
                    )
                }
                saveCache(tag, account)
            }
            Outcome.Missing -> {
                if (!session.isCurrent(tag)) return
                lastLoad = System.currentTimeMillis()
                _state.update { AccountPrefsState(supported = false, restored = true) }
                saveCache(tag, account)
                local.withLock { runCatching { io.github.svenbuild.fylvo.android.widget.FylvoWidget.noFavorites(context) } }
            }
            else -> if (session.isCurrent(tag)) _state.update { it.copy(loadFailed = true) }
        }
    }

    private suspend fun saveCache(tag: SessionTag, account: String) = local.withLock {
        if (!session.isCurrent(tag)) return@withLock
        runCatching { saveCacheLocked(account) }
    }

    private suspend fun saveCacheLocked(account: String) {
        val s = _state.value
        // The widget's favourites follow the same copy (only while the session is still this one).
        s.favorites?.let { io.github.svenbuild.fylvo.android.widget.FylvoWidget.save(context, FylvoActions.accountHash(account), it.files) }
        context.accountPrefsStore.edit { store ->
            // Only this account's entry; a copy of another account on this device is dropped.
            val entry = if (s.supported == false || s.prefs == null) emptyMap()
            else mapOf(account to Cached(s.prefs, s.accents, s.disableViewSync, s.favorites, s.tags))
            store[key] = api.json.encodeToString<Map<String, Cached>>(entry)
        }
    }

    /** Forgets everything of the account (sign-out, expired session). */
    suspend fun clear() {
        local.withLock {
            // Each step on its own: a broken file must not keep the rest (or the sign-out) from running.
            _state.value = AccountPrefsState(restored = true)
            lastLoad = 0
            runCatching { context.accountPrefsStore.edit { it.clear() } }
            runCatching { io.github.svenbuild.fylvo.android.widget.FylvoWidget.clear(context) }
        }
    }

    // ---- writes ------------------------------------------------------------

    /** Result of a change: null when it worked, otherwise the server's message (or empty). */
    private suspend fun applyPrefs(tag: SessionTag, outcome: Outcome<FylvoPrefs>): String? {
        return when (outcome) {
            is Outcome.Found -> {
                if (!session.isCurrent(tag)) return ""
                _state.update { it.copy(prefs = outcome.value) }
                saveCache(tag, accountKey())
                null
            }
            is Outcome.Refused -> outcome.message
            else -> ""
        }
    }

    /** `PATCH /fylvo/prefs` with [patch]; the answer replaces the state. */
    suspend fun patch(patch: Map<String, JsonElement>): String? = queued("") { tag ->
        applyPrefs(tag, call(tag) { api.service().patchPrefs(JsonObject(patch), tag) })
    }

    fun densityPatch(density: Density) = mapOf("view" to JsonObject(mapOf("density" to JsonPrimitive(density.key))))
    fun modePatch(mode: ThemeMode) = mapOf("appearance" to JsonObject(mapOf("mode" to JsonPrimitive(mode.key))))
    fun accentPatch(key: String) = mapOf("appearance" to JsonObject(mapOf("accent" to JsonPrimitive(key))))
    fun startPagePatch(value: String) = mapOf("start_page" to JsonPrimitive(value))

    /** Shows [change] at once (optimistic) while it is saved; reverts on failure. */
    suspend fun patchOptimistic(change: (FylvoPrefs) -> FylvoPrefs, patch: Map<String, JsonElement>): String? = queued("") { tag ->
        val before = _state.value.prefs
        _state.update { s -> s.prefs?.let { s.copy(prefs = change(it)) } ?: s }
        val result = applyPrefs(tag, call(tag) { api.service().patchPrefs(JsonObject(patch), tag) })
        if (result != null) {
            // Not saved: back to what was shown before (also when the server cannot be asked).
            if (session.isCurrent(tag)) _state.update { it.copy(prefs = before) }
            loadLocked()
        }
        result
    }

    /**
     * Changes one part of `mobile`: reads the current one from the server first and replaces
     * only that part, so a change of another device to another part is kept.
     */
    suspend fun updateMobile(change: (JsonObject?) -> JsonObject): String? = queued("") { tag ->
        val fresh = call(tag) { api.service().prefs(tag) }
        if (fresh !is Outcome.Found) return@queued (fresh as? Outcome.Refused)?.message ?: ""
        val mobile = change(fresh.value.mobile)
        if (session.isCurrent(tag)) _state.update { it.copy(prefs = fresh.value.copy(mobile = mobile)) }
        val result = applyPrefs(tag, call(tag) { api.service().patchPrefs(JsonObject(mapOf("mobile" to mobile)), tag) })
        if (result != null) {
            // Not saved: what the server had a moment ago.
            if (session.isCurrent(tag)) _state.update { it.copy(prefs = fresh.value) }
            loadLocked()
        }
        result
    }

    /** Back to the defaults for the whole account (also the web); `mobile` too. */
    suspend fun reset(): String? = queued("") { tag ->
        var result = applyPrefs(tag, call(tag) { api.service().resetPrefs(tag) })
        if (result == null) {
            result = applyPrefs(tag, call(tag) { api.service().patchPrefs(JsonObject(mapOf("mobile" to JsonNull)), tag) })
        }
        loadLocked()
        result
    }

    // ---- favourites ----------------------------------------------------------

    suspend fun addFavorites(uris: List<String>): String? = favoritesChange { tag ->
        call(tag) { api.service().addFavorites(FavoritesAddRequest(uris), tag) }
    }

    suspend fun removeFavorites(ids: List<String>): String? = favoritesChange { tag ->
        call(tag) { api.service().removeFavorites(FavoritesRemoveRequest(ids), tag) }
    }

    private suspend fun favoritesChange(
        request: suspend (SessionTag) -> Outcome<io.github.svenbuild.fylvo.android.api.FavoriteIds>,
    ): String? = queued("") { tag ->
        when (val outcome = request(tag)) {
            is Outcome.Found -> {
                if (!session.isCurrent(tag)) return@queued ""
                _state.update { s -> s.copy(prefs = s.prefs?.copy(favorites = outcome.value.favorites)) }
                val list = (call(tag) { api.service().favorites(tag) } as? Outcome.Found)?.value
                if (list != null && session.isCurrent(tag)) _state.update { it.copy(favorites = list) }
                saveCache(tag, accountKey())
                null
            }
            is Outcome.Refused -> outcome.message
            else -> ""
        }
    }

    /** Fylvo: only the favourites and tags again (opening their pages), not the whole settings. */
    suspend fun refreshFavorites() = queued(Unit) { tag ->
        // The ids (stars) first, then the list, so a favourite from elsewhere also shows its star.
        val prefs = (call(tag) { api.service().prefs(tag) } as? Outcome.Found)?.value
        if (prefs != null && session.isCurrent(tag)) _state.update { it.copy(prefs = prefs) }
        val favorites = (call(tag) { api.service().favorites(tag) } as? Outcome.Found)?.value
        val tags = (call(tag) { api.service().tags(tag) } as? Outcome.Found)?.value
        if (session.isCurrent(tag)) _state.update { it.copy(favorites = favorites ?: it.favorites, favoritesFailed = favorites == null, tags = tags ?: it.tags) }
        if (favorites != null || tags != null) saveCache(tag, accountKey())
    }

    /** Whether the share [id] is member-only and not the user's own; asked once per session. */
    suspend fun memberShare(id: String): Boolean? {
        _state.value.memberShares[id]?.let { return it }
        val tag = session.sessionTag()
        val info = (call(tag) { api.service().shareInfo(id, tag) } as? Outcome.Found)?.value ?: return null
        val me = session.userId.first()
        val member = info.memberOnly && info.owner?.id != me && me.isNotEmpty()
        if (session.isCurrent(tag)) _state.update { it.copy(memberShares = it.memberShares + (id to member)) }
        return member
    }

    // ---- metadata and view -----------------------------------------------------

    /** Sets or removes metadata of [uris]; reloads the tags afterwards. */
    suspend fun patchMetadata(uris: List<String>, patches: List<MetadataPatch>): String? = queued("") { tag ->
        when (val outcome = call(tag, emptyOk = true) { api.service().patchMetadata(MetadataPatchRequest(uris, patches), tag) }) {
            is Outcome.Found -> {
                val tags = (call(tag) { api.service().tags(tag) } as? Outcome.Found)?.value
                if (tags != null && session.isCurrent(tag)) _state.update { it.copy(tags = tags) }
                saveCache(tag, accountKey())
                null
            }
            is Outcome.Refused -> outcome.message
            else -> ""
        }
    }

    /**
     * Writes the view of the folder [uri] like the web app: the whole stored view is read
     * (without page size and sort in the request) and only [change] is applied to it.
     */
    suspend fun writeView(uri: String, change: (JsonObject) -> JsonObject): String? = queued("") { tag ->
        val current = call(tag) { api.service().folderView(uri, tag) }
        if (current !is Outcome.Found) return@queued (current as? Outcome.Refused)?.message ?: ""
        val view = change(current.value.view ?: JsonObject(emptyMap()))
        val result = when (val outcome = call(tag, emptyOk = true) { api.service().patchView(ViewPatchRequest(uri, view), tag) }) {
            is Outcome.Found -> null
            is Outcome.Refused -> outcome.message
            else -> ""
        }
        if (result == null && uri == io.github.svenbuild.fylvo.android.util.CrUri.MY_PREFIX) loadLocked()
        result
    }

    // ---- plumbing --------------------------------------------------------------

    private suspend fun <T> call(tag: SessionTag, emptyOk: Boolean = false, request: suspend () -> Response<ApiResponse<T>>): Outcome<T> {
        if (!session.isCurrent(tag)) return Outcome.Failed
        return try {
            outcomeOf(request(), emptyOk)
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            if (e.code == ApiException.CODE_LOGIN_REQUIRED && session.isCurrent(tag)) onSessionExpired()
            Outcome.Failed
        } catch (_: SerializationException) {
            // An unreadable answer (a captive portal's page, a proxy error): keep what is there.
            // An older server answers unknown routes with HTTP 404, see outcomeOf.
            Outcome.Failed
        } catch (_: Exception) {
            Outcome.Failed
        }
    }

    companion object {
        /** Reload when the app comes back after this long. */
        const val STALE_MS = 5 * 60_000L

    }
}
