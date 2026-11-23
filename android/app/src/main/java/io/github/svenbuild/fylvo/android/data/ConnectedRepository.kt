package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ApiResponse
import io.github.svenbuild.fylvo.android.api.CapacityResponse
import io.github.svenbuild.fylvo.android.api.ConnectedOverview
import io.github.svenbuild.fylvo.android.api.ConnectedUploadState
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import retrofit2.Response

/** Fylvo: what `GET fylvo/connected` gave. */
sealed interface OverviewResult {
    data class Found(val overview: ConnectedOverview) : OverviewResult
    /** The server does not have connected storages (an older Fylvo): show nothing, say nothing. */
    data object Missing : OverviewResult
    /** Network or server trouble: keep what was shown before. */
    data object Failed : OverviewResult
}

/** Fylvo: the account's connected storages and their usage. */
class ConnectedRepository(private val api: ApiClient) {

    /** Throws [ApiException] with [ApiException.CODE_LOGIN_REQUIRED] when the session is gone. */
    suspend fun overview(): OverviewResult {
        val response = try {
            api.service().connectedStorages()
        } catch (e: CancellationException) {
            throw e
        } catch (_: SerializationException) {
            // Not the API's JSON (an older server answers unknown routes with the web page).
            return OverviewResult.Missing
        } catch (_: Exception) {
            return OverviewResult.Failed
        }
        return overviewOf(response)
    }

    /** The page of the web app where storages are connected; with [reconnect] it points at that one. */
    suspend fun webSettingsUrl(reconnect: String? = null): String = settingsUrl(api.serverBase(), reconnect)

    /**
     * Where the upload [session] stands. A session the server no longer knows (40016, HTTP 404)
     * reads as `unknown`. Network trouble throws an IOException, any other refusal an ApiException.
     */
    suspend fun uploadState(session: String): ConnectedUploadState {
        val response = api.editService().connectedUploadState(session)
        if (response.code() == 404) return ConnectedUploadState(state = UPLOAD_UNKNOWN)
        if (response.code() in 500..599) throw java.io.IOException("server answered ${response.code()}")
        val body = response.body() ?: throw java.io.IOException("no answer from the server")
        return when (body.code) {
            0 -> body.data ?: throw java.io.IOException("empty answer")
            ApiException.CODE_PATH_NOT_EXIST -> ConnectedUploadState(state = UPLOAD_UNKNOWN)
            else -> throw ApiException(body.code, body.msg)
        }
    }

    /** Usage of one storage; null when it cannot be read now. */
    suspend fun capacity(id: String): CapacityResponse? = try {
        val body = api.service().connectedCapacity(id).takeIf { it.isSuccessful }?.body()
        body?.data?.takeIf { body.code == 0 }
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }
}

const val UPLOAD_RECEIVING = "receiving"
const val UPLOAD_DONE = "done"
const val UPLOAD_FAILED = "failed"
const val UPLOAD_UNKNOWN = "unknown"

/** Fylvo: the decision of [ConnectedRepository.overview] for one answer (unit-tested). */
internal fun overviewOf(response: Response<ApiResponse<ConnectedOverview>>): OverviewResult {
    if (response.code() == 404) return OverviewResult.Missing
    if (response.code() == 401) throw ApiException(ApiException.CODE_LOGIN_REQUIRED, "HTTP 401")
    if (!response.isSuccessful) return OverviewResult.Failed
    val body = response.body() ?: return OverviewResult.Failed
    return when (body.code) {
        0 -> body.data?.let { OverviewResult.Found(it) } ?: OverviewResult.Failed
        ApiException.CODE_LOGIN_REQUIRED -> throw ApiException(body.code, body.msg)
        404 -> OverviewResult.Missing
        else -> OverviewResult.Failed
    }
}

/** Fylvo: `/settings?tab=connected[&reconnect=<id>]` below the server address (unit-tested). */
internal fun settingsUrl(server: String, reconnect: String?): String {
    val base = server.trimEnd('/') + "/settings?tab=connected"
    return if (reconnect.isNullOrEmpty()) base else base + "&reconnect=" + java.net.URLEncoder.encode(reconnect, "UTF-8")
}
