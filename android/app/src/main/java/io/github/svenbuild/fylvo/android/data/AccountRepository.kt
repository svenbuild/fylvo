package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.Me
import io.github.svenbuild.fylvo.android.api.SessionTag
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody

/**
 * Fylvo: the signed-in account (`/user/me`) with its group rights, and the server's explorer
 * switches (map, full-text search). Held in memory for one session only; until the answers are
 * there, everything that depends on them is not offered.
 */
class AccountRepository(private val api: ApiClient, private val session: SessionManager) {

    data class State(
        val me: Me? = null,
        /** Null until known: the server's map provider is set (not empty, not "none"). */
        val mapEnabled: Boolean? = null,
        val fullTextSearch: Boolean = false,
    ) {
        /** The account's group right [bit], or null while unknown. */
        fun group(bit: Int): Boolean? = me?.group?.let { io.github.svenbuild.fylvo.android.util.Permissions.enabled(it.permission, bit) }
    }

    private val _state = MutableStateFlow(State())
    val state: StateFlow<State> = _state.asStateFlow()

    /** Loads both answers; a failure leaves the old state (asked again on the next call). */
    suspend fun refresh() {
        val tag = session.sessionTag()
        val me = runCatching { api.service().me(tag).unwrap() }.getOrNull()
        val explorer = runCatching { api.service().explorerConfig().unwrap() }.getOrNull()
        if (!session.isCurrent(tag)) return
        _state.value = _state.value.let { old ->
            old.copy(
                me = me ?: old.me,
                mapEnabled = explorer?.let { mapOn(it.mapProvider) } ?: old.mapEnabled,
                fullTextSearch = explorer?.fullTextSearch ?: old.fullTextSearch,
            )
        }
    }

    /** Loads once per session; later calls do nothing while the state is complete. */
    suspend fun ensureLoaded() {
        val s = _state.value
        if (s.me == null || s.mapEnabled == null) refresh()
    }

    fun clear() {
        _state.value = State()
    }

    companion object {
        fun mapOn(provider: String): Boolean = provider.isNotBlank() && !provider.equals("none", ignoreCase = true)
    }
}

/**
 * Fylvo 1.6: the account's profile and security (design/android-parity-plan.md, 9). Passwords,
 * codes and the 2FA secret are only passed through, never kept; the requests that carry them use
 * the client without redirects.
 */
class SecurityRepository(private val api: ApiClient, private val account: AccountRepository, private val session: SessionManager) {

    suspend fun settings(): io.github.svenbuild.fylvo.android.api.UserSettings = api.service().securitySettings().unwrap()

    suspend fun setName(name: String) {
        api.service().patchUserSetting(session = session.sessionTag(), patch = kotlinx.serialization.json.buildJsonObject { put("nick", kotlinx.serialization.json.JsonPrimitive(name)) }).unwrapEmpty()
        account.refresh()
    }

    /** 40069: [current] is wrong. */
    suspend fun changePassword(current: String, new: String) {
        api.authService().patchUserSetting(session = session.sessionTag(), patch = kotlinx.serialization.json.buildJsonObject {
            put("current_password", kotlinx.serialization.json.JsonPrimitive(current))
            put("new_password", kotlinx.serialization.json.JsonPrimitive(new))
        }).unwrapEmpty()
    }

    suspend fun twoFaSecret(): String = api.authService().twoFaSecret(session.sessionTag()).unwrap()

    /** Switches 2FA on or off; the code is always sent with it (the server needs both). 40022: wrong code. */
    suspend fun setTwoFa(on: Boolean, code: String) {
        api.authService().patchUserSetting(session = session.sessionTag(), patch = kotlinx.serialization.json.buildJsonObject {
            put("two_fa_enabled", kotlinx.serialization.json.JsonPrimitive(on))
            put("two_fa_code", kotlinx.serialization.json.JsonPrimitive(code))
        }).unwrapEmpty()
    }

    suspend fun revokeGrant(clientId: String) = api.service().revokeGrant(clientId, session.sessionTag()).unwrapEmpty()

    suspend fun signIns(next: Int? = null): io.github.svenbuild.fylvo.android.api.ActivityList = api.service().accountActivity(next).unwrap()

    suspend fun uploadAvatar(jpeg: ByteArray) {
        api.service().uploadAvatar(jpeg.toRequestBody("image/jpeg".toMediaType()), session.sessionTag()).unwrapEmpty()
        account.refresh()
    }

    companion object {
        /** `otpauth://` for authenticator apps (issuer Fylvo, the account's e-mail as its name). */
        fun otpAuthUri(secret: String, email: String): String {
            fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8").replace("+", "%20")
            return "otpauth://totp/Fylvo:${enc(email)}?secret=${enc(secret)}&issuer=Fylvo"
        }

        /** The secret in groups of four, easier to type. */
        fun grouped(secret: String): String = secret.chunked(4).joinToString(" ")
    }
}
