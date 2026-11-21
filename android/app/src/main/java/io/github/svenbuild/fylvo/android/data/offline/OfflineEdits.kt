package io.github.svenbuild.fylvo.android.data.offline

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import androidx.core.content.FileProvider
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.localized
import io.github.svenbuild.fylvo.android.work.OfflineEditWorker
import io.github.svenbuild.fylvo.android.work.OfflineSyncWorker
import java.io.File
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Fylvo: editing files of offline roots on the phone ("Bearbeiten"). Makes working copies, opens
 * them instead of the synced copy, finishes or discards them, and runs the upload pass
 * ([EditPass]) for the sync engine, the edit check ([OfflineEditWorker]) and explicit actions.
 * Everything that writes the edit index or uploads holds [OfflineManager.editLock].
 */
class OfflineEdits(
    private val context: Context,
    private val manager: OfflineManager,
    private val repo: FileRepository,
) {
    private val lock get() = manager.editLock

    /** Closed during a sign-out: no new pass starts (an explicit one of the sign-out still runs). */
    private val gate = AtomicBoolean(false)

    private val _entries = MutableStateFlow<List<EditEntry>>(emptyList())
    /** Every entry of the signed-in account (also finished ones), for the screens. */
    val entries: StateFlow<List<EditEntry>> = _entries

    sealed class StartResult {
        data class Ready(val file: File, val entryId: String) : StartResult()
        /** The synced copy is not the current version (yet): try again after the sync. */
        data object NotReady : StartResult()
    }

    enum class NotUploaded { Offline, Pending, Missing, Failed }

    sealed class FinishResult {
        data object Done : FinishResult()
        data class Failed(val reason: NotUploaded) : FinishResult()
    }

    enum class SignOutChoice { None, Upload, Discard }

    sealed class SignOutResult {
        data object Done : SignOutResult()
        /** Working copies with changes not uploaded: ask first. */
        data class NeedsChoice(val count: Int) : SignOutResult()
        data class Failed(val count: Int, val reason: NotUploaded) : SignOutResult()
    }

    private suspend fun stores(): Pair<OfflineStore, EditStore>? =
        manager.store()?.let { it to EditStore(context.filesDir, it.accountKey) }

    private fun publish(index: EditIndex) {
        _entries.value = index.entries
    }

    /** App start or return to the app: shows the entries, and checks soon when something waits. */
    suspend fun start() = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext
        val entries = store.load().entries
        _entries.value = entries
        if (entries.any { needsCheck(it, statOf(store.fileOf(it))) }) scheduleCheck(offline.accountKey)
    }

    /** The session ended: nothing of it is shown. */
    fun forget() {
        _entries.value = emptyList()
    }

    /** A pass of the sync engine ([EditPhase.Start], [EditPhase.End]) or of the edit check. */
    suspend fun pass(offline: OfflineStore, phase: EditPhase) {
        if (gate.get()) return
        val store = EditStore(context.filesDir, offline.accountKey)
        val result = lock.withLock {
            if (gate.get()) return
            runPass(offline, store, phase)
        }
        // The start pass leaves what waits to the end pass of the same run.
        if (phase == EditPhase.Start) return
        if (result.syncWanted && phase == EditPhase.Check) manager.syncNow()
        if (result.again) scheduleCheck(offline.accountKey, result.delay)
    }

    /** The edit check of [accountKey], only while that account is signed in. */
    suspend fun check(accountKey: String) {
        val offline = manager.store()?.takeIf { it.accountKey == accountKey } ?: return
        pass(offline, EditPhase.Check)
    }

    /** Runs a pass; the caller holds the lock. */
    private suspend fun runPass(offline: OfflineStore, store: EditStore, phase: EditPhase, only: Set<String>? = null): EditPass.Result =
        withContext(Dispatchers.IO) {
            EditPass(
                repo, manager, store, offline, phase,
                conflictLabel = context.localized().getString(R.string.edit_conflict_label),
                stillSignedIn = { manager.store()?.accountKey == offline.accountKey },
                publish = ::publish,
            ).run(only)
        }

    /** An edit check in [delayMs] (one waiting check covers every request). */
    suspend fun scheduleCheck(accountKey: String, delayMs: Long = EDIT_RECOVERY_DELAY_MS) {
        val mobile = manager.mobileData.first()
        withContext(Dispatchers.IO) { OfflineEditWorker.schedule(context, accountKey, mobile, delayMs) }
    }

    /** "Später erneut versuchen": a check of the signed-in account soon. */
    suspend fun checkSoon() {
        manager.store()?.let { scheduleCheck(it.accountKey) }
    }

    /** The working copy in use for the server file [fileId] (not an outdated one), or null. */
    suspend fun workingCopy(fileId: String): File? = withContext(Dispatchers.IO) {
        val (_, store) = stores() ?: return@withContext null
        store.load().entries.firstOrNull { it.active && !it.outdated && it.fileId == fileId }
            ?.let { store.fileOf(it) }?.takeIf { it.isFile }
    }

    /**
     * The file to read for the server file [path]: its working copy when there is one (whatever
     * version is asked for), else the synced copy ([OfflineManager.localCopy]). The working copy
     * is found by address or by the file id the synced index gives; with [online] also by the
     * server's file id (a working copy that outlived its root).
     */
    suspend fun localCopy(path: String, entity: String? = null, online: Boolean = false): File? = withContext(Dispatchers.IO) {
        val store = stores()?.second
        val entries = store?.load()?.entries.orEmpty().filter { it.active && !it.outdated }
        if (store != null && entries.isNotEmpty()) {
            val c = CrUri.canonical(path)
            val hit = entries.firstOrNull { CrUri.canonical(it.uri) == c } ?: run {
                val id = manager.syncedFileIdAt(path) ?: if (online) lookupId(path) else null
                entries.firstOrNull { id != null && it.fileId == id }
            }
            hit?.let { store.fileOf(it) }?.takeIf { it.isFile }?.let { return@withContext it }
        }
        manager.localCopy(path, entity)
    }

    private suspend fun lookupId(path: String): String? = try {
        repo.lookup(path)?.id
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }

    /**
     * "Bearbeiten": the working copy of [file], made from its synced copy when there is none yet
     * (an existing one is reopened, never copied over). [offlineMode]: the listing comes from the
     * phone, so it cannot be compared with the server's version.
     */
    suspend fun startEditing(file: FileObject, offlineMode: Boolean): StartResult = withContext(Dispatchers.IO) {
        val (_, store) = stores() ?: return@withContext StartResult.NotReady
        lock.withLock {
            store.load().entries.firstOrNull { it.active && it.fileId == file.id }?.let { existing ->
                val local = store.fileOf(existing)
                if (!existing.outdated && local.isFile) return@withLock StartResult.Ready(local, existing.id)
                // Outdated (no unsaved change) or gone: it is finished, a fresh copy follows.
                retireLocked(store, existing)
            }
            // The entry first: a stop while copying leaves an entry (deleted at the next pass), no orphan.
            val entry = EditEntry(
                id = EditStore.newId(), fileId = file.id, uri = file.path, name = EditStore.safeName(file.name),
                base = EditSnapshot(-1, 0, ""), createdAt = System.currentTimeMillis(),
            )
            publish(store.put(entry))
            val made = try {
                makeCopy(store, entry, file, offlineMode)
            } catch (e: CancellationException) {
                withContext(NonCancellable) { dropCreating(store, entry) }
                throw e
            } catch (_: Exception) {
                null
            }
            if (made == null) {
                dropCreating(store, entry)
                return@withLock StartResult.NotReady
            }
            publish(store.put(made))
            StartResult.Ready(store.fileOf(made), made.id)
        }
    }

    private fun dropCreating(store: EditStore, entry: EditEntry) {
        store.deleteFiles(entry)
        publish(store.remove(entry.id))
    }

    /** Copies the synced copy (only when it is current) into place; null when it is not. */
    private suspend fun makeCopy(store: EditStore, entry: EditEntry, file: FileObject, offlineMode: Boolean): EditEntry? {
        val opened = manager.openSyncedCopy(file.id) ?: return null
        opened.stream.use { input ->
            // The listing shows a newer version than the phone holds.
            if (!offlineMode && file.primaryEntity.isNotEmpty() && opened.entity != file.primaryEntity) return null
            val tmp = store.creatingOf(entry)
            store.tmpFolder()
            val hash = tmp.outputStream().use { EditStore.copyHashing(input, it) }
            val dest = store.activeFileOf(entry)
            dest.parentFile?.mkdirs()
            if (!tmp.renameTo(dest)) return null
            return entry.copy(
                state = EditState.Editing, rootId = opened.rootId, baseEntity = opened.entity,
                base = EditSnapshot(dest.length(), dest.lastModified(), hash),
            )
        }
    }

    /** An editor got write access to [entryId]: noted, and its changes are checked in a few minutes. */
    suspend fun opened(entryId: String) = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext
        lock.withLock {
            store.entry(entryId)?.let { publish(store.put(it.copy(grantedAt = System.currentTimeMillis()))) }
        }
        scheduleCheck(offline.accountKey)
    }

    /**
     * "Bearbeitung abschliessen": the editor loses access, changes not yet uploaded go up now
     * (without waiting for the file to be quiet), and the copy is finished only when the server
     * holds exactly its bytes.
     */
    suspend fun finish(entryId: String): FinishResult = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext FinishResult.Done
        lock.withLock {
            val entry = store.entry(entryId) ?: return@withLock FinishResult.Done
            revoke(store, entry)
            val failed = uploadLocked(offline, store, listOf(entry))
            if (failed != null) return@withLock FinishResult.Failed(failed)
            store.entry(entryId)?.let { retireLocked(store, it) }
            FinishResult.Done
        }
    }

    /**
     * Uploads [entries] in an explicit pass (the caller holds the lock and revoked the grants).
     * Null when every one is settled afterwards, else why not.
     */
    private suspend fun uploadLocked(offline: OfflineStore, store: EditStore, entries: List<EditEntry>): NotUploaded? {
        fun open() = entries.mapNotNull { store.entry(it.id) }.filter { !isSettled(it, hashOf(store, it)) }
        val waiting = open()
        if (waiting.isEmpty()) return null
        if (waiting.any { !store.fileOf(it).isFile }) return NotUploaded.Missing
        if (!networkAllows()) return NotUploaded.Offline
        runPass(offline, store, EditPhase.Explicit, waiting.mapTo(HashSet()) { it.id })
        val left = open()
        return when {
            left.isEmpty() -> null
            left.any { it.pending != null } -> NotUploaded.Pending
            else -> NotUploaded.Failed
        }
    }

    /** "Änderungen verwerfen": the working copy goes at once (with its unfinished upload, if any). */
    suspend fun discard(entryId: String) = withContext(Dispatchers.IO) {
        val (_, store) = stores() ?: return@withContext
        lock.withLock { store.entry(entryId)?.let { discardLocked(store, it) } }
    }

    private suspend fun discardLocked(store: EditStore, entry: EditEntry) {
        revoke(store, entry)
        entry.pending?.takeIf { it.sessionId.isNotEmpty() }?.let { m ->
            // Best effort; a placeholder left behind goes away on the server by itself.
            withTimeoutOrNull(10_000) { runCatching { repo.deleteUploadSession(m.sessionId, m.sessionUri) } }
        }
        store.deleteFiles(entry)
        publish(store.remove(entry.id))
    }

    /** The note about a conflict copy was read. */
    suspend fun dismissNotice(entryId: String) = withContext(Dispatchers.IO) {
        val (_, store) = stores() ?: return@withContext
        lock.withLock { store.entry(entryId)?.let { publish(store.put(it.copy(conflict = ""))) } }
    }

    /** Working copies made in root [rootId] (or of its files) with changes the server does not have. */
    suspend fun rootBlockers(rootId: String): List<EditEntry> = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext emptyList()
        val ids = offline.load(rootId)?.entries.orEmpty().mapTo(HashSet()) { it.id }
        blockers(store).filter { it.rootId == rootId || it.fileId in ids }
    }

    /** Explicit upload of [entries] (switching a root off); true when the server holds every change. */
    suspend fun upload(entries: List<EditEntry>): Boolean = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext false
        lock.withLock {
            entries.forEach { revoke(store, it) }
            uploadLocked(offline, store, entries) == null
        }
    }

    suspend fun discardAll(entries: List<EditEntry>) = withContext(Dispatchers.IO) {
        val (_, store) = stores() ?: return@withContext
        lock.withLock { entries.forEach { e -> store.entry(e.id)?.let { discardLocked(store, it) } } }
    }

    /** How many working copies hold changes the server does not have (asked before a sign-out). */
    suspend fun signOutBlockers(): Int = withContext(Dispatchers.IO) { stores()?.second?.let { blockers(it).size } ?: 0 }

    private fun blockers(store: EditStore): List<EditEntry> =
        signOutBlockers(store.load().entries.map { it to hashOf(store, it) })

    /**
     * Fylvo: sign-out with working copies. With the lock held (a running pass ends first) the
     * choice is applied: [SignOutChoice.Upload] uploads every change and goes on only when all
     * are on the server; [SignOutChoice.Discard] deletes them. Then the workers stop, [end] ends
     * the session (revokes the token), and the remaining copies are finished; they stay for the
     * same account (done/, 7 days).
     */
    suspend fun signOut(choice: SignOutChoice, end: suspend () -> Unit): SignOutResult {
        val stores = stores()
        gate.set(true)
        try {
            return lock.withLock {
                if (stores != null) {
                    val (offline, store) = stores
                    val open = blockers(store)
                    when (choice) {
                        SignOutChoice.None -> if (open.isNotEmpty()) return@withLock SignOutResult.NeedsChoice(open.size)
                        SignOutChoice.Discard -> open.forEach { discardLocked(store, it) }
                        SignOutChoice.Upload -> {
                            open.forEach { revoke(store, it) }
                            uploadLocked(offline, store, open)?.let { reason ->
                                return@withLock SignOutResult.Failed(blockers(store).size, reason)
                            }
                        }
                    }
                }
                withContext(NonCancellable) {
                    withContext(Dispatchers.IO) {
                        OfflineSyncWorker.cancel(context)
                        OfflineEditWorker.cancel(context)
                    }
                    end()
                    stores?.second?.let { store -> store.load().entries.filter { it.active }.forEach { retireLocked(store, it) } }
                    forget()
                }
                SignOutResult.Done
            }
        } finally {
            gate.set(false)
        }
    }

    /** Moves the copy to done/ (kept 7 days, watched for late writes) after revoking the grant. */
    private fun retireLocked(store: EditStore, entry: EditEntry) {
        revoke(store, entry)
        if (store.moveToDone(entry) || !store.activeFileOf(entry).exists()) {
            publish(store.put(entry.copy(retiredAt = System.currentTimeMillis())))
        }
    }

    /** Other apps lose their access to the working copy (read and write). */
    private fun revoke(store: EditStore, entry: EditEntry) {
        runCatching {
            context.revokeUriPermission(
                uriOf(context, store.activeFileOf(entry)),
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
            )
        }
    }

    private fun hashOf(store: EditStore, entry: EditEntry): String? =
        store.fileOf(entry).takeIf { it.isFile }?.let(EditStore::sha256)

    /** A connection the offline setting allows (Wi-Fi only unless mobile data is on). */
    private suspend fun networkAllows(): Boolean {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return false
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        if (!caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) return false
        return manager.mobileData.first() || caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
    }

    private fun statOf(file: File): LocalStat? = file.takeIf { it.isFile }?.let { LocalStat(it.length(), it.lastModified()) }

    companion object {
        /** The address other apps get for a working copy (see FylvoFileProvider). */
        fun uriOf(context: Context, file: File): Uri =
            FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
    }
}
