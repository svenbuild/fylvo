package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.serialization.Serializable
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.util.CrUri

/**
 * Fylvo: files of offline roots edited on this phone ("working copies"). Each working copy is a
 * file of its own next to the synced copies (see [EditStore]); the sync never replaces, moves or
 * deletes one. Edits are uploaded as new versions of their file, or as a conflict copy when the
 * file changed on the server meanwhile, so nothing written on the phone is ever lost.
 * Everything here is pure and unit-tested; the network part is [EditPass].
 */
@Serializable
data class EditIndex(val entries: List<EditEntry> = emptyList())

/** Size, modification time and SHA-256 of a working copy at one moment. */
@Serializable
data class EditSnapshot(val size: Long, val mtime: Long, val sha256: String)

/** Size and modification time of a working copy as a pass saw them first, for the stable rule. */
@Serializable
data class EditSeen(val size: Long, val mtime: Long, val at: Long)

@Serializable
enum class EditState {
    /** Being copied from the synced copy; a crash leaves only this, which is deleted and never uploaded. */
    Creating,
    /** No change since it was made (or since the last upload was confirmed). */
    Editing,
    /** Changed; the change waits for its upload. */
    Uploading,
    /** The server holds exactly these bytes. */
    Synced,
    /** Needs a look ([EditEntry.problem]); tried again only on a periodic run or "Sync now". */
    Error,
}

@Serializable
enum class EditProblem {
    /** The working copy is gone (an editor deleted it); nothing to upload or finish. */
    Missing,
    /** The server's storage is full; tried again later. */
    ServerFull,
    /** The upload was stopped by the system three times in a row. */
    Stalled,
    /** The server refused the upload with a code the app does not know ([EditEntry.errorCode]). */
    Refused,
}

@Serializable
enum class UploadKind {
    /** A new version of the file, only if the file still has the version the edit is based on. */
    Version,
    /** A new file next to it (or in My files), never replacing anything. */
    Conflict,
}

/**
 * Written before an upload starts and again when the server answered the session, so a crash at
 * any point can be cleared up ([EditPass] recovery). The snapshot file stays until it is resolved.
 */
@Serializable
data class EditMarker(
    val kind: UploadKind,
    val snapshot: EditSnapshot,
    /** Version: the file's address. Conflict: the folder the copy goes into. */
    val target: String,
    /** Conflict: the name of the copy. */
    val name: String = "",
    /** Empty until the server answered the session: then nothing was sent yet. */
    val sessionId: String = "",
    /** The address the session belongs to (the server may pick another name). */
    val sessionUri: String = "",
    /** The entity (version) the upload becomes. */
    val entityId: String = "",
    /** Written or last given up; a recovery waits [EDIT_RECOVERY_DELAY_MS] after it. */
    val at: Long,
    /** The server's code when it refused these bytes (see EditEntry.refusedSnapshots). */
    val refusedCode: Int = 0,
)

