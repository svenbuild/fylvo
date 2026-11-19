package io.github.svenbuild.fylvo.android.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonPrimitive
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.LoginResponse
import io.github.svenbuild.fylvo.android.api.OtpLoginRequest
import io.github.svenbuild.fylvo.android.api.PasswordLoginRequest
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.normalizeServerUrl

data class LoginUiState(
    val serverUrl: String = "",
    val email: String = "",
    val password: String = "",
    val loading: Boolean = false,
    val error: String? = null,
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
            _state.value = s0.copy(error = "Fill in server, email and password")
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
                    throw Exception(body?.msg ?: "Login failed (HTTP ${resp.code()})")
                }
                when (body.code) {
                    // Password accepted; the server wants a TOTP code before issuing tokens.
                    // `data` is the 2FA session id, valid for ten minutes.
                    CODE_NOT_FULLY_SUCCESS -> {
                        val sessionId = (body.data as? JsonPrimitive)?.content
                            ?: throw Exception("Server requested 2FA without a session id")
                        _state.value = _state.value.copy(loading = false, twoFactorSessionId = sessionId, otp = "")
                    }
                    0 -> completeLogin(api.json.decodeFromJsonElement(LoginResponse.serializer(), body.data))
                    else -> throw Exception(body.msg.ifBlank { "Login failed" })
                }
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = describe(e))
            }
        }
    }

    /** Network failures in words a person can act on, instead of OkHttp's internals. */
    private fun describe(e: Exception): String = when {
        // Fylvo: plain http:// is blocked by the network security config (release builds always,
        // debug builds except for the test addresses in src/debug).
        e is java.net.UnknownServiceException || e.message?.contains("CLEARTEXT", ignoreCase = true) == true ->
            "Fylvo only connects over https. Use the https:// address of your server."
        e is javax.net.ssl.SSLException || e.message?.contains("TLS", ignoreCase = true) == true ->
            "Couldn't make a secure connection. Check the address and the server's certificate."
        e is java.net.UnknownHostException -> "Server not found. Check the address."
        e is java.net.ConnectException -> "Couldn't reach the server. Is it online?"
        e is java.net.SocketTimeoutException -> "The server took too long to respond."
        else -> e.message ?: "Login failed"
    }

    /** Second step of a password login: submit the TOTP code for the pending 2FA session. */
    fun verify2FA() {
        val s = _state.value
        val sessionId = s.twoFactorSessionId ?: return
        if (s.otp.length != 6) {
            _state.value = s.copy(error = "Enter the 6-digit code from your authenticator app")
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
                        error = if (expired) "Sign-in timed out, please enter your password again"
                        else body?.msg?.ifBlank { null } ?: "Incorrect code (HTTP ${resp.code()})",
                    )
                    return@launch
                }
                completeLogin(body.data)
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, otp = "", error = e.message ?: "Verification failed")
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
