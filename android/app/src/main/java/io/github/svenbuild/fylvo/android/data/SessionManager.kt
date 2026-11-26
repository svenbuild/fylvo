package io.github.svenbuild.fylvo.android.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.sessionStore by preferencesDataStore(name = "session")

/**
 * Ensure a user-entered server address is a usable base URL: bare hosts like
 * `drive.example.com` get `https://`, surrounding whitespace and trailing
 * slashes are stripped.
 */
fun normalizeServerUrl(url: String): String {
    val trimmed = url.trim().trimEnd('/')
    if (trimmed.isEmpty() || trimmed.contains("://")) return trimmed
    return "https://$trimmed"
}

/**
 * SessionManager persists the server base URL and token pair. It is the
 * single source of truth for "which account, on which server".
 *
 * Fylvo: the access and refresh tokens are stored encrypted with a key from the Android
 * Keystore ([SecretBox]). A token that no longer decrypts counts as signed out.
 */
class SessionManager(private val context: Context) {

    // Fylvo: the number of the session in this process. It changes at every sign-in and at the
    // start of every session end; requests tagged with an older number are not sent (ApiClient),
    // and answers to them are not taken over (AccountPrefsRepository).
    private val generationCounter = java.util.concurrent.atomic.AtomicLong(1)
    @Volatile private var ended = false

    /** Fylvo: the tag for a request of the current session. */
    fun sessionTag(): io.github.svenbuild.fylvo.android.api.SessionTag =
        io.github.svenbuild.fylvo.android.api.SessionTag(generationCounter.get())

    /** Fylvo: true while [tag]'s session is the signed-in one and not ending. */
    fun isCurrent(tag: io.github.svenbuild.fylvo.android.api.SessionTag): Boolean =
        !ended && tag.generation == generationCounter.get()

    /** Fylvo: the session ends now; no tagged request runs any more until the next sign-in. */
    fun endGeneration() {
        ended = true
        generationCounter.incrementAndGet()
    }

    private object Keys {
        val serverUrl = stringPreferencesKey("server_url")
        val accessToken = stringPreferencesKey("access_token")
        val refreshToken = stringPreferencesKey("refresh_token")
        // Epoch millis when access_token expires; 0 = unknown.
        val accessExpiresAt = longPreferencesKey("access_expires_at")
        val userEmail = stringPreferencesKey("user_email")
        val userNick = stringPreferencesKey("user_nick")
        /** Fylvo: the account's id on the server; binds the offline data to the account. */
        val userId = stringPreferencesKey("user_id")
    }

    val serverUrl: Flow<String> = context.sessionStore.data.map { it[Keys.serverUrl] ?: "" }
    val accessToken: Flow<String> = context.sessionStore.data.map { secret(Keys.accessToken) }
    val userEmail: Flow<String> = context.sessionStore.data.map { it[Keys.userEmail] ?: "" }
    val userNick: Flow<String> = context.sessionStore.data.map { it[Keys.userNick] ?: "" }
    val userId: Flow<String> = context.sessionStore.data.map { it[Keys.userId] ?: "" }
    val isLoggedIn: Flow<Boolean> = context.sessionStore.data.map {
        secret(Keys.refreshToken).isNotEmpty() && !it[Keys.serverUrl].isNullOrEmpty()
    }

    /**
     * Fylvo: the latest decrypted value per key (stored value and plain text; a new stored value
     * replaces the old one). Every request reads the access token, which was a Keystore round trip
     * each time. Cleared when the session ends; [secretsGeneration] is read before the stored value,
     * so a read that began before the session ended never puts its value back.
     */
    private val secrets = HashMap<String, Pair<String, String>>()
    private var secretsGeneration = 0L

    /** Decrypted value, or "" when missing or no longer decryptable. */
    private suspend fun secret(key: Preferences.Key<String>): String {
        val generation = synchronized(secrets) { secretsGeneration }
        val stored = context.sessionStore.data.first()[key] ?: return ""
        synchronized(secrets) {
            secrets[key.name]?.let { (sealed, plain) -> if (sealed == stored) return plain }
        }
        val plain = SecretBox.decrypt(stored) ?: return ""
        synchronized(secrets) { if (generation == secretsGeneration) secrets[key.name] = stored to plain }
        return plain
    }

    private fun forgetSecrets() = synchronized(secrets) {
        secretsGeneration++
        secrets.clear()
    }

    suspend fun serverUrlNow(): String = serverUrl.first()
    suspend fun accessTokenNow(): String = accessToken.first()
    suspend fun refreshTokenNow(): String =
        secret(Keys.refreshToken)
    suspend fun accessExpiresAtNow(): Long =
        context.sessionStore.data.first()[Keys.accessExpiresAt] ?: 0L

    suspend fun saveServerUrl(url: String) {
        context.sessionStore.edit { it[Keys.serverUrl] = normalizeServerUrl(url) }
    }

    suspend fun saveSession(
        accessToken: String,
        refreshToken: String,
        accessExpiresAt: Long,
        email: String,
        nick: String,
        userId: String = "",
    ) {
        val accessSealed = SecretBox.encrypt(accessToken)
        val refreshSealed = SecretBox.encrypt(refreshToken)
        context.sessionStore.edit {
            it[Keys.accessToken] = accessSealed
            it[Keys.refreshToken] = refreshSealed
            it[Keys.accessExpiresAt] = accessExpiresAt
            it[Keys.userEmail] = email
            it[Keys.userNick] = nick
            it[Keys.userId] = userId
        }
        generationCounter.incrementAndGet()
        ended = false
        notifyDocumentsRoots()
    }

    /** Fylvo: the system Files app shows the Fylvo root only while signed in. */
    private fun notifyDocumentsRoots() {
        context.contentResolver.notifyChange(
            android.provider.DocumentsContract.buildRootsUri("${context.packageName}.documents"), null,
        )
    }

    suspend fun saveTokens(
        accessToken: String,
        refreshToken: String,
        accessExpiresAt: Long,
        /** Fylvo: only while this session is current (checked inside the store's write, after a sign-out began nothing is written). */
        forSession: io.github.svenbuild.fylvo.android.api.SessionTag? = null,
    ) {
        val accessSealed = SecretBox.encrypt(accessToken)
        val refreshSealed = SecretBox.encrypt(refreshToken)
        context.sessionStore.edit {
            if (forSession != null && !isCurrent(forSession)) return@edit
            it[Keys.accessToken] = accessSealed
            it[Keys.refreshToken] = refreshSealed
            it[Keys.accessExpiresAt] = accessExpiresAt
        }
    }

    suspend fun clear() {
        endGeneration()
        context.sessionStore.edit { it.clear() }
        forgetSecrets()
        notifyDocumentsRoots()
    }

    /**
     * Session expiry (401 from the server): drops tokens and identity but keeps
     * serverUrl/userEmail so re-login stays on the same server with the email
     * prefilled. Full sign-out uses [clear] instead.
     */
    suspend fun clearSession() {
        endGeneration()
        context.sessionStore.edit {
            it.remove(Keys.accessToken)
            it.remove(Keys.refreshToken)
            it.remove(Keys.accessExpiresAt)
            it.remove(Keys.userNick)
        }
        forgetSecrets()
        notifyDocumentsRoots()
    }
}