@Serializable
data class EditEntry(
    /** Random id, also the name of the working copy's folder; never changes. */
    val id: String,
    /** The server file the working copy belongs to; a rebase after a conflict copy changes it. */
    val fileId: String,
    /** The offline root it was made in (only for the switch-off question). */
    val rootId: String = "",
    /** The file's address on the server, refreshed by file id from the synced index. */
    val uri: String,
    /** File name at creation; the working copy keeps it (also after a rename on the server). */
    val name: String,
    /** The version the working copy is based on; empty for an empty file without version. */
    val baseEntity: String = "",
    /** The bytes the working copy was made from. */
    val base: EditSnapshot,
    /** The last upload the server confirmed. */
    val confirmed: EditSnapshot? = null,
    val pending: EditMarker? = null,
    /**
     * Snapshots the server clearly refused (each kept in tmp/ as refused-<id>-<hash>) while newer
     * states of the working copy go up. They go only with an explicit discard; until then the
     * entry counts as not uploaded.
     */
    val refusedSnapshots: List<EditMarker> = emptyList(),
    val state: EditState = EditState.Creating,
    val problem: EditProblem? = null,
    val errorCode: Int = 0,
    /** Name of the last conflict copy made from it (shown as a notice). */
    val conflict: String = "",
    /** Unchanged, and the server has a newer version: opening shows the synced copy again. */
    val outdated: Boolean = false,
    val createdAt: Long = 0,
    /** When an editor last got write access. */
    val grantedAt: Long = 0,
    /** When it was finished ("retired", moved to done/); 0 = in use. */
    val retiredAt: Long = 0,
    val seen: EditSeen? = null,
    /** Uploads stopped by the system in a row. */
    val stalls: Int = 0,
    /** Recoveries of [pending] without a clear answer in a row (for the retry delay). */
    val attempts: Int = 0,
    /**
     * Fylvo 1.6: made from the server, not from an offline copy ("Edit" without keeping the file
     * offline). Its address is followed by file id on the server; once uploaded and left alone for
     * a day it is finished by itself and goes a day later.
     */
    val standalone: Boolean = false,
) {
    val retired: Boolean get() = retiredAt > 0
    /** What the working copy is compared with: the last confirmed upload, else the bytes it was made from. */
    val reference: EditSnapshot get() = confirmed ?: base
    /** In use: shown, opened and uploaded as the file's current content. */
    val active: Boolean get() = !retired && state != EditState.Creating
}

const val EDIT_STABLE_MS = 2 * 60_000L
const val EDIT_RECOVERY_DELAY_MS = 3 * 60_000L
const val EDIT_RETIRED_KEEP_MS = 7 * 24 * 60 * 60_000L
/** Fylvo 1.6: a standalone working copy, uploaded and untouched this long, is finished by itself... */
const val EDIT_STANDALONE_IDLE_MS = 24 * 60 * 60_000L
/** ...and kept this long after that (watched for a late write, which goes up as a conflict copy). */
const val EDIT_STANDALONE_KEEP_MS = 24 * 60 * 60_000L
/** After an editor got access, the edit check keeps looking this long. */
const val EDIT_WATCH_MS = 60 * 60_000L
const val EDIT_MAX_STALLS = 3
/** Unclear answers while clearing up an upload whose file is still there; then a conflict copy. */
const val EDIT_MAX_UNCLEAR = 5
const val CONFLICT_MAX_TRIES = 20
const val NAME_MAX_BYTES = 255

/**
 * Fylvo 1.6: a standalone working copy that can be finished by itself: in use, everything of it is
 * on the server (nothing changed since the last confirmed upload, nothing pending or refused), and
 * neither the file nor an editor's access changed for [EDIT_STANDALONE_IDLE_MS].
 */
fun idleStandalone(entry: EditEntry, stat: LocalStat?, sha256: String?, now: Long): Boolean =
    entry.standalone && entry.active && entry.pending == null && entry.refusedSnapshots.isEmpty() &&
        stat != null && sha256 != null && !isChanged(entry, sha256) &&
        now - stat.mtime >= EDIT_STANDALONE_IDLE_MS && now - maxOf(entry.grantedAt, entry.createdAt) >= EDIT_STANDALONE_IDLE_MS

/** The working copy differs from its last confirmed upload (or from what it was made from). */
fun isChanged(entry: EditEntry, sha256: String): Boolean = sha256 != entry.reference.sha256

/**
 * An editor is probably done writing: the file was not modified for [EDIT_STABLE_MS], or a pass
 * saw the same size and time at least that long ago (covers a clock that runs differently).
 */
fun isStable(stat: LocalStat, seen: EditSeen?, now: Long): Boolean =
    now - stat.mtime >= EDIT_STABLE_MS ||
        (seen != null && seen.size == stat.size && seen.mtime == stat.mtime && now - seen.at >= EDIT_STABLE_MS)

/** What a pass remembers as seen: kept while size and time stay the same. */
fun nextSeen(stat: LocalStat, seen: EditSeen?, now: Long): EditSeen =
    if (seen != null && seen.size == stat.size && seen.mtime == stat.mtime) seen else EditSeen(stat.size, stat.mtime, now)

