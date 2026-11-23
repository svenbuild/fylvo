package io.github.svenbuild.fylvo.android

import android.app.Application
import android.content.Intent
import android.net.Uri
import androidx.core.net.toUri
import androidx.work.WorkManager
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.imageLoader
import okhttp3.OkHttpClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.flow.first
import io.github.svenbuild.fylvo.android.api.RefreshTokenRequest
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.data.AppUpdater
import io.github.svenbuild.fylvo.android.data.FavoritesStore
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.TaskNotifySettings
import io.github.svenbuild.fylvo.android.data.SyncFolderSettings
import io.github.svenbuild.fylvo.android.data.ViewSettings
import io.github.svenbuild.fylvo.android.data.offline.OfflineEdits
import io.github.svenbuild.fylvo.android.data.offline.OfflineManager
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.localized

class FylvoApp : Application(), ImageLoaderFactory {

    lateinit var sessionManager: SessionManager
        private set
    lateinit var apiClient: ApiClient
        private set
    lateinit var fileRepository: FileRepository
        private set
    lateinit var favoritesStore: FavoritesStore
        private set
    lateinit var taskNotifySettings: TaskNotifySettings
        private set
    lateinit var syncFolderSettings: SyncFolderSettings
        private set
    lateinit var viewSettings: ViewSettings
        private set
    lateinit var appUpdater: AppUpdater
        private set
    lateinit var offlineManager: OfflineManager
        private set
    lateinit var offlineEdits: OfflineEdits
        private set
    /** Fylvo: the account's customisation (prefs, favourites, tags, accents). */
    lateinit var accountPrefs: io.github.svenbuild.fylvo.android.data.prefs.AccountPrefsRepository
        private set
    /** Fylvo: the account's group rights and the server's explorer switches (memory only). */
    lateinit var account: io.github.svenbuild.fylvo.android.data.AccountRepository
        private set
    /** Fylvo 1.6: connecting storages from the app. */
    lateinit var connectRepo: io.github.svenbuild.fylvo.android.data.ConnectRepository
        private set
    lateinit var connect: io.github.svenbuild.fylvo.android.ui.files.ConnectController
        private set
    /** Fylvo 1.6: the storages list to read again after a connection (set by the files screen). */
    @Volatile var reloadConnected: (suspend () -> Unit)? = null
    /** Fylvo 1.6: archives and tasks. */
    lateinit var archives: io.github.svenbuild.fylvo.android.data.ArchiveRepository
        private set
    /** Fylvo 1.6: profile and security of the account. */
    lateinit var security: io.github.svenbuild.fylvo.android.data.SecurityRepository
        private set
    /** Fylvo 1.6: versions, details, history and the trash. */
    lateinit var history: io.github.svenbuild.fylvo.android.data.HistoryRepository
        private set
    /** Fylvo 1.6: text files edited in the app. */
    lateinit var texts: io.github.svenbuild.fylvo.android.data.TextRepository
        private set
    /** Fylvo 1.6: the account's own shares. */
    lateinit var shares: io.github.svenbuild.fylvo.android.data.ShareRepository
        private set

