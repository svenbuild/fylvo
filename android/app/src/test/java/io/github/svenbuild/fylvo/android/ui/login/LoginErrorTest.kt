package io.github.svenbuild.fylvo.android.ui.login

import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.util.UiText
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class LoginErrorTest {
    @Test
    fun wrongPasswordUsesOwnText() {
        val text = LoginViewModel.serverError(40080, "Incorrect password or email address")
        assertTrue(text is UiText.Res)
        assertEquals(R.string.login_wrong_password, (text as UiText.Res).id)
    }

    @Test
    fun serverLockUsesOwnText() {
        val text = LoginViewModel.serverError(40900, "Too many wrong attempts, please try again later")
        assertTrue(text is UiText.Res)
        assertEquals(R.string.login_too_many_attempts, (text as UiText.Res).id)
    }

    @Test
    fun otherCodesShowTheServerMessage() {
        assertEquals(UiText.Raw("This account has been blocked"), LoginViewModel.serverError(40017, "This account has been blocked"))
    }

    @Test
    fun emptyMessageFallsBackToGenericText() {
        val text = LoginViewModel.serverError(40017, "")
        assertEquals(R.string.login_failed, (text as UiText.Res).id)
    }
}
