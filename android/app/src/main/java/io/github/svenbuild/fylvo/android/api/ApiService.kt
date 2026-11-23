package io.github.svenbuild.fylvo.android.api

import kotlinx.serialization.json.JsonElement
import okhttp3.RequestBody
import okhttp3.ResponseBody
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query
import retrofit2.http.Streaming
import retrofit2.http.PATCH
import retrofit2.http.Tag
import kotlinx.serialization.json.JsonObject
import retrofit2.http.Url

interface ApiService {

    // With 2FA enabled the server answers code 203 and `data` holds a 2FA session id
    // (JSON string) instead of a LoginResponse; finish with [verify2FA].
    @POST("api/v4/session/token")
    suspend fun login(@Body request: PasswordLoginRequest): Response<ApiResponse<JsonElement>>

    @POST("api/v4/session/token/2fa")
    suspend fun verify2FA(@Body request: OtpLoginRequest): Response<ApiResponse<JsonElement>>

    /** Fylvo: the new password for an account that must change it at sign-in. */
    @POST("api/v4/session/token/password")
    suspend fun setNewPassword(@Body request: NewPasswordRequest): Response<ApiResponse<JsonElement>>

    @POST("api/v4/session/token/refresh")
    suspend fun refreshToken(@Body request: RefreshTokenRequest): Response<ApiResponse<RefreshTokenResponse>>

    // Retrofit's @DELETE does not accept a @Body; the server reads the refresh token from it.
    @HTTP(method = "DELETE", path = "api/v4/session/token", hasBody = true)
    suspend fun signOut(@Body request: RefreshTokenRequest): Response<ApiResponse<Unit>>

    @GET("api/v4/user/capacity")
    suspend fun capacity(): Response<ApiResponse<CapacityResponse>>

    @GET("api/v4/site/config/login")
    suspend fun siteConfig(): Response<ApiResponse<SiteConfig>>

    /** Fylvo: explorer settings; only the file type icons are read. No sign-in needed. */
    @GET("api/v4/site/config/explorer")
    suspend fun explorerConfig(): Response<ApiResponse<ExplorerConfig>>

    @GET("api/v4/file")
    suspend fun listFiles(
        @Query("uri") uri: String,
        @Query("page_size") pageSize: Int = 50,
        @Query("next_page_token") nextPageToken: String? = null,
        @Query("order_by") orderBy: String? = null,
        @Query("order_direction") orderDirection: String? = null,
    ): Response<ApiResponse<ListFileResponse>>

    /** One file or folder by exact URI; the envelope carries 40016 when the path does not exist. */
    @GET("api/v4/file/info")
    suspend fun fileInfo(@Query("uri") uri: String): Response<ApiResponse<FileObject>>

    @POST("api/v4/file/url")
    suspend fun fileUrls(@Body request: FileUrlRequest): Response<ApiResponse<FileUrlResponse>>

    @POST("api/v4/file/create")
    suspend fun createFile(@Body request: CreateFileRequest): Response<ApiResponse<Unit>>

    @POST("api/v4/file/rename")
    suspend fun renameFile(@Body request: RenameFileRequest): Response<ApiResponse<Unit>>

    @POST("api/v4/file/restore")
    suspend fun restoreFiles(@Body request: RestoreFileRequest): Response<ApiResponse<Unit>>

    @POST("api/v4/file/move")
    suspend fun moveFiles(@Body request: MoveFileRequest): Response<ApiResponse<Unit>>

    @HTTP(method = "DELETE", path = "api/v4/file", hasBody = true)
    suspend fun deleteFiles(@Body request: DeleteFileRequest): Response<ApiResponse<Unit>>

    @PUT("api/v4/file/upload")
    suspend fun createUploadSession(@Body request: CreateUploadSessionRequest): Response<ApiResponse<UploadSessionResponse>>

    /** Cancels an unfinished upload session, so its placeholder file and lock go away. */
    @HTTP(method = "DELETE", path = "api/v4/file/upload", hasBody = true)
    suspend fun deleteUploadSession(@Body request: DeleteUploadSessionRequest): Response<ResponseBody>

    // Chunk targets are fully resolved URLs returned in upload_urls —
    // same-origin for local policies, presigned for remote policies.
    @POST
    suspend fun uploadChunk(@Url url: String, @Body body: RequestBody): Response<ResponseBody>

    @PUT
    suspend fun uploadChunkPut(@Url url: String, @Body body: RequestBody): Response<ResponseBody>

    @POST
    suspend fun completeUpload(@Url url: String): Response<ResponseBody>

    // Streamed: a large file must never be held in memory as a whole.
    @Streaming
    @GET
    suspend fun download(@Url url: String): Response<ResponseBody>

    @GET("api/v4/file/thumb")
    suspend fun thumb(@Query("uri") uri: String): Response<ApiResponse<ThumbResponse>>

    @GET("api/v4/workflow")
    suspend fun listTasks(
        @Query("page_size") pageSize: Int = 20,
        @Query("category") category: String = "general",
    ): Response<ApiResponse<TaskListResponse>>

    @PUT("api/v4/share")
    suspend fun createShare(@Body request: ShareCreateRequest): Response<ApiResponse<String>>

    // ---- Fylvo 1.6: managing own shares (design/android-parity-plan.md, 1 and 2).