    override fun onCreate() {
        super.onCreate()
        CrUri.useLabelsFrom(localized())
        sessionManager = SessionManager(this)
        apiClient = ApiClient(sessionManager)
        fileRepository = FileRepository(apiClient)
        account = io.github.svenbuild.fylvo.android.data.AccountRepository(apiClient, sessionManager)
        shares = io.github.svenbuild.fylvo.android.data.ShareRepository(apiClient, sessionManager)
        texts = io.github.svenbuild.fylvo.android.data.TextRepository(apiClient, fileRepository, cacheDir, sessionManager)
        history = io.github.svenbuild.fylvo.android.data.HistoryRepository(apiClient, sessionManager)
        archives = io.github.svenbuild.fylvo.android.data.ArchiveRepository(apiClient, sessionManager)
        connectRepo = io.github.svenbuild.fylvo.android.data.ConnectRepository(apiClient, sessionManager)
        connect = io.github.svenbuild.fylvo.android.ui.files.ConnectController(
            apiClient, connectRepo, io.github.svenbuild.fylvo.android.data.ConnectAttemptStore(this),
            reload = { reloadConnected?.invoke() },
        )
        security = io.github.svenbuild.fylvo.android.data.SecurityRepository(apiClient, account, sessionManager)
        favoritesStore = FavoritesStore(this)
        taskNotifySettings = TaskNotifySettings(this)
        syncFolderSettings = SyncFolderSettings(this)
        viewSettings = ViewSettings(this)
        appUpdater = AppUpdater(this, apiClient)
        offlineManager = OfflineManager(this, sessionManager, favoritesStore)
        offlineEdits = OfflineEdits(this, offlineManager, fileRepository)
        accountPrefs = io.github.svenbuild.fylvo.android.data.prefs.AccountPrefsRepository(
            this, apiClient, sessionManager, onSessionExpired = { endSession(expired = true) },
        )
        // Upload lists no job will finish any more; off the main thread.
        Thread {
            runCatching { io.github.svenbuild.fylvo.android.work.UploadWorker.cleanUpOldBatches(this) }
            // Fylvo 1.6: read rights on uploaded phone folders that no batch needs any more.
            runCatching { io.github.svenbuild.fylvo.android.work.FolderUpload.releaseFinished(this) }
        }.start()
    }

    /** Fylvo 1.6: pictures of accounts; follows no redirect (never to a third-party service). */
    val avatarLoader: ImageLoader by lazy {
        ImageLoader.Builder(this)
            .okHttpClient { OkHttpClient.Builder().followRedirects(false).addInterceptor(ApiClient.userAgent).build() }
            .build()
    }

    /** Fylvo: previews and the image viewer name the app to the server like every other request. */
    override fun newImageLoader(): ImageLoader = ImageLoader.Builder(this)
        .okHttpClient {
            OkHttpClient.Builder().addInterceptor(ApiClient.userAgent)
                // Fylvo 1.6: pictures of connected storages, two at a time, kept by version.
                .addInterceptor(io.github.svenbuild.fylvo.android.data.ConnectedThumbs.limiter)
                .addNetworkInterceptor(io.github.svenbuild.fylvo.android.data.ConnectedThumbs.keepLonger)
                .build()
        }
        .build()

    /**
     * Fylvo: sign-out, with [choice] for working copies whose changes are not on the server yet
     * (see OfflineEdits.signOut). The result says whether it happened or what to ask first.
     */
    suspend fun signOut(
        choice: OfflineEdits.SignOutChoice = OfflineEdits.SignOutChoice.None,
        shown: Set<String> = emptySet(),
    ): OfflineEdits.SignOutResult = offlineEdits.signOut(choice, shown) { endSession() }

