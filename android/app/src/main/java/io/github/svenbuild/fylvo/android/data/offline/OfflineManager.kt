package io.github.svenbuild.fylvo.android.data.offline

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.FavoritesStore
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.work.OfflineSyncWorker
import java.io.File
import java.util.concurrent.ConcurrentHashMap

private val Context.offlineSettingsStore by preferencesDataStore(name = "offline_settings")

/** A root with what is on the phone of it. */
data class OfflineRootInfo(val root: OfflineRoot, val files: Int, val bytes: Long)

/**
 * Which server paths are kept offline: the roots themselves and everything inside a folder root
 * (a folder inside an offline folder is "covered" and cannot be switched separately).
 */
class OfflineCoverage(roots: List<OfflineRoot> = emptyList()) {
    private val items = roots.map { CrUri.canonical(it.uri) to it }

    /** The root that is exactly [path]. */
    fun rootAt(path: String): OfflineRoot? = CrUri.canonical(path).let { c -> items.firstOrNull { it.first == c }?.second }

    /** A folder root that contains [path]. */
    fun coveringFolder(path: String): OfflineRoot? = CrUri.canonical(path).let { c ->
        items.firstOrNull { (rc, root) -> root.folder && relativePath(rc, c) != null }?.second
    }

    fun isOffline(path: String): Boolean = rootAt(path) != null || coveringFolder(path) != null
}

/**
 * Fylvo: offline folders and files of the signed-in account. Holds the roots for the screens,
 * switches roots on and off, finds local copies and serves the offline listing. The sync itself
 * is [OfflineSyncEngine], run by [OfflineSyncWorker].
 */