    /** Own shares, newest first; [pageSize] 10 to 100, next page with `pagination.next_token`. */
    @GET("api/v4/share")
    suspend fun listShares(
        @Query("page_size") pageSize: Int = 50,
        @Query("order_direction") orderDirection: String = "desc",
        @Query("next_page_token") nextPageToken: String? = null,
    ): Response<ApiResponse<ShareListResponse>>

    /** Replaces all settings of share [id] (the password stays); answers with the link. */
    @POST("api/v4/share/{id}")
    suspend fun editShare(@Path("id") id: String, @Body request: ShareCreateRequest): Response<ApiResponse<String>>

    @HTTP(method = "DELETE", path = "api/v4/share/{id}")
    suspend fun deleteShare(@Path("id") id: String): Response<ApiResponse<Unit>>

    /**
     * One share; [password] unlocks a protected one (also needed by its owner for
     * [ownerExtended], which adds `source_uri`). A wrong password answers `unlocked: false`.
     */
    @GET("api/v4/share/info/{id}")
    suspend fun shareDetails(
        @Path("id") id: String,
        @Query("password") password: String? = null,
        @Query("owner_extended") ownerExtended: Boolean? = null,
    ): Response<ApiResponse<ShareInfo>>

    /** A file with `extended_info` (shares, versions, storage used). */
    @GET("api/v4/file/info")
    suspend fun fileInfoExtended(@Query("uri") uri: String, @Query("extended") extended: Boolean = true): Response<ApiResponse<FileObject>>

    /** Fylvo: the account's connected storages; a server without the feature answers HTTP 404. */
    @GET("api/v4/fylvo/connected")
    suspend fun connectedStorages(): Response<ApiResponse<ConnectedOverview>>

    @GET("api/v4/fylvo/connected/upload/{session}")
    suspend fun connectedUploadState(@Path("session") session: String): Response<ApiResponse<ConnectedUploadState>>

    @GET("api/v4/fylvo/connected/{id}/capacity")
    suspend fun connectedCapacity(@Path("id") id: String): Response<ApiResponse<CapacityResponse>>

    /** Fylvo: the newest Android release on the server; envelope code 404 when none is published. */
    @GET("api/v4/fylvo/android/latest")
    suspend fun androidLatest(): Response<ApiResponse<AndroidRelease>>

    /**
     * Fylvo: the APK of the release with this SHA-256 (lowercase hex), streamed instead of
     * buffered. 404 or a JSON envelope means the release changed meanwhile.
     */
    @Streaming
    @GET("api/v4/fylvo/android/apk/{sha256}")
    suspend fun androidApk(@Path("sha256") sha256: String): Response<ResponseBody>

    // ---- Fylvo: customisation (design/customize-plan.md). Calls carry the session they belong
    // to ([SessionTag]); ApiClient drops them once that session ended.

    @GET("api/v4/fylvo/prefs")
    suspend fun prefs(@Tag session: SessionTag): Response<ApiResponse<FylvoPrefs>>

    @PATCH("api/v4/fylvo/prefs")
    suspend fun patchPrefs(@Body patch: JsonObject, @Tag session: SessionTag): Response<ApiResponse<FylvoPrefs>>

    @HTTP(method = "DELETE", path = "api/v4/fylvo/prefs")
    suspend fun resetPrefs(@Tag session: SessionTag): Response<ApiResponse<FylvoPrefs>>

    @GET("api/v4/fylvo/favorites")
    suspend fun favorites(@Tag session: SessionTag): Response<ApiResponse<FavoritesList>>

    @PUT("api/v4/fylvo/favorites")
    suspend fun addFavorites(@Body request: FavoritesAddRequest, @Tag session: SessionTag): Response<ApiResponse<FavoriteIds>>

    @HTTP(method = "DELETE", path = "api/v4/fylvo/favorites", hasBody = true)
    suspend fun removeFavorites(@Body request: FavoritesRemoveRequest, @Tag session: SessionTag): Response<ApiResponse<FavoriteIds>>

    @GET("api/v4/fylvo/tags")
    suspend fun tags(@Tag session: SessionTag): Response<ApiResponse<List<TagInfo>>>

    @GET("api/v4/site/config/basic")
    suspend fun siteBasic(@Tag session: SessionTag): Response<ApiResponse<SiteBasic>>

    @GET("api/v4/user/setting")
    suspend fun userSettings(@Tag session: SessionTag): Response<ApiResponse<UserSettings>>

    @PATCH("api/v4/file/metadata")
    suspend fun patchMetadata(@Body request: MetadataPatchRequest, @Tag session: SessionTag): Response<ApiResponse<Unit>>

    @PATCH("api/v4/file/view")
    suspend fun patchView(@Body request: ViewPatchRequest, @Tag session: SessionTag): Response<ApiResponse<Unit>>

    /** The listing's whole `view`; without page size and sort, which would replace the stored ones. */
    @GET("api/v4/file")
    suspend fun folderView(@Query("uri") uri: String, @Tag session: SessionTag): Response<ApiResponse<FolderViewResponse>>

    /** Fylvo: the signed-in account with its group rights. */
    @GET("api/v4/user/me")
    suspend fun me(@Tag session: SessionTag): Response<ApiResponse<Me>>

    /** Fylvo: one share's public facts (member-only, owner); decides the star inside shares. */
    @GET("api/v4/share/info/{id}")
    suspend fun shareInfo(@Path("id") id: String, @Tag session: SessionTag): Response<ApiResponse<ShareInfo>>
}