    /**
     * Fylvo: ends the session and removes what the account left on the device: background
     * jobs, offline copies, cached downloads and app updates, upload staging, folder-sync and notification
     * settings, URI grants. On sign-out the refresh token is revoked on the server; when the
     * server already ended the session ([expired]) the server address and email stay for the
     * login form. The session store is cleared last: that switches the app to the login
     * screen and restarts it (see MainActivity).
     */
    suspend fun endSession(expired: Boolean = false) {
        // Runs to the end even when the caller's screen or search is cancelled meanwhile.
        withContext(NonCancellable) { endLock.withLock {
            // Fylvo: two expiries at the same time end the session once (the second finds it gone).
            if (!sessionManager.isLoggedIn.first()) return@withLock
            // Open uploads first, while the sign-in still works and its token can be renewed
            // (their sessions on the server are cancelled).
            if (!expired) {
                try {
                    withContext(Dispatchers.IO) {
                        kotlinx.coroutines.withTimeoutOrNull(15_000) {
                            io.github.svenbuild.fylvo.android.work.UploadWorker.cancelAllForSignOut(this@FylvoApp)
                        }
                    }
                } catch (e: Throwable) {
                    // The sign-out goes on.
                }
            }
            // Fylvo: then no request of this session runs any more (nor its answer lands).
            sessionManager.endGeneration()
            /** Fylvo: one cleanup step; a failure (full storage, broken file) never stops the others. */
            suspend fun step(block: suspend () -> Unit) {
                try {
                    block()
                } catch (e: Throwable) {
                    // Nothing to do: the next step and the removal of the tokens still run.
                }
            }
            // Fylvo: whatever fails on the way, the session's tokens are removed at the end.
            try {
            if (!expired) {
                step { apiClient.authService().signOut(RefreshTokenRequest(sessionManager.refreshTokenNow())) }
            }
            step {
                withContext(Dispatchers.IO) {
                    WorkManager.getInstance(this@FylvoApp).cancelAllWork().result.get()
                    WorkManager.getInstance(this@FylvoApp).pruneWork()
                }
            }
            // Fylvo: what names the account's files on this device goes first.
            step { accountPrefs.clear() }
            step { account.clear() }
            step { connect.clear() }
            step { io.github.svenbuild.fylvo.android.widget.FolderShortcuts.disableAll(this@FylvoApp) }
            step { io.github.svenbuild.fylvo.android.data.prefs.ShareTargetStore(this@FylvoApp).clear() }
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.pending.value = null
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.share.value = null
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.shareTarget.value = null
            io.github.svenbuild.fylvo.android.data.prefs.IncomingQueue.session = null
            step { io.github.svenbuild.fylvo.android.util.CrUri.forgetShareNames() }
            step { io.github.svenbuild.fylvo.android.util.Connected.forgetNames() }
            step { fileRepository.forgetThumbs() }
            step { io.github.svenbuild.fylvo.android.data.ConnectedThumbs.clear(this@FylvoApp) }
            // Fylvo: previews and images the account loaded (Coil keeps previews by their stored id, thumbCacheKey).
            step {
                imageLoader.memoryCache?.clear()
                withContext(Dispatchers.IO) { imageLoader.diskCache?.clear() }
            }
            step { appUpdater.reset() }
            step { favoritesStore.clear() }
            // Synced copies are read-only, the server has them. After an expired session they stay
            // for the same account (bound to it, see OfflineStore). Working copies are never deleted
            // here: a sign-out handles them first (signOut), after an expired session they stay
            // hidden until the same account signs in again.
            step { if (expired) offlineManager.forget() else offlineManager.deleteAll() }
            step { offlineEdits.forget() }
            step { syncFolderSettings.clear() }
            step { taskNotifySettings.clear() }
            step {
                contentResolver.persistedUriPermissions.forEach {
                    val flags = (if (it.isReadPermission) Intent.FLAG_GRANT_READ_URI_PERMISSION else 0) or
                        (if (it.isWritePermission) Intent.FLAG_GRANT_WRITE_URI_PERMISSION else 0)
                    runCatching { contentResolver.releasePersistableUriPermission(it.uri, flags) }
                }
            }
            // Grants other apps got to our documents (system Files app) end with the session.
            step {
                revokeUriPermission(
                    Uri.parse("content://$packageName.documents/"),
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
                )
            }
            // Also the grants to opened files, working copies included (they stay after an expired session).
            step {
                revokeUriPermission(
                    "content://$packageName.fileprovider/".toUri(),
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
                )
            }
            step {
                withContext(Dispatchers.IO) {
                    // Copies of app versions before 1.2 (directly in offline/); offline/files is handled above.
                    filesDir.resolve("offline").listFiles()?.forEach { if (it.name != "files") it.deleteRecursively() }
                    // Lists of uploads that were still waiting (their jobs are cancelled above).
                    filesDir.resolve("upload_batches").deleteRecursively()
                    // Also removes cacheDir/updates, the downloaded app update.
                    cacheDir.listFiles()?.forEach { it.deleteRecursively() }
                }
            }
            } finally {
                if (expired) sessionManager.clearSession() else sessionManager.clear()
            }
        } }
    }

    /** Fylvo: one session end at a time. */
    private val endLock = kotlinx.coroutines.sync.Mutex()
}