enum class EditStep {
    /** Nothing to do in this pass. */
    Skip,
    /** The working copy is gone. */
    Missing,
    /** An upload of an earlier run is not resolved: clear it up first. */
    Recover,
    /** Like [Recover], but too soon after it was lost. */
    WaitRecovery,
    Unchanged,
    /** Changed, but maybe still being written. */
    Unstable,
    Upload,
    /** A finished copy that is gone or was kept long enough: its entry (and file) go. */
    Expire,
}

/**
 * What a pass does with [entry]. [stat] and [sha256] describe the working copy now (null when it
 * is missing). [explicit]: "Bearbeitung abschliessen" or sign-out, without the stable rule.
 * [retryErrors]: a periodic run or "Sync now" (a standalone edit check leaves errors alone).
 */
fun planEdit(entry: EditEntry, stat: LocalStat?, sha256: String?, now: Long, explicit: Boolean, retryErrors: Boolean): EditStep {
    val pending = entry.pending
    return when {
        entry.state == EditState.Creating -> EditStep.Skip
        entry.state == EditState.Error && !retryErrors && !explicit -> EditStep.Skip
        pending != null -> if (now - pending.at >= EDIT_RECOVERY_DELAY_MS) EditStep.Recover else EditStep.WaitRecovery
        // An entry with refused snapshots never expires: they go only with an explicit discard.
        stat == null || sha256 == null -> when {
            !entry.retired -> EditStep.Missing
            entry.refusedSnapshots.isEmpty() -> EditStep.Expire
            else -> EditStep.Skip
        }
        !isChanged(entry, sha256) ->
            if (entry.retired && entry.refusedSnapshots.isEmpty() &&
                now - entry.retiredAt >= (if (entry.standalone) EDIT_STANDALONE_KEEP_MS else EDIT_RETIRED_KEEP_MS)) EditStep.Expire
            else EditStep.Unchanged
        explicit || isStable(stat, entry.seen, now) -> EditStep.Upload
        else -> EditStep.Unstable
    }
}

/** What the server says about the address of a file before an upload. */
sealed class Preflight {
    data class Found(val id: String, val folder: Boolean, val primaryEntity: String) : Preflight()
    data object Gone : Preflight()
    /** A clear "you may not": no right, the share is gone. */
    data object NoAccess : Preflight()
    /** No clear answer (network, server trouble). */
    data object Unclear : Preflight()
}

enum class TargetPlan { Version, Conflict, PrivateConflict, Later, WaitForSync }

/**
 * How a changed working copy goes up. [decideGone]: the root listing of this run is done (end pass,
 * explicit actions), so a missing file or another file at the address really means it is gone;
 * otherwise the pass waits for that listing (a rename on the server is seen there).
 */
fun planTarget(entry: EditEntry, preflight: Preflight, decideGone: Boolean): TargetPlan = when {
    // A finished copy written to later, and an edit of an empty file without version: never a version.
    entry.retired || entry.baseEntity.isEmpty() -> TargetPlan.Conflict
    preflight is Preflight.Unclear -> TargetPlan.Later
    preflight is Preflight.NoAccess -> TargetPlan.PrivateConflict
    preflight is Preflight.Found && preflight.id == entry.fileId && !preflight.folder -> TargetPlan.Version
    decideGone -> TargetPlan.Conflict
    else -> TargetPlan.WaitForSync
}

/** Codes that mean the file or folder is not there. */
val GONE_CODES = setOf(ApiException.CODE_PATH_NOT_EXIST, 404, 40044)

/** Codes that clearly refuse access (no right, owner only, share gone, sign-in of a link needed). */
val NO_ACCESS_CODES = setOf(403, 40082, 40058, 40088)

/** The answer of a lookup as a [Preflight]; [code] null = no answer at all. */
fun preflightOf(found: FileObject?, code: Int?): Preflight = when {
    code == null && found != null -> Preflight.Found(found.id, found.isFolder, found.primaryEntity)
    code == null -> Preflight.Gone
    code in GONE_CODES -> Preflight.Gone
    code in NO_ACCESS_CODES -> Preflight.NoAccess
    else -> Preflight.Unclear
}

