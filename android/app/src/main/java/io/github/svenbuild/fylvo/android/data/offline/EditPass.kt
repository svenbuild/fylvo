package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.api.FileObject
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
 * Fylvo: what an edit pass needs from the server (see [RepoEditServer]); a fake in the unit tests.
 */
internal interface EditServer {
    /** The file at [uri]; null when it is clearly not there. Refusals throw [ApiException]. */
    suspend fun lookup(uri: String): FileObject?

    /**
     * Uploads [file] as [name] into [folder]: with [previous] as a new version of exactly that
     * version of the file (previous_required), without as a new file that replaces nothing.
     */
    suspend fun upload(file: File, folder: String, name: String, lastModified: Long, previous: String, onSession: (String, String, String) -> Unit)

    /** Cancels an upload session; the server's parsed code, or null without a clear answer. */
    suspend fun cancel(sessionId: String, uri: String): Int?
}

internal class RepoEditServer(private val repo: FileRepository) : EditServer {
    override suspend fun lookup(uri: String): FileObject? = repo.lookup(uri)

    override suspend fun upload(file: File, folder: String, name: String, lastModified: Long, previous: String, onSession: (String, String, String) -> Unit) {
        repo.uploadFile(
            file, folder, name, overwrite = previous.isNotEmpty(), lastModified = lastModified,
            previous = previous, previousRequired = previous.isNotEmpty(), strict = true, onSession = onSession,
        )
    }

    override suspend fun cancel(sessionId: String, uri: String): Int? = repo.cancelUploadStrict(sessionId, uri)
}

/**
 * Fylvo: one pass over the edit index of [store]: uploads changed working copies (as a version of
 * their file, or as a conflict copy), clears up uploads an earlier run did not finish, and keeps
 * the finished copies. The caller holds [OfflineManager.editLock]; every change of an entry is
 * written to the index at once, so a stop at any point goes on from there.
 */
