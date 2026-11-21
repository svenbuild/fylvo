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

    enum class NotUploaded { Offline, Pending, Missing, Refused, Failed }

    sealed class FinishResult {
        data object Done : FinishResult()
        data class Failed(val reason: NotUploaded) : FinishResult()
    }

    enum class SignOutChoice { None, Upload, Discard }

    sealed class SignOutResult {
        data object Done : SignOutResult()
        /** Working copies ([ids]) with changes not uploaded: ask first. */
        data class NeedsChoice(val ids: Set<String>) : SignOutResult()
        data class Failed(val ids: Set<String>, val reason: NotUploaded, val code: Int = 0) : SignOutResult()
    }

    private suspend fun stores(): Pair<OfflineStore, EditStore>? =
        manager.store()?.let { it to EditStore(context.filesDir, it.accountKey) }

    private fun publish(index: EditIndex) {
        _entries.value = index.entries
    }

    /** App start or return to the app: shows the entries, and checks soon when something waits. */
    suspend fun start() = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext
        val entries = guarded(null) { store.load().entries }
        // An index that cannot be read is left alone (never written over); the periodic run tries again.
        if (entries == null) return@withContext
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
        // A local failure (an unreadable index, a full disk) never stops the sync run.
        val result = guarded(null) {
            lock.withLock {
                if (gate.get()) return
                // An account move stopped half way goes on before any orphan is looked for.
                EditStore.adoptAccount(context.filesDir, manager.emailKey(), offline.accountKey)
                runPass(offline, store, phase)
            }
        }
        // The start pass leaves what waits to the end pass of the same run.
        if (phase == EditPhase.Start) return
        guarded(Unit) {
            if (result?.syncWanted == true && phase == EditPhase.Check) manager.syncNow()
            // A pass that failed as a whole (the index cannot be read) is left to the periodic run.
            if (result?.again == true) scheduleCheck(offline.accountKey, result.delay)
        }
    }

    /** Runs [block]; a local failure gives [fallback] instead of a crash. Cancellation goes through. */
    private inline fun <T> guarded(fallback: T, block: () -> T): T = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        fallback
    }

    /** The edit check of [accountKey], only while that account is signed in. */
    suspend fun check(accountKey: String) {
        val offline = manager.store()?.takeIf { it.accountKey == accountKey } ?: return
        pass(offline, EditPhase.Check)
    }

    /** Runs a pass; the caller holds the lock. */
    private suspend fun runPass(offline: OfflineStore, store: EditStore, phase: EditPhase, only: Set<String>? = null): EditPass.Result =
        withContext(Dispatchers.IO) {
            manager.settingChange.set(false)
            EditPass(
                RepoEditServer(repo), store, { manager.syncedUri(offline, it) }, phase,
                conflictLabel = context.localized().getString(R.string.edit_conflict_label),
                stillSignedIn = { manager.store()?.accountKey == offline.accountKey },
                publish = ::publish,
                countStall = { !manager.settingChange.get() },
                // An account move into this key that is not finished: its files are no orphans.
                scanOrphans = !EditStore(context.filesDir, manager.emailKey()).let { it.accountKey != offline.accountKey && it.hasIndex() },
                strayLabel = context.localized().getString(R.string.edit_stray_label),
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
        guarded(null) {
            store.load().entries.firstOrNull { it.active && !it.outdated && it.fileId == fileId }
                ?.let { store.fileOf(it) }?.takeIf { it.isFile }
        }
    }

    /**
     * The file to read for the server file [path]: its working copy when there is one (whatever
     * version is asked for), else the synced copy ([OfflineManager.localCopy]). The working copy
     * is found by the file id at [path] (from the synced index, with [online] also from the
     * server, for a working copy that outlived its root); only when no id can be known, by address.
     */
    suspend fun localCopy(path: String, entity: String? = null, online: Boolean = false): File? = withContext(Dispatchers.IO) {
        val store = stores()?.second
        val entries = guarded(emptyList()) { store?.load()?.entries.orEmpty().filter { it.active && !it.outdated } }
        if (store != null && entries.isNotEmpty()) {
            val id = manager.syncedFileIdAt(path) ?: if (online) lookupId(path) else null
            val hit = if (id != null) entries.firstOrNull { it.fileId == id }
            else CrUri.canonical(path).let { c -> entries.firstOrNull { CrUri.canonical(it.uri) == c } }
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
        guarded(StartResult.NotReady) { lock.withLock { startLocked(store, file, offlineMode) } }
    }

    private suspend fun startLocked(store: EditStore, file: FileObject, offlineMode: Boolean): StartResult {
        run {
            store.load().entries.firstOrNull { it.active && it.fileId == file.id }?.let { existing ->
                val local = store.fileOf(existing)
                if (!existing.outdated && local.isFile) return StartResult.Ready(local, existing.id)
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
                return StartResult.NotReady
            }
            publish(store.put(made))
            return StartResult.Ready(store.fileOf(made), made.id)
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
        guarded(Unit) {
            lock.withLock {
                store.entry(entryId)?.let { publish(store.put(it.copy(grantedAt = System.currentTimeMillis()))) }
            }
        }
        guarded(Unit) { scheduleCheck(offline.accountKey) }
    }

    /**
     * "Bearbeitung abschliessen": the editor loses access, changes not yet uploaded go up now
     * (without waiting for the file to be quiet), and the copy is finished only when the server
     * holds exactly its bytes.
     */
    suspend fun finish(entryId: String): FinishResult = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext FinishResult.Done
        guarded(FinishResult.Failed(NotUploaded.Failed)) {
            lock.withLock {
                val entry = store.entry(entryId) ?: return@withLock FinishResult.Done
                revoke(store, entry)
                val failed = uploadLocked(offline, store, listOf(entry))
                if (failed != null) return@withLock FinishResult.Failed(failed)
                store.entry(entryId)?.let { retireLocked(store, it) }
                FinishResult.Done
            }
        }
    }

    /**
     * Uploads [entries] in an explicit pass (the caller holds the lock and revoked the grants).
     * Null when every one is settled afterwards, else why not.
     */
    private suspend fun uploadLocked(offline: OfflineStore, store: EditStore, entries: List<EditEntry>): NotUploaded? {
        // Done also when the working copy is missing but nothing of it is unconfirmed any more.
        fun open() = entries.mapNotNull { store.entry(it.id) }.filter { isUnsent(it, hashOf(store, it)) }
        val waiting = open()
        if (waiting.isEmpty()) return null
        // A missing working copy with a kept snapshot still has bytes to send: the pass goes first.
        fun missing(e: EditEntry) = !store.fileOf(e).isFile && e.pending == null
        if (!networkAllows()) return if (waiting.any(::missing)) NotUploaded.Missing else NotUploaded.Offline
        runPass(offline, store, EditPhase.Explicit, waiting.mapTo(HashSet()) { it.id })
        val left = open()
        return when {
            left.isEmpty() -> null
            left.any { it.problem == EditProblem.Refused || it.refusedSnapshots.isNotEmpty() } -> NotUploaded.Refused
            left.any { it.pending != null } -> NotUploaded.Pending
            left.any(::missing) -> NotUploaded.Missing
            else -> NotUploaded.Failed
        }
    }

    /** "Änderungen verwerfen": the working copy goes at once (with its unfinished upload, if any). False when that failed. */
    suspend fun discard(entryId: String): Boolean = discardAll(setOf(entryId))

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
        guarded(Unit) { lock.withLock { store.entry(entryId)?.let { publish(store.put(it.copy(conflict = ""))) } } }
    }

    /**
     * Working copies made in root [rootId] (or of its files) with changes the server does not
     * have. On a local failure none: switching a root off never touches working copies anyway.
     */
    suspend fun rootBlockers(rootId: String): List<EditEntry> = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext emptyList()
        guarded(emptyList()) {
            val ids = offline.load(rootId)?.entries.orEmpty().mapTo(HashSet()) { it.id }
            blockers(store).filter { it.rootId == rootId || it.fileId in ids }
        }
    }

    /** Explicit upload of [entries] (switching a root off); null when the server holds every change, else why not. */
    suspend fun upload(entries: List<EditEntry>): NotUploaded? = withContext(Dispatchers.IO) {
        val (offline, store) = stores() ?: return@withContext NotUploaded.Failed
        guarded(NotUploaded.Failed) {
            lock.withLock {
                entries.forEach { revoke(store, it) }
                uploadLocked(offline, store, entries)
            }
        }
    }

    /** Discards the working copies [ids]; false when that failed. */
    suspend fun discardAll(ids: Set<String>): Boolean = withContext(Dispatchers.IO) {
        val (_, store) = stores() ?: return@withContext false
        guarded(false) {
            lock.withLock { ids.forEach { id -> store.entry(id)?.let { discardLocked(store, it) } } }
            true
        }
    }

    private fun blockers(store: EditStore): List<EditEntry> =
        signOutBlockers(store.load().entries.map { it to hashOf(store, it) })

    /**
     * Fylvo: sign-out with working copies. First no new work and the running offline and edit
     * workers stop; the lock then waits for a pass that is still running. Then the choice is
     * applied: [SignOutChoice.Upload] uploads every change and goes on only when all are on the
     * server; [SignOutChoice.Discard] deletes exactly the copies [shown] in the question (when
     * another one changed meanwhile it asks again). Then [end] ends the session (revokes the
     * token), and the remaining copies are finished; they stay for the same account (done/,
     * 7 days). When it does not sign out, the background work is set up again.
     */
    suspend fun signOut(choice: SignOutChoice, shown: Set<String>, end: suspend () -> Unit): SignOutResult = withContext(Dispatchers.IO) {
        val stores = stores()
        gate.set(true)
        var result: SignOutResult = SignOutResult.Failed(shown, NotUploaded.Failed)
        try {
            OfflineSyncWorker.cancel(context)
            OfflineEditWorker.cancel(context)
            result = guarded(result) {
                lock.withLock {
                    if (stores != null) {
                        val (offline, store) = stores
                        val open = blockers(store)
                        val ids = open.mapTo(LinkedHashSet()) { it.id }
                        when (choice) {
                            SignOutChoice.None -> if (open.isNotEmpty()) return@withLock SignOutResult.NeedsChoice(ids)
                            SignOutChoice.Discard -> {
                                if (!shown.containsAll(ids)) return@withLock SignOutResult.NeedsChoice(ids)
                                shown.forEach { id -> store.entry(id)?.let { discardLocked(store, it) } }
                            }
                            SignOutChoice.Upload -> {
                                open.forEach { revoke(store, it) }
                                uploadLocked(offline, store, open)?.let { reason ->
                                    val left = blockers(store)
                                    return@withLock SignOutResult.Failed(
                                        left.mapTo(LinkedHashSet()) { it.id }, reason, left.map(::refusalCode).firstOrNull { it > 0 } ?: 0,
                                    )
                                }
                            }
                        }
                    }
                    withContext(NonCancellable) {
                        end()
                        stores?.second?.let { store ->
                            runCatching { store.load().entries.filter { it.active }.forEach { retireLocked(store, it) } }
                        }
                        forget()
                    }
                    SignOutResult.Done
                }
            }
            result
        } finally {
            gate.set(false)
            // Still signed in: the background work comes back.
            if (result != SignOutResult.Done) withContext(NonCancellable) {
                guarded(Unit) {
                    manager.start()
                    stores?.let { scheduleCheck(it.first.accountKey) }
                }
            }
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
