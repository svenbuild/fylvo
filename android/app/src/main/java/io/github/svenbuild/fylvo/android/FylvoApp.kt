package io.github.svenbuild.fylvo.android

import android.app.Application
import android.content.Intent
import android.net.Uri
import androidx.work.WorkManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.api.RefreshTokenRequest
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.data.AppUpdater
import io.github.svenbuild.fylvo.android.data.FavoritesStore
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.TaskNotifySettings
import io.github.svenbuild.fylvo.android.data.SyncFolderSettings
import io.github.svenbuild.fylvo.android.data.ViewSettings
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.localized

class FylvoApp : Application() {

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

    override fun onCreate() {
        super.onCreate()
        CrUri.useLabelsFrom(localized())
        sessionManager = SessionManager(this)
        apiClient = ApiClient(sessionManager)
        fileRepository = FileRepository(apiClient)
        favoritesStore = FavoritesStore(this)
        taskNotifySettings = TaskNotifySettings(this)
        syncFolderSettings = SyncFolderSettings(this)
        viewSettings = ViewSettings(this)
        appUpdater = AppUpdater(this, apiClient)
        // Upload lists no job will finish any more; off the main thread.
        Thread { runCatching { io.github.svenbuild.fylvo.android.work.UploadWorker.cleanUpOldBatches(this) } }.start()
    }

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
        withContext(NonCancellable) {
            // Open uploads first, while the sign-in still works (their sessions are cancelled).
            if (!expired) {
                withContext(Dispatchers.IO) {
                    kotlinx.coroutines.withTimeoutOrNull(15_000) {
                        runCatching { io.github.svenbuild.fylvo.android.work.UploadWorker.cancelAllForSignOut(this@FylvoApp) }
                    }
                }
            }
            if (!expired) {
                runCatching {
                    apiClient.authService().signOut(RefreshTokenRequest(sessionManager.refreshTokenNow()))
                }
            }
            withContext(Dispatchers.IO) {
                WorkManager.getInstance(this@FylvoApp).cancelAllWork().result.get()
                WorkManager.getInstance(this@FylvoApp).pruneWork()
            }
            io.github.svenbuild.fylvo.android.util.CrUri.forgetShareNames()
            appUpdater.reset()
            favoritesStore.clear()
            syncFolderSettings.clear()
            taskNotifySettings.clear()
            contentResolver.persistedUriPermissions.forEach {
                val flags = (if (it.isReadPermission) Intent.FLAG_GRANT_READ_URI_PERMISSION else 0) or
                    (if (it.isWritePermission) Intent.FLAG_GRANT_WRITE_URI_PERMISSION else 0)
                runCatching { contentResolver.releasePersistableUriPermission(it.uri, flags) }
            }
            // Grants other apps got to our documents (system Files app) end with the session.
            revokeUriPermission(
                Uri.parse("content://$packageName.documents/"),
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
            )
            withContext(Dispatchers.IO) {
                filesDir.resolve("offline").deleteRecursively()
                // Lists of uploads that were still waiting (their jobs are cancelled above).
                filesDir.resolve("upload_batches").deleteRecursively()
                // Also removes cacheDir/updates, the downloaded app update.
                cacheDir.listFiles()?.forEach { it.deleteRecursively() }
            }
            if (expired) sessionManager.clearSession() else sessionManager.clear()
        }
    }
}