enum class UploadOutcome { Conflict, PrivateConflict, Later, ServerFull, Replan, Error }

/** What a refusal of a version upload means; network trouble is [UploadOutcome.Later] at the caller. */
fun classifyRefusal(code: Int): UploadOutcome = when {
    // Stale version, file gone, a folder took its place.
    code == 40076 || code == 40904 || code == ApiException.CODE_OBJECT_EXISTS -> UploadOutcome.Conflict
    code in NO_ACCESS_CODES -> UploadOutcome.PrivateConflict
    code == ApiException.CODE_LOCK_CONFLICT -> UploadOutcome.Later
    code == 40051 -> UploadOutcome.ServerFull
    code == 409 -> UploadOutcome.Replan
    code in 500..599 || code == 429 || code == ApiException.CODE_LOGIN_REQUIRED || code >= 50000 ||
        code == ApiException.CODE_UPLOAD_SESSION_EXPIRED -> UploadOutcome.Later
    else -> UploadOutcome.Error
}

/** A conflict copy's name is taken (also in another spelling): try the next counter. */
fun isNameTaken(code: Int): Boolean = code == ApiException.CODE_OBJECT_EXISTS || code == 40905

enum class CancelOutcome {
    /** The server answered the cancel clearly: now the file tells whether the upload got in. */
    Check,
    /** A clear refusal: the snapshot goes up as a private conflict copy. */
    PrivateConflict,
    /** No clear answer: the marker stays and is tried again later. */
    Keep,
}

fun classifyCancel(code: Int?): CancelOutcome = when (code) {
    0 -> CancelOutcome.Check
    in NO_ACCESS_CODES -> CancelOutcome.PrivateConflict
    else -> CancelOutcome.Keep
}

/**
 * An upload counts only when it is the file's current version: the file's version list also
 * holds the entity of an upload that never finished, so being listed proves nothing.
 */
fun isCurrentVersion(file: FileObject, entityId: String): Boolean = entityId.isNotEmpty() && file.primaryEntity == entityId


/** A confirmed version upload: the working copy is based on it now. */
fun confirmVersion(entry: EditEntry, marker: EditMarker): EditEntry = settled(entry, marker).copy(baseEntity = marker.entityId)

/**
 * A confirmed conflict copy: the working copy belongs to that new file from now on (its id,
 * address and our version; the local path stays). Further edits become versions of the copy.
 */
fun rebaseOnto(entry: EditEntry, copy: FileObject, marker: EditMarker): EditEntry =
    settled(entry, marker).copy(fileId = copy.id, uri = copy.path, baseEntity = marker.entityId, conflict = marker.name)

private fun settled(entry: EditEntry, marker: EditMarker) = entry.copy(
    confirmed = marker.snapshot, pending = null, state = EditState.Synced, problem = null, errorCode = 0,
    outdated = false, stalls = 0, attempts = 0,
)

/**
 * Bytes of this entry are not confirmed on the server: a kept snapshot (pending or refused), or a
 * working copy ([sha256], null when missing) that differs from the last confirmed state.
 */
fun isUnsent(entry: EditEntry, sha256: String?): Boolean =
    entry.pending != null || entry.refusedSnapshots.isNotEmpty() || (sha256 != null && sha256 != entry.reference.sha256)

/** Nothing of the working copy waits for an upload: it can be finished (or the account signed out). */
fun isSettled(entry: EditEntry, sha256: String?): Boolean = sha256 != null && !isUnsent(entry, sha256)

/**
 * The working copies that must be uploaded or discarded before a sign-out: in use and not
 * settled, or holding a kept snapshot not confirmed on the server (also when the working copy is
 * missing). A missing copy without one holds nothing to save; finished copies stay for the same account.
 */
fun signOutBlockers(entries: List<Pair<EditEntry, String?>>): List<EditEntry> =
    entries.filter { (entry, sha256) -> entry.active && isUnsent(entry, sha256) }.map { it.first }

