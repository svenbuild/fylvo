package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ApiResponse
import io.github.svenbuild.fylvo.android.api.CapacityResponse
import io.github.svenbuild.fylvo.android.api.ConnectedOverview
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
