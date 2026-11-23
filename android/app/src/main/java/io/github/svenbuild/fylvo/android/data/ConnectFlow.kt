package io.github.svenbuild.fylvo.android.data

import android.content.Context
import android.net.Uri
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ConnectAuthorizeRequest
import io.github.svenbuild.fylvo.android.api.ConnectFinishRequest
import io.github.svenbuild.fylvo.android.api.ConnectPreview
import io.github.svenbuild.fylvo.android.api.ConnectedStorage
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

/**
 * Fylvo 1.6: connecting a storage (Google Drive, OneDrive) from the app (design/android-parity-plan.md,
 * 14), on the server's way for apps: a secret ("verifier") of the app, only its check value goes to
 * `authorize`; the sign-in runs in the system browser (Custom Tab); the server's page leads back with
 * `fylvo://connected?attempt=…&finish=…`; the app shows which account it is and saves only after "yes".
 * The verifier is never logged.
 */
object ConnectFlow {
    /** 32 random bytes, base64url without padding (43 characters). */
    fun newVerifier(random: SecureRandom = SecureRandom()): String =
        ByteArray(32).also(random::nextBytes).let(::b64)

    /** base64url without padding of SHA-256 over the verifier's characters (as PKCE S256). */
    fun challenge(verifier: String): String = b64(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII)))

    private fun b64(bytes: ByteArray) = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)

    private val TICKET = Regex("[A-Za-z0-9_-]{43}")
    private val ATTEMPT = Regex("[A-Za-z0-9_-]{16}")
    private val CODE = Regex("[A-Za-z0-9_-]{32}")
    private val ERRORS = setOf("denied", "provider", "setup", "expired", "not_allowed", "too_many")
    private const val START_PATH = "/api/v4/fylvo/connected/start/"

    /** The start address is taken only in exactly the server's form; [allowHttp] for development builds. */
    fun validStart(url: String, allowHttp: Boolean): Boolean {
        val u = url.toHttpUrlOrNull() ?: return false
        if (u.scheme != "https" && !(allowHttp && u.scheme == "http")) return false
        if (u.username.isNotEmpty() || u.password.isNotEmpty() || u.query != null || u.fragment != null) return false
        val path = u.encodedPath
        return path.startsWith(START_PATH) && TICKET.matches(path.removePrefix(START_PATH))
    }

    fun validAttempt(attempt: String): Boolean = ATTEMPT.matches(attempt)

    /** What a link back says. */
    sealed interface Return {
        data class Finish(val attempt: String, val code: String) : Return
        /** A hint only (the attempt id is no secret): shown, the attempt goes on. */
        data class Error(val attempt: String, val reason: String) : Return
        /** `fylvo://connected` alone: only brings the app to the front. */
        data object Front : Return
    }

    /** The link back, or null for anything that is not exactly one of its forms (then ignored). */
    fun parseReturn(uri: String): Return? {
        val u = runCatching { Uri.parse(uri) }.getOrNull() ?: return null
        return parseReturn(u.scheme, u.host, u.encodedPath, u.encodedQuery, u.fragment)
    }

    /** [parseReturn] on the parts of the address (pure; unit-tested). */
    fun parseReturn(scheme: String?, host: String?, path: String?, query: String?, fragment: String?): Return? {
        if (scheme != "fylvo" || host != "connected" || fragment != null) return null
        if (!path.isNullOrEmpty() && path != "/") return null
        if (query.isNullOrEmpty()) return Return.Front
        val pairs = query.split('&').map { it.substringBefore('=') to it.substringAfter('=', "") }
        val keys = pairs.map { it.first }
        if (keys.size != keys.toSet().size) return null
        val map = pairs.toMap()
        val attempt = map["attempt"]?.takeIf(::validAttempt) ?: return null
        return when (keys.toSet()) {
            setOf("attempt", "finish") -> map["finish"]?.takeIf { CODE.matches(it) }?.let { Return.Finish(attempt, it) }
            setOf("attempt", "error") -> map["error"]?.takeIf { it in ERRORS }?.let { Return.Error(attempt, it) }
            else -> null
        }
    }

    /** What the app does with an answer of `finish` (the table of the server's contract). */
    enum class Next {
        /** Ignore this link, keep waiting for another one. */
        KeepWaiting,
        /** Stop the attempt. */
        End,
        /** Stop the attempt and read the list again (it may have been saved). */
        EndAndReload,
        /** Send the same request again (no answer came). */
        Repeat,
    }

    /** [code]/[reason] of a refusal, null code = no answer at all; [sentBefore]: this code was sent before. */
    fun next(code: Int?, reason: String?, sentBefore: Boolean): Next = when {
        code == null -> Next.Repeat
        code == 40922 -> Next.End
        code == 40001 && reason == "expired" -> if (sentBefore) Next.EndAndReload else Next.KeepWaiting
        code == 40001 && (reason == "account" || reason == "verifier") -> Next.KeepWaiting
        code == 40004 -> Next.EndAndReload
        code == 40001 && reason == "other_account" -> Next.End
        code == 40001 && reason == "too_many" -> Next.End
        code == 40016 -> Next.EndAndReload
        code == 40001 && reason == "overtaken" -> Next.EndAndReload
        else -> Next.EndAndReload
    }

    /** An attempt ends at the latest 20 minutes after `authorize`. */
    const val ATTEMPT_MS = 20 * 60_000L
}

