package io.github.svenbuild.fylvo.android.util

import kotlinx.serialization.SerializationException
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.ApiException
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test
import java.net.SocketTimeoutException

class ErrorTextTest {
    private fun idOf(text: UiText): Int = (text as UiText.Res).id

    private fun idFor(code: Int, inner: List<Int> = emptyList(), upload: Boolean = false) =
        idOf(ErrorText.of(ApiException(code, "raw server text", inner), involvesUpload = upload))

    @Test
    fun lockConflictHasItsOwnText() {
        assertEquals(R.string.error_locked, idFor(40073))
        assertEquals(R.string.error_locked, idFor(40054))
    }

    @Test
    fun lockOnAnUnfinishedUploadAddsThe48Hours() {
        assertEquals(R.string.error_locked_upload, idFor(40073, upload = true))
        // Other errors stay as they are.
        assertEquals(R.string.error_not_found, idFor(40016, upload = true))
    }

    @Test
    fun commonCodesAreMapped() {
        assertEquals(R.string.error_exists, idFor(40004))
        assertEquals(R.string.error_not_found, idFor(40016))
        assertEquals(R.string.error_not_found, idFor(40044))
        assertEquals(R.string.error_not_found, idFor(404))
        assertEquals(R.string.error_quota, idFor(40051))
        assertEquals(R.string.error_permission, idFor(403))
        assertEquals(R.string.error_permission, idFor(40082))
        assertEquals(R.string.error_login_required, idFor(401))
        assertEquals(R.string.error_too_many_attempts, idFor(40900))
        assertEquals(R.string.error_too_many_attempts, idFor(429))
        assertEquals(R.string.error_bad_name, idFor(40052))
        assertEquals(R.string.error_too_many_items, idFor(40074))
        assertEquals(R.string.error_server, idFor(502))
        assertEquals(R.string.error_server, idFor(50001))
    }

    @Test
    fun unknownCodesShowTheCodeOnly() {
        val text = ErrorText.of(ApiException(40099, "raw server text")) as UiText.Res
        assertEquals(R.string.error_generic_code, text.id)
        assertArrayEquals(arrayOf<Any>(40099), text.args)
    }

    @Test
    fun batchAnswerUsesTheSingleFailures() {
        assertEquals(R.string.error_locked, idFor(40081, inner = listOf(40016, 40073)))
        assertEquals(R.string.error_not_found, idFor(40081, inner = listOf(40016)))
        assertEquals(R.string.error_generic_code, idFor(40081))
    }

    @Test
    fun parserAndPhoneErrorsNeverShowTheirText() {
        assertEquals(R.string.error_generic, idOf(ErrorText.of(SerializationException("Unexpected JSON token at offset 21"))))
        assertEquals(R.string.error_generic, idOf(ErrorText.of(IllegalStateException("boom"))))
        assertEquals(R.string.error_network, idOf(ErrorText.of(SocketTimeoutException("timeout"))))
        assertEquals(R.string.download_failed, idOf(ErrorText.of(ApiException(-1, "Empty download"), fallback = R.string.download_failed)))
        assertEquals(R.string.error_generic, idOf(ErrorText.of(ApiException(0, "API error 0"))))
    }
}
