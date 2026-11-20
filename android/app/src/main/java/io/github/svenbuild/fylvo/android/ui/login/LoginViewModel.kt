package io.github.svenbuild.fylvo.android.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonPrimitive
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.LoginResponse
import io.github.svenbuild.fylvo.android.api.OtpLoginRequest
import io.github.svenbuild.fylvo.android.api.PasswordLoginRequest
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.normalizeServerUrl
import io.github.svenbuild.fylvo.android.util.UiText

/** Fylvo's own server. Prefilled so nobody has to type it, but it can still be changed. */
const val DEFAULT_SERVER_URL = "https://cloud.example.com"

data class LoginUiState(
    val serverUrl: String = DEFAULT_SERVER_URL,
    val email: String = "",
    val password: String = "",
    val loading: Boolean = false,
    val error: UiText? = null,
    val loggedIn: Boolean = false,
    /** Non-null once the server has accepted the password and is waiting for a TOTP code. */
    val twoFactorSessionId: String? = null,
    val otp: String = "",
)

class LoginViewModel(
    private val session: SessionManager,
    private val api: ApiClient,
) : ViewModel() {

    private val _state = MutableStateFlow(LoginUiState())
    val state: StateFlow<LoginUiState> = _state

    fun onServerUrlChange(v: String) = _state.value.let { _state.value = it.copy(serverUrl = v, error = null) }
    fun onEmailChange(v: String) = _state.value.let { _state.value = it.copy(email = v, error = null) }
    fun onPasswordChange(v: String) = _state.value.let { _state.value = it.copy(password = v, error = null) }
    fun onOtpChange(v: String) = _state.value.let { _state.value = it.copy(otp = v.filter(Char::isDigit).take(6), error = null) }

    /** Abandon the pending 2FA challenge and return to the password form. */
    fun cancelTwoFactor() = _state.value.let { _state.value = it.copy(twoFactorSessionId = null, otp = "", error = null) }


    fun login() {
        val s0 = _state.value
        if (s0.serverUrl.isBlank() || s0.email.isBlank() || s0.password.isBlank()) {
            _state.value = s0.copy(error = UiText.Res(R.string.fill_in_login))
            return
        }
        val s = s0
        _state.value = s.copy(loading = true, error = null)
        val serverUrl = normalizeServerUrl(s.serverUrl)
        viewModelScope.launch {
            try {
                session.saveServerUrl(serverUrl)
                val resp = api.authService().login(PasswordLoginRequest(s.email, s.password))
                val body = resp.body()
                if (!resp.isSuccessful || body == null || body.data == null) {
                    throw body?.msg?.ifBlank { null }?.let(::Exception) ?: LoginError(UiText.Res(R.string.login_failed_http, resp.code()))
                }
                when (body.code) {
                    // Password accepted; the server wants a TOTP code before issuing tokens.
                    // `data` is the 2FA session id, valid for ten minutes.
                    CODE_NOT_FULLY_SUCCESS -> {
                        val sessionId = (body.data as? JsonPrimitive)?.content
                            ?: throw LoginError(UiText.Res(R.string.two_factor_without_session))
                        _state.value = _state.value.copy(loading = false, twoFactorSessionId = sessionId, otp = "")
                    }
                    0 -> completeLogin(api.json.decodeFromJsonElement(LoginResponse.serializer(), body.data))
                    else -> throw body.msg.ifBlank { null }?.let(::Exception) ?: LoginError(UiText.Res(R.string.login_failed))
                }
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = describe(e))
            }
        }
    }

    /** Network failures in words a person can act on, instead of OkHttp's internals. */
    private fun describe(e: Exception): UiText = when {
        e is LoginError -> e.text
        // Fylvo: plain http:// is blocked by the network security config (release builds always,
        // debug builds except for the test addresses in src/debug).
        e is java.net.UnknownServiceException || e.message?.contains("CLEARTEXT", ignoreCase = true) == true ->
            UiText.Res(R.string.https_only)
        e is javax.net.ssl.SSLException || e.message?.contains("TLS", ignoreCase = true) == true ->
            UiText.Res(R.string.secure_connection_failed)
        e is java.net.UnknownHostException -> UiText.Res(R.string.server_not_found)
        e is java.net.ConnectException -> UiText.Res(R.string.server_unreachable)
        e is java.net.SocketTimeoutException -> UiText.Res(R.string.server_timeout)
        else -> e.message?.let { UiText.Raw(it) } ?: UiText.Res(R.string.login_failed)
    }

    /** A sign-in failure of our own wording (server messages travel as plain exceptions). */
    private class LoginError(val text: UiText) : Exception()

    /** Second step of a password login: submit the TOTP code for the pending 2FA session. */
    fun verify2FA() {
        val s = _state.value
        val sessionId = s.twoFactorSessionId ?: return
        if (s.otp.length != 6) {
            _state.value = s.copy(error = UiText.Res(R.string.enter_code))
            return
        }
        _state.value = s.copy(loading = true, error = null)
        viewModelScope.launch {
            try {
                val resp = api.authService().verify2FA(OtpLoginRequest(otp = s.otp, sessionId = sessionId))
                val body = resp.body()
                if (!resp.isSuccessful || body == null || body.code != 0 || body.data == null) {
                    // A 404 means the ten-minute 2FA session expired: start over.
                    val expired = body?.code == CODE_NOT_FOUND
                    _state.value = _state.value.copy(
                        loading = false,
                        twoFactorSessionId = if (expired) null else sessionId,
                        otp = "",
                        error = if (expired) UiText.Res(R.string.sign_in_timed_out)
                        else body?.msg?.ifBlank { null }?.let { UiText.Raw(it) }
                            ?: UiText.Res(R.string.incorrect_code_http, resp.code()),
                    )
                    return@launch
                }
                completeLogin(body.data)
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, otp = "", error = e.message?.let { UiText.Raw(it) } ?: UiText.Res(R.string.verification_failed))
            }
        }
    }

    private suspend fun completeLogin(login: LoginResponse) {
        session.saveSession(
            accessToken = login.token.accessToken,
            refreshToken = login.token.refreshToken,
            accessExpiresAt = ApiClient.parseExpiresAt(login.token.accessExpires),
            email = login.user.email,
            nick = login.user.nickname,
        )
        _state.value = _state.value.copy(loading = false, loggedIn = true, twoFactorSessionId = null, otp = "")
    }

    companion object {
        /** serializer.CodeNotFullySuccess: password accepted, 2FA code still required. */
        private const val CODE_NOT_FULLY_SUCCESS = 203
        /** serializer.CodeNotFound: here, the 2FA session expired. */
        private const val CODE_NOT_FOUND = 404

    }
}