internal class EditPass(
    private val server: EditServer,
    private val store: EditStore,
    /** The server address of a file id as the synced index knows it now (renames). */
    private val syncedUri: (String) -> String?,
    private val phase: EditPhase,
    /** "Konflikt Handy" in the app language. */
    private val conflictLabel: String,
    /** False once another account is signed in (or nobody): the pass stops. */
    private val stillSignedIn: suspend () -> Boolean,
    private val publish: (EditIndex) -> Unit,
    private val clock: () -> Long = System::currentTimeMillis,
    /** False when the system stop of an upload was asked for by the app (a new network setting): no stall. */
    private val countStall: () -> Boolean = { true },
    /** False while an account move into this key is not finished: no orphan scan. */
    private val scanOrphans: Boolean = true,
    /** Name of a kept file whose own name is unknown ("Handy-Kopie" and the time). */
    private val strayLabel: String = "Handy-Kopie",
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
        if (only == null) guarded { housekeeping() }
        guarded { refreshUris() }
        for (id in store.load().entries.map { it.id }) {
            if (only != null && id !in only) continue
            if (!stillSignedIn()) break
            guarded { store.entry(id)?.let { handle(it) } }
        }
        if (only == null) guarded { refreshUris() }
        // An editor got access within the last hour: keep looking every few minutes.
        val now = clock()
        if (store.load().entries.any { it.active && now - it.grantedAt < EDIT_WATCH_MS }) result.again()
        return result
    }

    /**
     * A local failure (full disk, unreadable file) must not stop the pass or the sync run: the
     * entry stays as it is on disk (its marker too) and is looked at again later.
     */
    private suspend fun guarded(block: suspend () -> Unit) {
        try {
            block()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            result.again()
        }
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
        // A refusal recorded before its snapshot was renamed (a stop in between): the rename now.
        store.load().entries.forEach { entry ->
            entry.refusedSnapshots.forEach { kept ->
                val dest = store.refusedOf(entry, kept)
                val snap = store.snapshotOf(entry)
                if (!dest.exists() && entry.pending == null && snap.isFile && EditStore.sha256(snap) == kept.snapshot.sha256) snap.renameTo(dest)
            }
        }
        // While an account move is not finished its files may show up here without their entries.
        if (!scanOrphans) return
        // An editor wrote after a finish, or the index was lost: such files go up as conflict copies.
        for (orphan in store.orphans(store.load())) adopt(orphan, EditStore.safeName(orphan.name), null)
        // A file in tmp/ no entry points to. A snapshot whose bytes are its entry's working copy or
        // its last confirmed state goes (they are there or on the server). A refused snapshot is
        // never deleted: linked to its entry again. Anything else goes up as a conflict copy.
        val index = store.load()
        for (stray in store.strayTmp(index)) {
            val owner = EditStore.ownerOf(stray)?.let { id -> index.entries.firstOrNull { it.id == id } }
            val hash = EditStore.sha256(stray)
            if (owner != null && stray.name.startsWith("refused-")) {
                relinkRefused(owner, stray, hash)
                continue
            }
            if (owner != null && (hash == owner.reference.sha256 || hash == store.fileOf(owner).takeIf { it.isFile }?.let(EditStore::sha256))) {
                stray.delete()
                continue
            }
            val time = SimpleDateFormat("yyyy-MM-dd HH-mm", Locale.ROOT).format(Date(now))
            adopt(stray, owner?.name ?: "$strayLabel $time", owner?.uri)
        }
    }

    /** A refused snapshot that lost its place in the list goes back into it (under the name its bytes give). */
    private fun relinkRefused(owner: EditEntry, file: File, hash: String) {
        val kept = EditMarker(UploadKind.Version, EditSnapshot(file.length(), file.lastModified(), hash), target = owner.uri, at = clock())
        val dest = store.refusedOf(owner, kept)
        if (file != dest && (dest.exists() || !file.renameTo(dest))) return
        val current = store.entry(owner.id) ?: return
        if (current.refusedSnapshots.none { it.snapshot.sha256 == hash }) put(current.copy(refusedSnapshots = current.refusedSnapshots + kept))
    }

    /** Takes [file] into done/ as a finished copy that goes up as a conflict copy (into [uri]'s folder, else My files). */
    private fun adopt(file: File, name: String, uri: String?) {
        val now = clock()
        val entry = EditEntry(
            id = EditStore.newId(), fileId = "", uri = uri ?: CrUri.join(CrUri.MY_PREFIX, name), name = name,
            base = EditSnapshot(-1, 0, ""), state = EditState.Uploading, createdAt = now, retiredAt = now,
        )
        if (store.adoptOrphan(file, entry)) put(entry)
    }

    /** A rename or move on the server: the address follows the file id in the synced index. */
    private fun refreshUris() {
        store.load().entries.filter { !it.retired && it.state != EditState.Creating }.forEach { entry ->
            val uri = syncedUri(entry.fileId) ?: return@forEach
            if (CrUri.canonical(uri) != CrUri.canonical(entry.uri)) put(entry.copy(uri = uri))
        }
    }

    private suspend fun handle(entry: EditEntry) {
        val now = clock()
        val file = store.fileOf(entry)
        val stat = statOf(file)
        val sha256 = if (stat != null && (entry.pending == null || entry.problem == EditProblem.Refused)) EditStore.sha256(file) else null
        if (canPark(entry, sha256)) return park(entry)
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
        // A kept marker waits from now on (its recovery comes a few minutes later).
        store.entry(entry.id)?.let { put(it.copy(state = EditState.Uploading, problem = problem, pending = it.pending?.copy(at = clock()))) }
        result.again()
    }

    /**
     * The working copy moved on from a snapshot the server refused: that snapshot steps aside
     * (kept as refused-<id>-<hash>, with every other refused one) and the newer state is planned
     * normally. Refused snapshots go only with an explicit discard.
     */
    private suspend fun park(entry: EditEntry) {
        val marker = entry.pending ?: return
        val kept = marker.copy(refusedCode = entry.errorCode)
        val dest = store.refusedOf(entry, kept)
        val list = entry.refusedSnapshots.filter { it.snapshot.sha256 != kept.snapshot.sha256 } + kept
        // The index first (with the name the file gets), then the rename; a stop in between is
        // finished by the next housekeeping.
        put(entry.copy(pending = null, refusedSnapshots = list, state = EditState.Uploading, problem = null, errorCode = 0, attempts = 0))
        val snap = store.snapshotOf(entry)
        // The same bytes were refused before: that copy is kept already.
        val moved = if (dest.exists() && EditStore.sha256(dest) == kept.snapshot.sha256) snap.delete() else snap.renameTo(dest)
        // Not moved: no new snapshot may be written over it before the housekeeping moves it.
        if (!moved) return result.again()
        store.entry(entry.id)?.let { handle(it) }
    }

    /**
     * A fresh snapshot gets its marker (no session yet) before anything is asked of the server, so
     * one that has to wait stays referenced and is sent later; one never marked goes.
     */
    private suspend fun upload(entry: EditEntry) {
        // A refused snapshot still under the snapshot's name (not moved yet) is never written over.
        if (entry.refusedSnapshots.any { !store.refusedOf(entry, it).exists() } && store.snapshotOf(entry).exists()) return later(entry)
        val snapshot = takeSnapshot(entry) ?: return later(entry)
        val kind = if (entry.retired || entry.baseEntity.isEmpty()) UploadKind.Conflict else UploadKind.Version
        // Nothing can stop the pass between the snapshot and this write (no suspension). If the
        // write fails locally, the snapshot stays for the housekeeping, which deletes it only when
        // its bytes are the working copy's or the confirmed ones, and else uploads it.
        put(entry.copy(pending = EditMarker(kind, snapshot, target = entry.uri, at = clock()), state = EditState.Uploading, attempts = 0))
        store.entry(entry.id)?.let { sendSnapshot(it, snapshot) }
    }

    /**
     * Sends [snapshot] the way the server's file allows. A version only when the address still
     * holds the working copy's file (same id, a file): a copy with the same version at the old
     * address never receives the edit. This guard runs before every version upload, also when a
     * kept snapshot is sent again.
     */
    private suspend fun sendSnapshot(entry: EditEntry, snapshot: EditSnapshot) {
        val plan = if (entry.retired || entry.baseEntity.isEmpty()) TargetPlan.Conflict
        else planTarget(entry, preflight(entry.uri), phase.decideGone)
        when (plan) {
            TargetPlan.Version -> sendVersion(entry, snapshot)
            TargetPlan.Conflict -> sendConflict(entry, snapshot, private = false)
            TargetPlan.PrivateConflict -> sendConflict(entry, snapshot, private = true)
            TargetPlan.WaitForSync -> {
                result.syncWanted = true
                later(entry)
            }
            TargetPlan.Later -> later(entry)
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
        // On the disk before a marker points to it (a power loss must not leave a damaged snapshot behind a marker).
        // A copy that fails half way goes at once: it never had a marker and its bytes are in the working copy.
        val hash = try {
            src.inputStream().use { input ->
                java.io.FileOutputStream(snap).use { out -> EditStore.copyHashing(input, out).also { out.fd.sync() } }
            }
        } catch (e: Throwable) {
            snap.delete()
            throw e
        }
        if (statOf(src) != before || EditStore.sha256(src) != hash) {
            snap.delete()
            return null
        }
        return EditSnapshot(before.size, before.mtime, hash)
    }

    private suspend fun sendVersion(entry: EditEntry, snapshot: EditSnapshot) {
        if (!stillSignedIn()) return
        put(entry.copy(pending = EditMarker(UploadKind.Version, snapshot, target = entry.uri, at = clock()), state = EditState.Uploading, attempts = 0))
        val sent = send(entry.id) {
            server.upload(
                store.snapshotOf(entry), CrUri.parent(entry.uri), CrUri.fileName(entry.uri), snapshot.mtime,
                previous = entry.baseEntity, onSession = sessionNoted(entry.id),
            )
        }
        if (sent == Sent.Done) return
        // Nothing reached the server. Network trouble or "later": the marker and its snapshot stay,
        // a later recovery sends that snapshot again.
        val current = store.entry(entry.id) ?: return
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
            if (!stillSignedIn()) return
            val current = store.entry(entry.id) ?: return
            val folder = conflictFolder(current, mine)
            val name = conflictName(baseName, conflictLabel, time, counter)
            val marker = EditMarker(UploadKind.Conflict, snapshot, target = folder, name = name, at = clock())
            put(current.copy(pending = marker, state = EditState.Uploading, attempts = 0))
            val sent = send(entry.id) {
                server.upload(store.snapshotOf(entry), folder, name, snapshot.mtime, previous = "", onSession = sessionNoted(entry.id))
            }
            // Network trouble: the marker stays, a later recovery sends the same snapshot again.
            if (sent == Sent.Done) return
            if (sent !is Sent.Refused) return later(current)
            when {
                isNameTaken(sent.code) -> counter++
                // No right in the original folder (a share left, its membership ended): My files.
                !mine && (sent.code in NO_ACCESS_CODES || sent.code in GONE_CODES) -> {
                    mine = true
                    counter = 1
                }
                else -> when (classifyRefusal(sent.code)) {
                    UploadOutcome.Later, UploadOutcome.Replan -> return later(current)
                    UploadOutcome.ServerFull -> return later(current, EditProblem.ServerFull)
                    else -> return refused(current, sent.code)
                }
            }
        }
        store.entry(entry.id)?.let { refused(it, ApiException.CODE_OBJECT_EXISTS) }
    }

    /**
     * A clear refusal the app cannot get around: the entry shows it with the code. Its marker and
     * snapshot stay (from the store, so the snapshot is never left without a marker) until the bytes
     * are on the server or the person discards them; periodic runs and "Sync now" try again.
     */
    private fun refused(entry: EditEntry, code: Int) {
        val current = store.entry(entry.id) ?: return
        put(current.copy(state = EditState.Error, problem = EditProblem.Refused, errorCode = code, pending = current.pending?.copy(at = clock())))
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
        val file = runCatching { server.lookup(marker.sessionUri) }.getOrElse {
            if (it is CancellationException) throw it
            null
        }
        if (file != null && isCurrentVersion(file, marker.entityId)) commit(entry, marker, file) else lost(id)
    }

    private fun commit(entry: EditEntry, marker: EditMarker, file: FileObject) {
        val next = if (marker.kind == UploadKind.Version) confirmVersion(entry, marker) else rebaseOnto(entry, file, marker)
        // Written to again meanwhile: that change waits for the next upload.
        val stat = statOf(store.fileOf(next))
        val changed = stat != null && (stat.size != marker.snapshot.size || stat.mtime != marker.snapshot.mtime)
        // The index first: the snapshot goes only once nothing points to it any more.
        put(next.copy(state = if (changed) EditState.Uploading else EditState.Synced, seen = null))
        store.snapshotOf(entry).delete()
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
        val stalls = if (countStall()) e.stalls + 1 else e.stalls
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
     * Like [keep] while the file is still there with its id; after [EDIT_MAX_UNCLEAR] unclear
     * answers the snapshot goes up as a conflict copy (a duplicate at worst), so later edits are
     * not held up for ever.
     */
    private suspend fun keepOrCopy(entry: EditEntry, marker: EditMarker) {
        if (entry.attempts + 1 >= EDIT_MAX_UNCLEAR) sendConflict(entry, marker.snapshot, private = false) else keep(entry)
    }

    /**
     * The kept snapshot still has the bytes its marker names. If not (damaged, a power loss): it is
     * dropped and the working copy is planned anew; never sent under a hash it does not have.
     */
    private suspend fun intact(entry: EditEntry, marker: EditMarker): Boolean {
        if (EditStore.sha256(store.snapshotOf(entry)) == marker.snapshot.sha256) return true
        store.snapshotOf(entry).delete()
        put(entry.copy(pending = null))
        store.entry(entry.id)?.let { handle(it) }
        return false
    }

    /**
     * An upload of an earlier run without a final answer. With a session: the session is cancelled
     * first (a finished one is not affected), then the file tells whether our version got in: only
     * when it is the file's current version (an unfinished upload is listed among the versions
     * too, so being listed proves nothing). Whatever is not proven goes up again from the kept
     * snapshot, the only copy known to be whole; a duplicate is acceptable, a loss is not.
     */
    private suspend fun recover(entry: EditEntry) {
        val marker = entry.pending!!
        if (!store.snapshotOf(entry).isFile) {
            // Nothing left to send again: plan anew from the working copy.
            put(entry.copy(pending = null))
            return store.entry(entry.id)?.let { handle(it) } ?: Unit
        }
        if (!intact(entry, marker)) return
        // Nothing reached the server: the same snapshot goes again.
        if (marker.sessionId.isEmpty()) return resend(entry, marker)
        // A version belongs to the working copy's file at its address now (found by id, renames
        // followed); a conflict copy to its session's address. The cancel goes there too.
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
            else -> when (classifyCancel(server.cancel(marker.sessionId, target))) {
                CancelOutcome.Keep -> keepOrCopy(entry, marker)
                CancelOutcome.PrivateConflict -> sendConflict(entry, marker.snapshot, private = true)
                CancelOutcome.Check -> {
                    val file = try {
                        server.lookup(target)
                    } catch (e: CancellationException) {
                        throw e
                    } catch (_: Exception) {
                        return keepOrCopy(entry, marker)
                    }
                    if (file != null && isCurrentVersion(file, marker.entityId)) return commit(entry, marker, file)
                    // Not in, and the session is gone: the snapshot goes again as a new upload.
                    val fresh = marker.copy(sessionId = "", sessionUri = "", entityId = "")
                    put(entry.copy(pending = fresh))
                    store.entry(entry.id)?.let { resend(it, fresh) }
                }
            }
        }
    }

    /**
     * Sends the kept snapshot of [marker] again: as a version only through the guard of
     * [sendSnapshot] (a newer version on the server makes it a conflict copy), or as a conflict copy.
     */
    private suspend fun resend(entry: EditEntry, marker: EditMarker) = when (marker.kind) {
        UploadKind.Version -> sendSnapshot(entry, marker.snapshot)
        UploadKind.Conflict -> sendConflict(entry, marker.snapshot, private = marker.target == CrUri.MY_PREFIX)
    }

    /** The server's view of [uri] before an upload. */
    private suspend fun preflight(uri: String): Preflight = try {
        preflightOf(server.lookup(uri), null)
    } catch (e: CancellationException) {
        throw e
    } catch (e: ApiException) {
        preflightOf(null, e.code)
    } catch (_: Exception) {
        Preflight.Unclear
    }

    private fun statOf(file: File): LocalStat? = file.takeIf { it.isFile }?.let { LocalStat(it.length(), it.lastModified()) }
}
