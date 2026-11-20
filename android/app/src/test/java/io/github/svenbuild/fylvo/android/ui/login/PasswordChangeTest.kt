package io.github.svenbuild.fylvo.android.ui.login

import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.util.UiText
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo: the forced password change at sign-in (server codes 40901 to 40903). */
class PasswordChangeTest {

    private fun id(text: UiText?) = (text as UiText.Res).id

    @Test
    fun passwordChangeRequiredIsNotARefusal() {
        // The answer carries the ticket as data; the app shows the new-password step.
        assertNull(LoginViewModel.loginRefusal(true, 200, 40901, "Bitte zuerst im Browser anmelden", hasData = true))
    }

    @Test
    fun stepFollowsTheState() {
        assertEquals(LoginStep.Password, LoginUiState().step())
        assertEquals(LoginStep.TwoFactor, LoginUiState(twoFactorSessionId = "s").step())
        assertEquals(LoginStep.NewPassword, LoginUiState(twoFactorSessionId = "s", passwordChangeTicket = "t").step())
    }

    @Test
    fun stateTextHasNoSecrets() {
        val text = LoginUiState(
            password = "old-secret", passwordChangeTicket = "ticket-123",
            newPassword = "new-secret", newPasswordRepeat = "new-secret",
        ).toString()
        assertFalse(text.contains("secret"))
        assertFalse(text.contains("ticket-123"))
    }

    @Test
    fun newPasswordIsCheckedBeforeSending() {
        assertEquals(R.string.new_password_length, id(LoginViewModel.newPasswordProblem("12345", "12345")))
        assertEquals(R.string.new_password_length, id(LoginViewModel.newPasswordProblem("a".repeat(129), "a".repeat(129))))
        assertEquals(R.string.new_password_mismatch, id(LoginViewModel.newPasswordProblem("123456", "123457")))
        assertNull(LoginViewModel.newPasswordProblem("123456", "123456"))
        assertNull(LoginViewModel.newPasswordProblem("a".repeat(128), "a".repeat(128)))
    }

    @Test
    fun successSignsIn() {
        assertEquals(PasswordChangeOutcome.Done, LoginViewModel.passwordChangeOutcome(true, 200, 0, "", hasData = true))
    }

    @Test
    fun expiredTicketGoesBackToSignIn() {
        assertEquals(PasswordChangeOutcome.Expired, LoginViewModel.passwordChangeOutcome(true, 200, 40903, "Session expired", hasData = false))
    }

    @Test
    fun samePasswordLetsTryAgain() {
        val outcome = LoginViewModel.passwordChangeOutcome(true, 200, 40902, "The new password must differ", hasData = false)
        assertTrue(outcome is PasswordChangeOutcome.Retry)
        assertEquals(R.string.new_password_same, id((outcome as PasswordChangeOutcome.Retry).error))
    }

    @Test
    fun lengthAndLockUseOwnTexts() {
        val length = LoginViewModel.passwordChangeOutcome(true, 200, 40001, "Parameter error", hasData = false)
        assertEquals(R.string.new_password_length, id((length as PasswordChangeOutcome.Retry).error))
        val lock = LoginViewModel.passwordChangeOutcome(true, 200, 40900, "Too many wrong attempts", hasData = false)
        assertEquals(R.string.login_too_many_attempts, id((lock as PasswordChangeOutcome.Retry).error))
    }

    @Test
    fun httpErrorsAndEmptySuccessAreRetries() {
        val http = LoginViewModel.passwordChangeOutcome(false, 502, null, null, hasData = false)
        assertEquals(R.string.login_failed_http, id((http as PasswordChangeOutcome.Retry).error))
        val empty = LoginViewModel.passwordChangeOutcome(true, 200, 0, "", hasData = false)
        assertEquals(R.string.login_failed, id((empty as PasswordChangeOutcome.Retry).error))
    }
}
