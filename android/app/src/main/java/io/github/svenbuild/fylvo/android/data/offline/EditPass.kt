package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.util.CrUri
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * Fylvo: when an edit pass runs. [decideGone]: the root listings of the run are done, so a file
 * missing at its address is really gone. [retryErrors]: a periodic run, "Sync now" or an explicit
 * action. [explicit]: "Bearbeitung abschliessen" or sign-out, without the stable rule.
 */
enum class EditPhase(val decideGone: Boolean, val retryErrors: Boolean, val explicit: Boolean) {
    /** At the start of a sync run. */
    Start(decideGone = false, retryErrors = true, explicit = false),
    /** At the end of a sync run. */
    End(decideGone = true, retryErrors = true, explicit = false),
    /** The edit check on its own (after "Bearbeiten", at app start). */
    Check(decideGone = false, retryErrors = false, explicit = false),
    Explicit(decideGone = true, retryErrors = true, explicit = true),
}

/**
 * Fylvo: one pass over the edit index of [store]: uploads changed working copies (as a version of
 * their file, or as a conflict copy), clears up uploads an earlier run did not finish, and keeps
 * the finished copies. The caller holds [OfflineManager.editLock]; every change of an entry is
 * written to the index at once, so a stop at any point goes on from there.
 */
internal class EditPass(
    private val repo: FileRepository,
    private val manager: OfflineManager,
    private val store: EditStore,
    private val offline: OfflineStore,
    private val phase: EditPhase,
    /** "Konflikt Handy" in the app language. */
    private val conflictLabel: String,
    /** False once another account is signed in (or nobody): the pass stops. */
    private val stillSignedIn: suspend () -> Boolean,
    private val publish: (EditIndex) -> Unit,
    private val clock: () -> Long = System::currentTimeMillis,
) {
    /** What the caller schedules afterwards. */
    class Result {
        /** Something waits (unstable, server busy, recovery): check again in [delay]. */
        var again = false
            private set
        var delay = EDIT_RECOVERY_DELAY_MS
            private set
        /** An address did not match while the root listing may still change it: a sync helps. */
        var syncWanted = false

        fun again(after: Long = EDIT_RECOVERY_DELAY_MS) {
            delay = if (again) minOf(delay, after) else after
            again = true
        }
    }

    val result = Result()

    private sealed class Sent {
        /** Confirmed, or kept for a later recovery: nothing more to do now. */
        data object Done : Sent()
        /** The session was not created: nothing reached the server. */
        data object Network : Sent()
        data class Refused(val code: Int) : Sent()
    }

    suspend fun run(only: Set<String>? = null): Result {
        if (only == null) housekeeping()
        refreshUris()
        for (id in store.load().entries.map { it.id }) {
            if (only != null && id !in only) continue
            if (!stillSignedIn()) break
            store.entry(id)?.let { handle(it) }
        }
        if (only == null) refreshUris()
        return result
    }

    private fun put(entry: EditEntry) = publish(store.put(entry))

    private fun remove(entry: EditEntry) {
        store.deleteFiles(entry)
        publish(store.remove(entry.id))
    }

    /** Clears up what a stop left: half-made copies, an unrecorded finish, files without an entry. */
    private fun housekeeping() {
        val now = clock()
        store.load().entries.forEach { entry ->
            when {
                // Never uploaded: its bytes are still in the synced copy.
                entry.state == EditState.Creating -> remove(entry)
                !entry.retired && store.isOnlyInDone(entry) -> put(entry.copy(retiredAt = now))
            }
        }
        // An editor wrote after a finish, or the index was lost: such files go up as conflict copies.
        for (orphan in store.orphans(store.load())) {
            val name = EditStore.safeName(orphan.name)
            val entry = EditEntry(
                id = EditStore.newId(), fileId = "", uri = CrUri.join(CrUri.MY_PREFIX, name), name = name,
                base = EditSnapshot(-1, 0, ""), state = EditState.Uploading, createdAt = now, retiredAt = now,
            )
            if (store.adoptOrphan(orphan, entry)) put(entry)
        }
        store.cleanTmp(store.load())
    }

    /** A rename or move on the server: the address follows the file id in the synced index. */
    private fun refreshUris() {
        store.load().entries.filter { !it.retired && it.state != EditState.Creating }.forEach { entry ->
            val uri = manager.syncedUri(offline, entry.fileId) ?: return@forEach
            if (CrUri.canonical(uri) != CrUri.canonical(entry.uri)) put(entry.copy(uri = uri))
        }
    }

    private suspend fun handle(entry: EditEntry) {
        val now = clock()
        val file = store.fileOf(entry)
        val stat = statOf(file)
        val sha256 = if (stat != null && entry.pending == null) EditStore.sha256(file) else null
        when (planEdit(entry, stat, sha256, now, phase.explicit, phase.retryErrors)) {
            EditStep.Skip -> Unit
            EditStep.Missing -> if (entry.problem != EditProblem.Missing) {
                put(entry.copy(state = EditState.Error, problem = EditProblem.Missing))
            }
            EditStep.WaitRecovery -> result.again(entry.pending!!.at + EDIT_RECOVERY_DELAY_MS - now)
            EditStep.Recover -> recover(entry)
            EditStep.Unchanged -> unchanged(entry, stat!!)
            EditStep.Unstable -> {
                put(entry.copy(state = EditState.Uploading, seen = nextSeen(stat!!, entry.seen, now)))
                result.again()
            }
            EditStep.Upload -> upload(entry)
            EditStep.Expire -> remove(entry)
        }
    }

    private suspend fun unchanged(entry: EditEntry, stat: LocalStat) {
        var next = entry.copy(seen = nextSeen(stat, entry.seen, clock()))
        if (!entry.retired) {
            next = next.copy(state = if (entry.confirmed != null) EditState.Synced else EditState.Editing, problem = null, errorCode = 0)
            // After the listings: does the server have a newer version meanwhile?
            if (phase == EditPhase.End && entry.baseEntity.isNotEmpty()) {
                val server = preflight(entry.uri)
                if (server is Preflight.Found && server.id == entry.fileId) next = next.copy(outdated = isOutdated(entry, false, server))
            }
        }
        if (next != entry) put(next)
    }

    private fun later(entry: EditEntry, problem: EditProblem? = null) {
        store.entry(entry.id)?.let { put(it.copy(state = EditState.Uploading, problem = problem)) }
        result.again()
    }

    private suspend fun upload(entry: EditEntry) {
        val plan = if (entry.retired || entry.baseEntity.isEmpty()) TargetPlan.Conflict
        else planTarget(entry, preflight(entry.uri), phase.decideGone)
        if (plan == TargetPlan.Later || plan == TargetPlan.WaitForSync) {
            if (plan == TargetPlan.WaitForSync) result.syncWanted = true
            return later(entry)
        }
        val snapshot = takeSnapshot(entry) ?: return later(entry)
        when (plan) {
            TargetPlan.Version -> sendVersion(entry, snapshot)
            else -> sendConflict(entry, snapshot, private = plan == TargetPlan.PrivateConflict)
        }
    }

    /**
     * Copies the working copy into tmp/ and checks it did not change while copying (same size
     * and time before and after, and the copy's hash equals a fresh hash of the file). Null when
     * it changed: it is looked at again later.
     */
    private fun takeSnapshot(entry: EditEntry): EditSnapshot? {
        val src = store.fileOf(entry)
        val snap = store.snapshotOf(entry)
        store.tmpFolder()
        val before = statOf(src) ?: return null
        val hash = src.inputStream().use { input -> snap.outputStream().use { EditStore.copyHashing(input, it) } }
        if (statOf(src) != before || EditStore.sha256(src) != hash) {
            snap.delete()
            return null
        }
        return EditSnapshot(before.size, before.mtime, hash)
    }

    private suspend fun sendVersion(entry: EditEntry, snapshot: EditSnapshot) {
        put(entry.copy(pending = EditMarker(UploadKind.Version, snapshot, target = entry.uri, at = clock()), state = EditState.Uploading))
        val sent = send(entry.id) {
            repo.uploadFile(
                store.snapshotOf(entry), CrUri.parent(entry.uri), CrUri.fileName(entry.uri), overwrite = true,
                lastModified = snapshot.mtime, previous = entry.baseEntity, previousRequired = true, strict = true,
                onSession = sessionNoted(entry.id),
            )
        }
        if (sent == Sent.Done) return
        // Nothing reached the server: the marker goes, the snapshot is used once more or cleaned up.
        val current = store.entry(entry.id)?.copy(pending = null) ?: return
        put(current)
        if (sent !is Sent.Refused) return later(current)
        when (classifyRefusal(sent.code)) {
            UploadOutcome.Conflict -> sendConflict(current, snapshot, private = false)
            UploadOutcome.PrivateConflict -> sendConflict(current, snapshot, private = true)
            UploadOutcome.Later, UploadOutcome.Replan -> later(current)
            UploadOutcome.ServerFull -> later(current, EditProblem.ServerFull)
            UploadOutcome.Error -> refused(current, sent.code)
        }
    }

    /**
     * Uploads [snapshot] as a new file next to the original (or into My files when [private]),
     * never over anything; a taken name gets the next counter.
     */
    private suspend fun sendConflict(entry: EditEntry, snapshot: EditSnapshot, private: Boolean) {
        var mine = private
        val time = SimpleDateFormat("yyyy-MM-dd HH-mm", Locale.ROOT).format(Date(clock()))
        val baseName = CrUri.fileName(entry.uri).ifEmpty { entry.name }
        var counter = 1
        while (counter <= CONFLICT_MAX_TRIES) {
            val current = store.entry(entry.id) ?: return
            val folder = conflictFolder(current, mine)
            val name = conflictName(baseName, conflictLabel, time, counter)
            val marker = EditMarker(UploadKind.Conflict, snapshot, target = folder, name = name, at = clock())
            put(current.copy(pending = marker, state = EditState.Uploading))
            val sent = send(entry.id) {
                repo.uploadFile(
                    store.snapshotOf(entry), folder, name, overwrite = false, lastModified = snapshot.mtime, strict = true,
                    onSession = sessionNoted(entry.id),
                )
            }
            // Network trouble: the marker stays, a later recovery sends the same snapshot again.
            if (sent == Sent.Done) return
            if (sent !is Sent.Refused) return later(current)
            when {
                isNameTaken(sent.code) -> counter++
                !mine && sent.code in NO_ACCESS_CODES -> {
                    mine = true
                    counter = 1
                }
                else -> when (classifyRefusal(sent.code)) {
                    UploadOutcome.Later, UploadOutcome.Replan -> return later(current)
                    UploadOutcome.ServerFull -> return later(current, EditProblem.ServerFull)
                    else -> return refused(current.copy(pending = null), sent.code)
                }
            }
        }
        store.entry(entry.id)?.let { refused(it.copy(pending = null), ApiException.CODE_OBJECT_EXISTS) }
    }

    private fun refused(entry: EditEntry, code: Int) {
        put(entry.copy(state = EditState.Error, problem = EditProblem.Refused, errorCode = code))
    }

    /** Records the session in the marker before any byte is sent. */
    private fun sessionNoted(id: String): (String, String, String) -> Unit = { sessionId, uri, entityId ->
        store.entry(id)?.let { e ->
            put(e.copy(pending = e.pending?.copy(sessionId = sessionId, sessionUri = uri, entityId = entityId, at = clock())))
        }
    }

    private fun sessionStarted(id: String): Boolean = store.entry(id)?.pending?.sessionId?.isNotEmpty() == true

    /** Runs one upload; confirms it, or keeps its marker for a recovery when it was not clearly refused. */
    private suspend fun send(id: String, upload: suspend () -> Unit): Sent {
        try {
            upload()
        } catch (e: CancellationException) {
            withContext(NonCancellable) { stalled(id) }
            throw e
        } catch (e: Exception) {
            if (sessionStarted(id)) {
                lost(id)
                return Sent.Done
            }
            return if (e is ApiException) Sent.Refused(e.code) else Sent.Network
        }
        confirm(id)
        return Sent.Done
    }

    /** After the final answer: committed only when the file's current version is ours. */
    private suspend fun confirm(id: String) {
        val entry = store.entry(id) ?: return
        val marker = entry.pending ?: return
        // No session (a rapid upload): nothing to prove it by; it is sent again (a duplicate at worst).
        if (marker.sessionId.isEmpty()) return lost(id)
        val file = runCatching { repo.lookup(marker.sessionUri) }.getOrElse {
            if (it is CancellationException) throw it
            null
        }
        if (file != null && isCurrentVersion(file, marker.entityId)) commit(entry, marker, file) else lost(id)
    }

    private fun commit(entry: EditEntry, marker: EditMarker, file: io.github.svenbuild.fylvo.android.api.FileObject) {
        val next = if (marker.kind == UploadKind.Version) confirmVersion(entry, marker) else rebaseOnto(entry, file, marker)
        store.snapshotOf(entry).delete()
        // Written to again meanwhile: that change waits for the next upload.
        val stat = statOf(store.fileOf(next))
        val changed = stat != null && (stat.size != marker.snapshot.size || stat.mtime != marker.snapshot.mtime)
        put(next.copy(state = if (changed) EditState.Uploading else EditState.Synced, seen = null))
        if (changed) result.again()
    }

    /** The final answer is unknown: the marker stays; a later run clears it up (never this one). */
    private fun lost(id: String) {
        store.entry(id)?.let { e -> put(e.copy(pending = e.pending?.copy(at = clock()))) }
        result.again()
    }

    /** The system stopped the upload (time limit, constraints): three times in a row ends in [EditProblem.Stalled]. */
    private fun stalled(id: String) {
        val e = store.entry(id) ?: return
        val stalls = e.stalls + 1
        val stuck = stalls >= EDIT_MAX_STALLS
        put(
            e.copy(
                stalls = stalls, pending = e.pending?.copy(at = clock()),
                state = if (stuck) EditState.Error else EditState.Uploading, problem = if (stuck) EditProblem.Stalled else e.problem,
            )
        )
    }

    /** No clear answer while clearing up: tried again after 3 min, 15 min, 1 h, then on periodic runs. */
    private fun keep(entry: EditEntry) {
        put(entry.copy(attempts = entry.attempts + 1, pending = entry.pending?.copy(at = clock())))
        retryDelay(entry.attempts)?.let { result.again(it) }
    }

    /**
     * An upload of an earlier run without a final answer. With a session: the session is cancelled
     * first (a finished one is not affected), then the file tells whether our version got in.
     */
    private suspend fun recover(entry: EditEntry) {
        val marker = entry.pending!!
        if (!store.snapshotOf(entry).isFile) {
            // Nothing left to send again: plan anew from the working copy.
            put(entry.copy(pending = null))
            return store.entry(entry.id)?.let { handle(it) } ?: Unit
        }
        if (marker.sessionId.isEmpty()) {
            // Nothing reached the server. A version is planned anew, a conflict copy sent again.
            if (marker.kind == UploadKind.Version) {
                put(entry.copy(pending = null))
                return store.entry(entry.id)?.let { handle(it) } ?: Unit
            }
            return sendConflict(entry, marker.snapshot, private = marker.target == CrUri.MY_PREFIX)
        }
        // A version belongs to the working copy's file (found by id); a conflict copy to its session's address.
        val target = if (marker.kind == UploadKind.Version) entry.uri else marker.sessionUri
        val found = preflight(target)
        val ours = found is Preflight.Found && !found.folder && (marker.kind == UploadKind.Conflict || found.id == entry.fileId)
        when {
            found is Preflight.Unclear -> keep(entry)
            found is Preflight.NoAccess -> sendConflict(entry, marker.snapshot, private = true)
            !ours && marker.kind == UploadKind.Version && !phase.decideGone -> {
                // Maybe renamed on the server: the listing of this run tells.
                result.syncWanted = true
                result.again()
            }
            // Our version cannot be on that file: the snapshot goes up as a conflict copy.
            !ours -> sendConflict(entry, marker.snapshot, private = false)
            else -> when (classifyCancel(repo.cancelUploadStrict(marker.sessionId, marker.sessionUri))) {
                CancelOutcome.Keep -> keep(entry)
                CancelOutcome.PrivateConflict -> sendConflict(entry, marker.snapshot, private = true)
                CancelOutcome.Check -> {
                    val file = try {
                        repo.lookup(target, extended = true)
                    } catch (e: CancellationException) {
                        throw e
                    } catch (_: Exception) {
                        return keep(entry)
                    }
                    when {
                        file != null && holdsVersion(file, marker.entityId) -> commit(entry, marker, file)
                        marker.kind == UploadKind.Version -> {
                            put(entry.copy(pending = null))
                            store.entry(entry.id)?.let { handle(it) }
                        }
                        else -> sendConflict(entry, marker.snapshot, private = marker.target == CrUri.MY_PREFIX)
                    }
                }
            }
        }
    }

    /** The server's view of [uri] before an upload. */
    private suspend fun preflight(uri: String): Preflight = try {
        preflightOf(repo.lookup(uri), null)
    } catch (e: CancellationException) {
        throw e
    } catch (e: ApiException) {
        preflightOf(null, e.code)
    } catch (_: Exception) {
        Preflight.Unclear
    }

    private fun statOf(file: File): LocalStat? = file.takeIf { it.isFile }?.let { LocalStat(it.length(), it.lastModified()) }
}
