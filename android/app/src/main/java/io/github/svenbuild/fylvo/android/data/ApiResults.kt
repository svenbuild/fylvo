package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiResponse
import retrofit2.Response

/** The `data` of a successful answer; any other answer becomes an [ApiException]. */
internal fun <T> Response<ApiResponse<T>>.unwrap(): T {
    val body = checked()
    return body.data ?: throw ApiException(body.code, body.msg.ifEmpty { "API error ${body.code}" })
}

/** Like [unwrap] for answers without `data`. */
internal fun <T> Response<ApiResponse<T>>.unwrapEmpty() {
    checked()
}

private fun <T> Response<ApiResponse<T>>.checked(): ApiResponse<T> {
    if (!isSuccessful) throw ApiException(code(), "HTTP ${code()}")
    val body = body() ?: throw ApiException(-1, "Empty response")
    if (body.code != 0) {
        throw ApiException(body.code, body.msg.ifEmpty { "API error ${body.code}" }, body.innerCodes, body.reason, body.failedPaths)
    }
    return body
}
