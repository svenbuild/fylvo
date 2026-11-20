package io.github.svenbuild.fylvo.android.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.LoginResponse
import io.github.svenbuild.fylvo.android.api.NewPasswordRequest
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
    /**
     * Fylvo: non-null when the password was right but the account must set a new one first.
     * The ticket lives only here (no log, no saved state) and is valid for ten minutes.
     */
    val passwordChangeTicket: String? = null,
    val newPassword: String = "",
    val newPasswordRepeat: String = "",
) {
    /** No passwords or tickets in logs or crash reports. */
    override fun toString() = "LoginUiState(step=${step()}, loading=$loading, loggedIn=$loggedIn)"

    fun step(): LoginStep = when {
        passwordChangeTicket != null -> LoginStep.NewPassword
        twoFactorSessionId != null -> LoginStep.TwoFactor
        else -> LoginStep.Password
    }
}

enum class LoginStep { Password, TwoFactor, NewPassword }

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

    fun onNewPasswordChange(v: String) = _state.value.let { _state.value = it.copy(newPassword = v, error = null) }
    fun onNewPasswordRepeatChange(v: String) = _state.value.let { _state.value = it.copy(newPasswordRepeat = v, error = null) }

    /** Fylvo: leave the password change and sign in again (the ticket is dropped). */
    fun cancelPasswordChange() = _state.value.let {
        _state.value = it.copy(passwordChangeTicket = null, newPassword = "", newPasswordRepeat = "", password = "", error = null)
    }

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
                loginRefusal(resp.isSuccessful, resp.code(), body?.code, body?.msg, body?.data != null)
                    ?.let { throw LoginError(it) }
                // loginRefusal returned null: the body and its data are there.
                val data = checkNotNull(body?.data)
                when (body.code) {
                    // Password accepted; the server wants a TOTP code before issuing tokens.
                    // `data` is the 2FA session id, valid for ten minutes.
                    CODE_NOT_FULLY_SUCCESS -> {
                        val sessionId = (data as? JsonPrimitive)?.content
                            ?: throw LoginError(UiText.Res(R.string.two_factor_without_session))
                        _state.value = _state.value.copy(loading = false, twoFactorSessionId = sessionId, otp = "")
                    }
                    CODE_PASSWORD_CHANGE_REQUIRED -> startPasswordChange(data)
                    0 -> completeLogin(api.json.decodeFromJsonElement(LoginResponse.serializer(), data))
                    else -> throw LoginError(UiText.Res(R.string.login_failed))
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
        else -> {
            android.util.Log.w("FylvoLogin", "Sign-in failed: ${e.javaClass.simpleName}")
            UiText.Res(R.string.login_failed)
        }
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
                if (resp.isSuccessful && body?.code == CODE_PASSWORD_CHANGE_REQUIRED && body.data != null) {
                    startPasswordChange(body.data)
                    return@launch
                }
                if (!resp.isSuccessful || body == null || body.code != 0 || body.data == null) {
                    // A 404 means the ten-minute 2FA session expired: start over.
                    val expired = body?.code == CODE_NOT_FOUND
                    _state.value = _state.value.copy(
                        loading = false,
                        twoFactorSessionId = if (expired) null else sessionId,
                        otp = "",
                        error = if (expired) UiText.Res(R.string.sign_in_timed_out)
                        else if (body != null && body.code != 0) serverError(body.code, body.msg)
                        else UiText.Res(R.string.login_failed_http, resp.code()),
                    )
                    return@launch
                }
                completeLogin(api.json.decodeFromJsonElement(LoginResponse.serializer(), body.data))
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, otp = "", error = describe(e))
            }
        }
    }

    /** The password was right, but the account must set a new one: show that step. */
    private fun startPasswordChange(data: JsonElement) {
        val ticket = (data as? JsonPrimitive)?.takeIf { it.isString }?.content?.ifBlank { null }
            ?: throw LoginError(UiText.Res(R.string.login_failed))
        _state.value = _state.value.copy(
            loading = false, twoFactorSessionId = null, otp = "",
            passwordChangeTicket = ticket, newPassword = "", newPasswordRepeat = "", error = null,
        )
    }

    /** Fylvo: store the new password with the ticket; on success the person is signed in. */
    fun setNewPassword() {
        val s = _state.value
        val ticket = s.passwordChangeTicket ?: return
        newPasswordProblem(s.newPassword, s.newPasswordRepeat)?.let {
            _state.value = s.copy(error = it)
            return
        }
        _state.value = s.copy(loading = true, error = null)
        viewModelScope.launch {
            try {
                val resp = api.authService().setNewPassword(NewPasswordRequest(sessionId = ticket, password = s.newPassword))
                val body = resp.body()
                when (val outcome = passwordChangeOutcome(resp.isSuccessful, resp.code(), body?.code, body?.msg, body?.data != null)) {
                    PasswordChangeOutcome.Done ->
                        completeLogin(api.json.decodeFromJsonElement(LoginResponse.serializer(), checkNotNull(body?.data)))
                    PasswordChangeOutcome.Expired -> _state.value = _state.value.copy(
                        loading = false, passwordChangeTicket = null, newPassword = "", newPasswordRepeat = "",
                        password = "", error = UiText.Res(R.string.sign_in_timed_out),
                    )
                    is PasswordChangeOutcome.Retry -> _state.value = _state.value.copy(loading = false, error = outcome.error)
                }
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = describe(e))
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
        _state.value = _state.value.copy(
            loading = false, loggedIn = true, twoFactorSessionId = null, otp = "",
            passwordChangeTicket = null, password = "", newPassword = "", newPasswordRepeat = "",
        )
    }

    companion object {
        /** serializer.CodeInvalidPassword: wrong email address or password. */
        const val CODE_INVALID_PASSWORD = 40080
        /** serializer.CodeTooManyAttempts (Fylvo): the server locks account and address for 15 minutes. */
        const val CODE_TOO_MANY_ATTEMPTS = 40900

        /** serializer.CodeUserBaned / CodeUserNotActivated. */
        const val CODE_USER_BLOCKED = 40017
        const val CODE_USER_NOT_ACTIVATED = 40018

        /** serializer.Code2FACodeErr: wrong two-step code. */
        const val CODE_WRONG_2FA_CODE = 40022

        /**
         * The error to show for an answer of the password sign-in, or null when it goes on
         * (signed in, or the 2FA step). Refusals such as a wrong password or the lock come
         * as HTTP 200 with a code and no data, so the code is checked before the data.
         */
        fun loginRefusal(httpOk: Boolean, httpCode: Int, code: Int?, msg: String?, hasData: Boolean): UiText? = when {
            !httpOk || code == null -> UiText.Res(R.string.login_failed_http, httpCode)
            code != 0 && code != CODE_NOT_FULLY_SUCCESS && code != CODE_PASSWORD_CHANGE_REQUIRED -> serverError(code, msg.orEmpty())
            !hasData -> UiText.Res(R.string.login_failed)
            else -> null
        }

        /** Sign-in refusals in the app's own words and language; the server's text is never shown. */
        fun serverError(code: Int, msg: String): UiText = when (code) {
            CODE_INVALID_PASSWORD -> UiText.Res(R.string.login_wrong_password)
            CODE_TOO_MANY_ATTEMPTS -> UiText.Res(R.string.login_too_many_attempts)
            CODE_WRONG_2FA_CODE -> UiText.Res(R.string.login_wrong_code)
            CODE_USER_BLOCKED -> UiText.Res(R.string.login_account_blocked)
            CODE_USER_NOT_ACTIVATED -> UiText.Res(R.string.login_account_inactive)
            // Other codes: the app's own words with the code.
            else -> UiText.Res(R.string.login_failed_code, code)
        }

        /** serializer.CodePasswordChangeRequired (Fylvo): data is the ticket for the new password. */
        const val CODE_PASSWORD_CHANGE_REQUIRED = 40901
        /** serializer.CodeSamePassword (Fylvo): the new password equals the current one. */
        const val CODE_SAME_PASSWORD = 40902
        /** serializer.CodePasswordChangeExpired (Fylvo): the ticket is unknown or expired. */
        const val CODE_PASSWORD_CHANGE_EXPIRED = 40903
        /** serializer.CodeParamErr: here, the new password is too short or too long. */
        const val CODE_PARAM_ERR = 40001

        /** The server takes 6 to 128 characters. */
        const val MIN_PASSWORD_LENGTH = 6
        const val MAX_PASSWORD_LENGTH = 128

        /** A problem with the typed new password, checked before it is sent; null when fine. */
        fun newPasswordProblem(password: String, repeat: String): UiText? = when {
            password.length !in MIN_PASSWORD_LENGTH..MAX_PASSWORD_LENGTH -> UiText.Res(R.string.new_password_length)
            password != repeat -> UiText.Res(R.string.new_password_mismatch)
            else -> null
        }

        /** What the answer to the new password means. */
        fun passwordChangeOutcome(httpOk: Boolean, httpCode: Int, code: Int?, msg: String?, hasData: Boolean): PasswordChangeOutcome = when {
            !httpOk || code == null -> PasswordChangeOutcome.Retry(UiText.Res(R.string.login_failed_http, httpCode))
            code == 0 && hasData -> PasswordChangeOutcome.Done
            code == 0 -> PasswordChangeOutcome.Retry(UiText.Res(R.string.login_failed))
            code == CODE_PASSWORD_CHANGE_EXPIRED -> PasswordChangeOutcome.Expired
            code == CODE_SAME_PASSWORD -> PasswordChangeOutcome.Retry(UiText.Res(R.string.new_password_same))
            code == CODE_PARAM_ERR -> PasswordChangeOutcome.Retry(UiText.Res(R.string.new_password_length))
            else -> PasswordChangeOutcome.Retry(serverError(code, msg.orEmpty()))
        }

        /** serializer.CodeNotFullySuccess: password accepted, 2FA code still required. */
        const val CODE_NOT_FULLY_SUCCESS = 203
        /** serializer.CodeNotFound: here, the 2FA session expired. */
        private const val CODE_NOT_FOUND = 404

    }
}

/** Fylvo: the answer to a new password at sign-in. */
sealed interface PasswordChangeOutcome {
    /** Stored; the answer holds the tokens like a normal sign-in. */
    data object Done : PasswordChangeOutcome
    /** The ticket is gone: back to the sign-in. */
    data object Expired : PasswordChangeOutcome
    /** Show the error, the ticket stays valid for another try. */
    data class Retry(val error: UiText) : PasswordChangeOutcome
}