/**
 * Fylvo 1.6: the one running attempt, kept in app-private storage (it survives the app being
 * stopped while the browser is in front; app data never goes into backups).
 */
@Serializable
data class ConnectAttempt(
    val server: String,
    val account: String,
    val attempt: String,
    val verifier: String,
    val provider: String,
    val reconnect: String? = null,
    val startedAt: Long,
    /** The code of a finish link being decided, and that the person said yes (saved before sending). */
    val code: String? = null,
    val yes: Boolean = false,
    /** The code was sent to `finish` (not preview) at least once. */
    val sent: Boolean = false,
)

class ConnectAttemptStore(context: Context) {
    private val prefs = context.getSharedPreferences("connect_attempt", Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }

    fun load(): ConnectAttempt? = prefs.getString(KEY, null)?.let { runCatching { json.decodeFromString(ConnectAttempt.serializer(), it) }.getOrNull() }

    fun save(a: ConnectAttempt) {
        prefs.edit().putString(KEY, json.encodeToString(ConnectAttempt.serializer(), a)).commit()
    }

    fun clear() {
        prefs.edit().remove(KEY).commit()
    }

    private companion object {
        const val KEY = "attempt"
    }
}

/** Fylvo 1.6: the server calls of connecting from the app; all with the verifier go without redirects. */
class ConnectRepository(private val api: ApiClient, private val session: SessionManager) {
    /** `url` and `attempt` of a new sign-in at [provider] (or [reconnect] of a storage). */
    suspend fun authorize(provider: String, reconnect: String?, challenge: String) =
        api.authService().connectAuthorize(ConnectAuthorizeRequest(provider, reconnect, "app", challenge), session.sessionTag()).unwrap()

    suspend fun preview(code: String, verifier: String): ConnectPreview =
        api.authService().connectPreview(ConnectFinishRequest(code, verifier, preview = true), session.sessionTag()).unwrap()

    suspend fun save(code: String, verifier: String): ConnectedStorage =
        api.authService().connectFinish(ConnectFinishRequest(code, verifier), session.sessionTag()).unwrap()

    suspend fun cancel(code: String, verifier: String) =
        api.authService().connectCancel(ConnectFinishRequest(code, verifier, cancel = true), session.sessionTag()).unwrapEmpty()

    suspend fun rename(id: String, name: String) = api.service().connectRename(id, io.github.svenbuild.fylvo.android.api.ConnectRenameRequest(name), session.sessionTag()).unwrapEmpty()

    suspend fun disconnect(id: String) = api.service().connectDelete(id, session.sessionTag()).unwrapEmpty()
}