class OfflineManager(
    private val context: Context,
    private val session: SessionManager,
    private val favorites: FavoritesStore,
) {
    /** One sync engine run at a time in the whole process; a run that finds it held ends at once. */
    val engineLock = Mutex()

    /** Short sections that write copies or an index; switching a root off takes it too. */
    private val writeLock = Mutex()

    /** Roots switched off in this process: the engine never writes for them again. */
    private val removed = ConcurrentHashMap.newKeySet<String>()

    private val _roots = MutableStateFlow<List<OfflineRootInfo>>(emptyList())
    val roots: StateFlow<List<OfflineRootInfo>> = _roots

    /** Id of the root the engine works on right now. */
    private val _syncing = MutableStateFlow<String?>(null)
    val syncing: StateFlow<String?> = _syncing

    private val mobileDataKey = booleanPreferencesKey("mobile_data")

    /** "Offline folders also over mobile data"; off = Wi-Fi (unmetered) only. */
    val mobileData: Flow<Boolean> = context.offlineSettingsStore.data.map { it[mobileDataKey] ?: false }

    suspend fun setMobileData(on: Boolean) {
        context.offlineSettingsStore.edit { it[mobileDataKey] = on }
        if (_roots.value.isNotEmpty()) OfflineSyncWorker.apply(context, on)
    }

    /** The store of the signed-in account, or null when nobody is signed in. */
    suspend fun store(): OfflineStore? {
        if (!session.isLoggedIn.first()) return null
        val server = session.serverUrlNow()
        val email = session.userEmail.first()
        val id = session.userId.first()
        if (server.isEmpty() || (email.isEmpty() && id.isEmpty())) return null
        return OfflineStore(context.filesDir, accountKey(server, id, email))
    }

    /** While signed in: takes over the old offline files, shows the roots, keeps the periodic sync. */
    suspend fun start() = withContext(Dispatchers.IO) {
        val store = store() ?: return@withContext
        migrateLegacy(store)
        reload(store)
        if (_roots.value.isNotEmpty()) OfflineSyncWorker.apply(context, mobileData.first())
    }

    /** Single offline files of app versions before 1.2 become single-file roots (once). */
    suspend fun migrateLegacy(store: OfflineStore) {
        writeLock.withLock {
            val legacy = favorites.listNow()
            if (legacy.isEmpty()) return
            // The new index first, then the old list: a stop in between adds nothing twice.
            migrateFavorites(legacy, store)
            favorites.clear()
        }
    }

    private fun reload(store: OfflineStore) {
        _roots.value = store.indexes().filter { it.root.id !in removed }.map(::info)
    }

    /** The engine wrote [index]; the screens show it. */
    fun publish(index: OfflineIndex) {
        if (index.root.id in removed) return
        val list = _roots.value
        val info = info(index)
        _roots.value = if (list.any { it.root.id == index.root.id }) list.map { if (it.root.id == index.root.id) info else it } else list + info
    }

    private fun info(index: OfflineIndex): OfflineRootInfo {
        val present = index.entries.filter { it.localSize >= 0 }
        return OfflineRootInfo(index.root, present.size, present.sumOf { it.localSize })
    }

    fun setSyncing(id: String?) {
        _syncing.value = id
    }

    fun isRemoved(id: String): Boolean = id in removed

    /** Runs [block] under the write lock unless root [id] was switched off; null then. */
    suspend fun <T> writing(id: String, block: () -> T): T? = writeLock.withLock {
        if (id in removed) null else block()
    }

    /** Switches [uri] on and starts a sync. False when it is already on or nobody is signed in. */
    suspend fun addRoot(uri: String, name: String, folder: Boolean): Boolean = withContext(Dispatchers.IO) {
        val store = store() ?: return@withContext false
        val added = writeLock.withLock {
            val canonical = CrUri.canonical(uri)
            if (store.indexes().any { CrUri.canonical(it.root.uri) == canonical }) return@withLock false
            val root = OfflineRoot(
                id = OfflineStore.newId(), uri = uri, name = name, folder = folder, createdAt = System.currentTimeMillis(),
            )
            store.save(OfflineIndex(root))
            true
        }
        reload(store)
        if (added) {
            val mobile = mobileData.first()
            OfflineSyncWorker.apply(context, mobile)
            OfflineSyncWorker.syncNow(context, mobile)
        }
        added
    }

    /** Switches root [id] off: the engine is locked out of it first, then its copies and index go. */
    suspend fun removeRoot(id: String) = withContext(Dispatchers.IO) {
        val store = store() ?: return@withContext
        writeLock.withLock {
            removed += id
            store.load(id)?.root?.legacyFile?.let(::deleteLegacyCopy)
            store.delete(id)
        }
        reload(store)
        if (_roots.value.isEmpty()) OfflineSyncWorker.cancel(context)
    }

    suspend fun syncNow() {
        OfflineSyncWorker.syncNow(context, mobileData.first())
    }

    /** A copy made before 1.2, directly in filesDir/offline; nothing else is ever deleted here. */
    fun deleteLegacyCopy(path: String) {
        val file = File(path)
        val legacyDir = File(context.filesDir, "offline").canonicalFile
        if (file.canonicalFile.parentFile == legacyDir) file.delete()
    }

    /** The local copy of the server file [path] (also inside an offline folder), or null. */
    suspend fun localCopy(path: String): File? = withContext(Dispatchers.IO) {
        val store = store() ?: return@withContext null
        val c = CrUri.canonical(path)
        for (index in store.indexes()) {
            val root = index.root
            if (root.id in removed) continue
            val rootCanonical = CrUri.canonical(root.uri)
            val rel = when {
                root.folder -> relativePath(rootCanonical, c)
                rootCanonical == c -> index.entries.firstOrNull()?.path
                else -> null
            } ?: continue
            val entry = index.entries.firstOrNull { it.path == rel }
            if (entry != null && entry.localSize >= 0) {
                val file = store.fileOf(root.id, rel)
                if (file.isFile && file.length() == entry.localSize) return@withContext file
            }
            root.legacyFile?.let(::File)?.takeIf { !root.folder && it.isFile }?.let { return@withContext it }
        }
        null
    }

    /**
     * The folders and files directly in [uri] from the index of the offline folder that holds it,
     * for browsing without a connection; null when no offline folder holds [uri].
     */
    suspend fun browse(uri: String): List<FileObject>? = withContext(Dispatchers.IO) {
        val store = store() ?: return@withContext null
        val c = CrUri.canonical(uri)
        for (index in store.indexes()) {
            if (!index.root.folder || index.root.id in removed) continue
            val rootCanonical = CrUri.canonical(index.root.uri)
            val rel = if (c == rootCanonical) "" else relativePath(rootCanonical, c) ?: continue
            return@withContext listOffline(index, rel, uri)
        }
        null
    }

    /** Clears the in-memory view (session ended). */
    fun forget() {
        _roots.value = emptyList()
        _syncing.value = null
    }

    /** Sign-out: every account's offline data goes; nothing on the phone is unsynced in this version. */
    suspend fun deleteAll() = withContext(Dispatchers.IO) {
        writeLock.withLock {
            store()?.indexes()?.forEach { removed += it.root.id }
            OfflineStore.deleteAll(context.filesDir)
        }
        context.offlineSettingsStore.edit { it.clear() }
        forget()
    }

    /** Bytes the app can still use on the phone (the system may free its caches for them). */
    fun freeBytes(): Long {
        val storage = context.getSystemService(android.os.storage.StorageManager::class.java)
        // No answer counts as no room: nothing is fetched then.
        return runCatching { storage.getAllocatableBytes(storage.getUuidForPath(context.filesDir)) }.getOrDefault(0)
    }
}

/** Entries of [index] directly in the folder [rel] ("" = the root), shown under [uri]. */
internal fun listOffline(index: OfflineIndex, rel: String, uri: String): List<FileObject> {
    val prefix = if (rel.isEmpty()) "" else "$rel/"
    fun direct(path: String) = path.startsWith(prefix) && path.length > prefix.length && '/' !in path.substring(prefix.length)
    val folderPaths = (index.folders + index.entries.flatMap { parentsOf(it.path) }).toSet().filter(::direct)
    val folders = folderPaths.map { it.substringAfterLast('/') }.sortedWith(String.CASE_INSENSITIVE_ORDER).map { name ->
        FileObject(type = 1, name = name, path = CrUri.join(uri, name))
    }
    val files = index.entries.filter { direct(it.path) && it.localSize >= 0 }
        .sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.path })
        .map { entry ->
            val name = entry.path.substringAfterLast('/')
            // No thumbnail requests without a connection.
            FileObject(
                type = 0, id = entry.id, name = name, path = CrUri.join(uri, name), size = entry.size,
                metadata = mapOf(FileObject.METADATA_THUMB_DISABLED to ""),
            )
        }
    return folders + files
}
