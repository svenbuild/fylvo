package io.github.svenbuild.fylvo.android.util

import android.util.Log
import androidx.annotation.StringRes
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.ApiException
import java.io.IOException

/**
 * Fylvo: a human sentence for a failed request, in the app's language. Server messages and parser
 * texts are never shown; [userError] writes them to the log instead.
 */
object ErrorText {
    // Envelope codes from pkg/serializer/error.go and fylvo_codes.go.
    const val CODE_LOCK_CONFLICT = 40073
    const val CODE_CONFLICT_UPLOAD_ONGOING = 40054
    const val CODE_BATCH_NOT_COMPLETED = 40081
    /** CodeNameCaseTaken: the name differs only in upper and lower case from an existing one. */
    const val CODE_NAME_CASE_TAKEN = 40905

    private val LOCKED = setOf(CODE_LOCK_CONFLICT, CODE_CONFLICT_UPLOAD_ONGOING)
    private val NOT_FOUND = setOf(404, 40016, 40044, 40077, 40078)
    private val PERMISSION = setOf(403, 40053, 40082, 40088, 40089)
    private val TOO_MANY_ITEMS = setOf(40014, 40074)
    private val TOO_MANY_ATTEMPTS = setOf(429, 40900)

    /**
     * The text for [e]. [involvesUpload]: the request touched a file whose upload is not finished,
     * so a lock answer also says that such uploads go away by themselves. [fallback] replaces the
     * generic text for failures that come from the phone itself (code -1).
     */
    fun of(e: Throwable, involvesUpload: Boolean = false, @StringRes fallback: Int? = null): UiText = when {
        e is ApiException -> forCode(e.code, e.innerCodes, involvesUpload, fallback, e.reason)
        e is IOException -> UiText.Res(R.string.error_network)
        else -> UiText.Res(fallback ?: R.string.error_generic)
    }

    private fun forCode(code: Int, innerCodes: List<Int>, involvesUpload: Boolean, @StringRes fallback: Int?, reason: String? = null): UiText {
        if (code == CODE_BATCH_NOT_COMPLETED && innerCodes.isNotEmpty()) {
            // The most telling single failure: a lock first, otherwise the first one.
            val inner = innerCodes.firstOrNull { it in LOCKED } ?: innerCodes.first()
            return forCode(inner, emptyList(), involvesUpload, fallback, reason)
        }
        val id = when (code) {
            in LOCKED -> if (involvesUpload) R.string.error_locked_upload else R.string.error_locked
            in NOT_FOUND -> R.string.error_not_found
            in PERMISSION -> R.string.error_permission
            in TOO_MANY_ITEMS -> R.string.error_too_many_items
            in TOO_MANY_ATTEMPTS -> R.string.error_too_many_attempts
            ApiException.CODE_LOGIN_REQUIRED -> R.string.error_login_required
            ApiException.CODE_OBJECT_EXISTS -> R.string.error_exists
            CODE_NAME_CASE_TAKEN -> R.string.error_name_case_taken
            40051 -> R.string.error_quota
            40052 -> R.string.error_bad_name
            40049 -> R.string.error_file_too_large
            40050 -> R.string.error_file_type
            40019 -> R.string.error_feature_off
            // Fylvo: connected storages (Google Drive, OneDrive).
            Connected.CODE_UNSUPPORTED -> R.string.error_connected_unsupported
            Connected.CODE_RECONNECT -> R.string.error_connected_reconnect
            Connected.CODE_UNAVAILABLE -> R.string.error_connected_unavailable
            Connected.CODE_BUSY -> R.string.error_connected_busy
            Connected.CODE_ITEM -> connectedItemText(reason)
            Connected.CODE_LISTING_EXPIRED -> R.string.error_connected_listing_expired
            Connected.CODE_FULL -> R.string.error_connected_full
            Connected.CODE_OUTCOME_UNKNOWN -> R.string.error_connected_outcome_unknown
            in 500..599, in 50000..50999 -> R.string.error_server
            else -> null
        }
        return when {
            id != null -> UiText.Res(id)
            code <= 0 -> UiText.Res(fallback ?: R.string.error_generic)
            else -> UiText.Res(R.string.error_generic_code, code)
        }
    }
}

/** Fylvo: the text for 40925 by its `data.reason`; without one the general text. */
@StringRes
private fun connectedItemText(reason: String?): Int = when (reason) {
    "ambiguous" -> R.string.error_connected_ambiguous
    "changed" -> R.string.error_connected_changed
    "forbidden" -> R.string.error_connected_forbidden
    "flagged" -> R.string.error_connected_flagged
    "export_too_large" -> R.string.error_connected_export_too_large
    "daily_limit" -> R.string.error_connected_daily_limit
    "too_large" -> R.string.error_connected_too_large
    else -> R.string.error_connected_item
}

/** [ErrorText.of] that also logs the raw text of [e] (server message or parser text, never tokens). */
fun userError(e: Throwable, involvesUpload: Boolean = false, @StringRes fallback: Int? = null): UiText {
    val code = (e as? ApiException)?.code
    Log.w("Fylvo", "Request failed${code?.let { " (code $it)" }.orEmpty()}: ${e.javaClass.simpleName}: ${e.message}")
    return ErrorText.of(e, involvesUpload, fallback)
}
