package io.github.svenbuild.fylvo.android

import android.app.Application
import androidx.work.WorkManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.api.RefreshTokenRequest
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.data.FavoritesStore
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.TaskNotifySettings
import io.github.svenbuild.fylvo.android.data.SyncFolderSettings
import io.github.svenbuild.fylvo.android.data.ViewSettings

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
    lateinit var syncFolderSettings: SyncFolderSettings
        private set
    lateinit var viewSettings: ViewSettings
        private set

    override fun onCreate() {
        super.onCreate()
        sessionManager = SessionManager(this)
        apiClient = ApiClient(sessionManager)
        fileRepository = FileRepository(apiClient)
        favoritesStore = FavoritesStore(this)
        taskNotifySettings = TaskNotifySettings(this)
        syncFolderSettings = SyncFolderSettings(this)
        viewSettings = ViewSettings(this)
    }

    /**
     * Fylvo: full sign-out. Revokes the refresh token on the server, stops every background
     * job, and removes what the account left on the device: tokens, offline copies, cached
     * downloads, upload staging, folder-sync and notification settings.
     */
    suspend fun signOut() {
        runCatching {
            apiClient.service().signOut(RefreshTokenRequest(sessionManager.refreshTokenNow()))
        }
        WorkManager.getInstance(this).cancelAllWork()
        WorkManager.getInstance(this).pruneWork()
        sessionManager.clear()
        favoritesStore.clear()
        syncFolderSettings.clear()
        taskNotifySettings.clear()
        contentResolver.persistedUriPermissions.forEach {
            runCatching {
                contentResolver.releasePersistableUriPermission(it.uri, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
        }
        withContext(Dispatchers.IO) {
            filesDir.resolve("offline").deleteRecursively()
            cacheDir.listFiles()?.forEach { it.deleteRecursively() }
        }
    }
}
