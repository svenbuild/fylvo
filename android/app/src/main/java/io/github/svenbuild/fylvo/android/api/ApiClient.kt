package io.github.svenbuild.fylvo.android.api

import com.jakewharton.retrofit2.converter.kotlinx.serialization.asConverterFactory
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import io.github.svenbuild.fylvo.android.data.SessionManager
import retrofit2.Retrofit
import java.util.concurrent.TimeUnit

/**
 * ApiClient owns the OkHttp/Retrofit wiring for one server. Requests to the
 * API host carry `Authorization: Bearer <access>`.
 *
 * Token refresh is single-flight and happens in two places: proactively when
 * the access token is about to expire, and reactively when the server answers
 * `{"code":401}`. Cloudreve reports "login required" inside a 200 body, so an
 * OkHttp `Authenticator` (HTTP 401 only) would never fire.
 */
class ApiClient(private val session: SessionManager) {

    /** Lenient decoder shared with callers that unwrap `JsonElement` payloads by hand. */
    val json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
    }

    private val refreshMutex = Mutex()

    @Volatile
    private var cachedBaseUrl: String? = null

    @Volatile
    private var cachedService: ApiService? = null

    @Volatile
    private var cachedAuthService: ApiService? = null

    suspend fun serverBase(): String = session.serverUrlNow()

    suspend fun service(): ApiService {
        val base = session.serverUrlNow().ifEmpty { "http://localhost" } + "/"
        cachedService?.let { if (cachedBaseUrl == base) return it }
        return buildService(base, followRedirects = true).also {
            cachedService = it
            cachedAuthService = null
            cachedBaseUrl = base
        }
    }

    /**
     * Fylvo: the same API without redirects, for requests whose body carries a password or a
     * refresh token (sign-in, 2FA, sign-out). OkHttp keeps the body on 307/308 redirects, and
     * a redirect could point to another server.
     */
    suspend fun authService(): ApiService {
        val base = session.serverUrlNow().ifEmpty { "http://localhost" } + "/"
        cachedAuthService?.let { if (cachedBaseUrl == base) return it }
        return buildService(base, followRedirects = false).also { cachedAuthService = it }
    }

    private fun buildService(baseUrl: String, followRedirects: Boolean): ApiService {
        // Only our own API host may receive the Cloudreve bearer token. Chunk uploads and
        // downloads for S3-style policies go to presigned object-storage URLs on a
        // different host; S3/Ceph rejects a presigned request that also carries an
        // Authorization header ("only one auth mechanism allowed", HTTP 400), so attaching
        // the token there breaks every direct upload and download.
        // Fylvo: the token goes only to the exact origin (scheme, host and port) of the server the
        // session belongs to, and only while that is still the signed-in server.
        val apiOrigin = baseUrl.toHttpUrlOrNull()?.let(::originOf)

        val sessionInterceptor = Interceptor { chain ->
            val original = chain.request()
            val sameOrigin = apiOrigin != null && originOf(original.url) == apiOrigin &&
                runBlocking { session.serverUrlNow() + "/" }.toHttpUrlOrNull()?.let(::originOf) == apiOrigin
            if (!sameOrigin) return@Interceptor chain.proceed(original)

            val token = runBlocking { freshAccessToken(baseUrl) }
            var response = chain.proceed(original.withBearer(token))
            if (token.isNotEmpty() && response.isLoginRequired()) {
                val renewed = runBlocking { refresh(baseUrl, staleToken = token) }
                if (renewed != null) {
                    response.close()
                    response = chain.proceed(original.withBearer(renewed))
                }
            }
            response
        }

        val client = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(60, TimeUnit.SECONDS)
            .addInterceptor(sessionInterceptor)
            .followRedirects(followRedirects)
            .build()

        return Retrofit.Builder()
            .baseUrl(baseUrl)
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ApiService::class.java)
    }

    /** Current access token, refreshed first if it expires within [REFRESH_MARGIN_MS]. */
    private suspend fun freshAccessToken(baseUrl: String): String {
        val token = session.accessTokenNow()
        if (token.isEmpty()) return token
        val expiresAt = session.accessExpiresAtNow()
        if (expiresAt > 0 && expiresAt - System.currentTimeMillis() < REFRESH_MARGIN_MS) {
            return refresh(baseUrl, staleToken = token) ?: token
        }
        return token
    }

    /**
     * Exchanges the refresh token for a new pair. Single-flight: concurrent
     * callers that queued behind a successful refresh reuse its result instead
     * of spending the (now consumed) refresh token again. Returns null when the
     * session cannot be renewed; callers then surface the login-required error.
     */
    private suspend fun refresh(baseUrl: String, staleToken: String): String? = refreshMutex.withLock {
        // Fylvo: never hand out or spend tokens for a server that is no longer the signed-in one.
        if (!isSignedInOrigin(baseUrl)) return@withLock null
        val current = session.accessTokenNow()
        if (current.isNotEmpty() && current != staleToken) return@withLock current
        val refreshToken = session.refreshTokenNow()
        if (refreshToken.isEmpty()) return@withLock null

        val resp = runCatching {
            bareService(baseUrl).refreshToken(RefreshTokenRequest(refreshToken))
        }.getOrNull()
        val body = resp?.body()
        if (resp?.isSuccessful != true || body == null || body.code != 0 || body.data == null) {
            return@withLock null
        }
        val tokens = body.data
        // Signed out or signed in again while the request ran: drop the result.
        if (session.refreshTokenNow() != refreshToken) return@withLock null
        session.saveTokens(tokens.accessToken, tokens.refreshToken, parseExpiresAt(tokens.accessExpires))
        tokens.accessToken
    }

    private suspend fun isSignedInOrigin(baseUrl: String): Boolean {
        val wanted = baseUrl.toHttpUrlOrNull()?.let(::originOf) ?: return false
        return (session.serverUrlNow() + "/").toHttpUrlOrNull()?.let(::originOf) == wanted
    }

    /** Retrofit without the session interceptor, for the refresh call itself. */
    private fun bareService(baseUrl: String): ApiService =
        Retrofit.Builder()
            .baseUrl(baseUrl)
            .client(OkHttpClient.Builder().connectTimeout(15, TimeUnit.SECONDS).followRedirects(false).build())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ApiService::class.java)

    private fun Request.withBearer(token: String): Request =
        if (token.isEmpty()) this else newBuilder().header("Authorization", "Bearer $token").build()

    /** True for a JSON envelope carrying Cloudreve's "login required" code. */
    private fun Response.isLoginRequired(): Boolean {
        if (!isSuccessful) return code == 401
        val type = header("Content-Type") ?: return false
        if (!type.startsWith("application/json")) return false
        val head = peekBody(256).string()
        return LOGIN_REQUIRED.containsMatchIn(head)
    }

    companion object {
        /** "https://host:port" with the scheme's default port filled in. */
        fun originOf(url: HttpUrl): String = "${url.scheme}://${url.host.lowercase()}:${url.port}"

        private const val REFRESH_MARGIN_MS = 60_000L
        private val LOGIN_REQUIRED = Regex(""""code"\s*:\s*401\b""")

        // OffsetDateTime parses both "…Z" and "+02:00" offsets; unknown → refresh early.
        fun parseExpiresAt(iso: String): Long = runCatching {
            java.time.OffsetDateTime.parse(iso).toInstant().toEpochMilli()
        }.getOrDefault(0L)
    }
}