/** The server's code of the latest refusal of an entry (pending or kept aside), 0 when none. */
fun refusalCode(entry: EditEntry): Int =
    entry.errorCode.takeIf { entry.problem == EditProblem.Refused && it > 0 } ?: entry.refusedSnapshots.lastOrNull()?.refusedCode ?: 0

/**
 * A pending upload the server clearly refused (a code no conflict copy avoids) that has nothing
 * sent yet: once the working copy holds other bytes, it steps aside ([EditEntry.refusedSnapshots]) so the
 * newer state is planned normally.
 */
fun canPark(entry: EditEntry, sha256: String?): Boolean {
    val pending = entry.pending ?: return false
    return entry.problem == EditProblem.Refused && pending.sessionId.isEmpty() && sha256 != null && sha256 != pending.snapshot.sha256
}

/**
 * Unchanged since its last confirmed state while the server has another version: opening shows
 * the synced copy, "Bearbeiten" makes a fresh working copy.
 */
fun isOutdated(entry: EditEntry, changed: Boolean, server: Preflight): Boolean =
    !changed && entry.pending == null && entry.refusedSnapshots.isEmpty() && !entry.retired && server is Preflight.Found &&
        server.id == entry.fileId && server.primaryEntity.isNotEmpty() && server.primaryEntity != entry.baseEntity

/**
 * Listed on the Offline screen: something to look at (an error or a stalled upload, a conflict
 * copy that was made, a late change of a finished copy waiting for its upload).
 */
fun needsAttention(entry: EditEntry): Boolean = entry.state != EditState.Creating && (
    entry.state == EditState.Error || entry.problem != null || entry.refusedSnapshots.isNotEmpty() ||
        (!entry.retired && entry.conflict.isNotEmpty()) || (entry.retired && entry.state == EditState.Uploading)
    )

/** Waits before an unresolved upload is cleared up again: 3 min, 15 min, 1 h, then only periodic runs. */
fun retryDelay(attempts: Int): Long? = listOf(3L, 15L, 60L).getOrNull(attempts)?.let { it * 60_000L }

/** Cheap check at app start (no hashing): the edit check should run soon. */
fun needsCheck(entry: EditEntry, stat: LocalStat?): Boolean {
    if (entry.state == EditState.Creating || entry.retired) return false
    if (entry.pending != null || entry.state == EditState.Uploading) return true
    val ref = entry.reference
    return stat != null && (stat.size != ref.size || stat.mtime != ref.mtime)
}

/**
 * `<base> (<label> <time>)<.ext>`, with ` 2`, ` 3` after the time for further tries; the base is
 * shortened so the name fits [NAME_MAX_BYTES] bytes of UTF-8. A dot file (".bashrc") has no
 * extension; an extension too long to keep goes into the base.
 */
fun conflictName(name: String, label: String, time: String, counter: Int = 1): String {
    val dot = name.lastIndexOf('.')
    var base = if (dot > 0) name.substring(0, dot) else name
    var ext = if (dot > 0) name.substring(dot) else ""
    val suffix = " ($label $time${if (counter > 1) " $counter" else ""})"
    if (utf8Size(suffix + ext) >= NAME_MAX_BYTES) {
        base = name
        ext = ""
    }
    val room = NAME_MAX_BYTES - utf8Size(suffix) - utf8Size(ext)
    return truncateUtf8(base, room.coerceAtLeast(0)) + suffix + ext
}

private fun utf8Size(text: String) = text.toByteArray(Charsets.UTF_8).size

/** The longest start of [text] (whole characters) that fits [maxBytes] bytes of UTF-8. */
internal fun truncateUtf8(text: String, maxBytes: Int): String {
    var bytes = 0
    var end = 0
    while (end < text.length) {
        val cp = text.codePointAt(end)
        val size = String(Character.toChars(cp)).toByteArray(Charsets.UTF_8).size
        if (bytes + size > maxBytes) break
        bytes += size
        end += Character.charCount(cp)
    }
    return text.substring(0, end)
}

/** The folder a conflict copy goes into: the file's folder, or My files for a private one. */
fun conflictFolder(entry: EditEntry, private: Boolean): String = if (private) CrUri.MY_PREFIX else CrUri.parent(entry.uri)
