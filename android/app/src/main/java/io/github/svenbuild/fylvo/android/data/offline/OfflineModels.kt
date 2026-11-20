package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.serialization.Serializable
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.util.CrUri
import java.security.MessageDigest

/**
 * Fylvo: a folder or a single file kept on this phone ("offline root"), with its index of the
 * synced copies. One JSON file per root (see [OfflineStore]); unknown fields are ignored on
 * reading, so later versions can add fields (for example working copies) without a migration.
 */
@Serializable
data class OfflineIndex(
    val root: OfflineRoot,
    val entries: List<OfflineEntry> = emptyList(),
    /** Relative paths of every folder in the subtree, so empty folders can be browsed offline too. */
    val folders: List<String> = emptyList(),
)

@Serializable
data class OfflineRoot(
    /** Local id, also the name of its folder on the phone. A new id each time it is switched on. */
    val id: String,
    /** The server address of the folder or file. */
    val uri: String,
    val name: String,
    val folder: Boolean,
    val state: OfflineState = OfflineState.Pending,
    /** When the last run that left everything up to date ended; 0 = never. */
    val lastSyncAt: Long = 0,
    val createdAt: Long = 0,
    /** Copy made by app versions before 1.2 (single offline files); deleted once the new copy exists. */
    val legacyFile: String? = null,
)

@Serializable
enum class OfflineState { Pending, Synced, Error, Gone, StorageFull }

@Serializable
data class OfflineEntry(
    /** The file's id on the server; a known id at a new path is a move. */
    val id: String,
    /** Path below the root, decoded, "/"-separated. */
    val path: String,
    /** The version (entity) the local copy holds; empty = unknown, always verified by a download. */
    val entity: String = "",
    /** Size on the server. */
    val size: Long = 0,
    /** Size and modification time of the local copy when it was written; -1 = no copy yet. */
    val localSize: Long = -1,
    val localMtime: Long = 0,
    val state: EntryState = EntryState.Synced,
)

@Serializable
enum class EntryState { Pending, Synced }

/** A file of the server listing of a root, with its path below the root. */
data class RemoteFile(val id: String, val path: String, val entity: String, val size: Long, val uri: String)

/**
 * The server's view of a root. [complete] is false when the listing stopped early; then nothing
 * is deleted on the phone.
 */
data class RemoteListing(val files: List<RemoteFile>, val folders: List<String>, val complete: Boolean)

data class LocalStat(val size: Long, val mtime: Long)

sealed class SyncAction {
    /** Fetch the file (new, changed, unknown version, or the local copy is missing). */
    data class Download(val file: RemoteFile) : SyncAction()

    /** Same server file at a new path: move the local copy from [from]. */
    data class Move(val from: String, val file: RemoteFile) : SyncAction()

    /**
     * The files at [paths] are gone from a complete listing. They are deleted only after one
     * lookup of [confirm] (the topmost missing folder or file) says it is gone.
     */
    data class Delete(val confirm: String, val paths: List<String>) : SyncAction()
}

/**
 * What one sync run has to do for a root. [remote] null means the listing failed or the root is
 * gone: nothing happens, above all no deletion.
 */
fun planSync(
    remote: RemoteListing?,
    index: List<OfflineEntry>,
    localStat: (String) -> LocalStat?,
): List<SyncAction> {
    if (remote == null) return emptyList()
    val byPath = index.associateBy { it.path }
    val remotePaths = remote.files.mapTo(HashSet()) { it.path }
    // Entries whose path is no longer on the server, by server id: candidates for a move.
    val movable = index.filter { it.path !in remotePaths && it.id.isNotEmpty() }.associateBy { it.id }
    val moved = HashSet<String>()
    val actions = ArrayList<SyncAction>()
    for (file in remote.files) {
        val entry = byPath[file.path]
        if (entry == null) {
            val old = movable[file.id]?.takeIf { it.path !in moved && localStat(it.path) != null }
            if (old != null) {
                moved += old.path
                actions += SyncAction.Move(old.path, file)
                if (!isCurrent(old, file, localStat(old.path))) actions += SyncAction.Download(file)
            } else {
                // Also when a file is already there without an entry (a run stopped before the
                // index was written): it is fetched again, never trusted.
                actions += SyncAction.Download(file)
            }
        } else if (!isCurrent(entry, file, localStat(file.path))) {
            actions += SyncAction.Download(file)
        }
    }
    if (remote.complete) {
        val remoteFolders = HashSet(remote.folders)
        remote.files.forEach { f -> parentsOf(f.path).forEach { remoteFolders += it } }
        index.filter { it.path !in remotePaths && it.path !in moved }
            .groupBy { topmostMissing(it.path, remoteFolders) }
            .forEach { (confirm, entries) -> actions += SyncAction.Delete(confirm, entries.map { it.path }) }
    }
    return actions
}

/**
 * The local copy holds the server's current version: known, same entity, copy present and whole.
 * An empty file may have no entity at all; then the empty copy is enough.
 */
private fun isCurrent(entry: OfflineEntry, file: RemoteFile, stat: LocalStat?): Boolean =
    entry.entity == file.entity && (entry.entity.isNotEmpty() || file.size == 0L) && entry.localSize >= 0 &&
        stat != null && stat.size == entry.localSize

/** "a/b/c.txt" -> ["a", "a/b"]. */
internal fun parentsOf(path: String): List<String> {
    val parts = path.split('/')
    return (1 until parts.size).map { parts.take(it).joinToString("/") }
}

/** The shortest prefix of [path] the server no longer has: one lookup confirms the whole group. */
internal fun topmostMissing(path: String, remoteFolders: Set<String>): String =
    parentsOf(path).firstOrNull { it !in remoteFolders } ?: path

/**
 * Path of [canonicalPath] below [canonicalRoot] (both from CrUri.canonical), or null when it is
 * not below it or has a segment that must not become a local path.
 */
fun relativePath(canonicalRoot: String, canonicalPath: String): String? {
    val prefix = canonicalRoot.trimEnd('/') + "/"
    if (!canonicalPath.startsWith(prefix)) return null
    val rel = canonicalPath.removePrefix(prefix).trim('/')
    val parts = rel.split('/')
    if (rel.isEmpty() || parts.any { it.isEmpty() || it == "." || it == ".." || it.contains('\u0000') }) return null
    return rel
}

/**
 * After a complete listing missed something, the lookup of its topmost missing path says gone:
 * nothing there, or something of the other type (a file became a folder or the other way round),
 * or for a single file another file (other id) under the same name.
 */
fun confirmsGone(found: FileObject?, expectFolder: Boolean, expectedId: String?): Boolean =
    found == null || found.isFolder != expectFolder ||
        (!expectFolder && !expectedId.isNullOrEmpty() && found.id != expectedId)

/**
 * A root inside the offline folder [outer] can go only when [outer]'s index holds a local copy of
 * each of its files (same id and version where known). A root no longer on the server stays.
 */
fun isCoveredBy(inner: OfflineIndex, outer: OfflineIndex): Boolean {
    if (!outer.root.folder || inner.root.id == outer.root.id || inner.root.state == OfflineState.Gone) return false
    val rel = relativePath(CrUri.canonical(outer.root.uri), CrUri.canonical(inner.root.uri)) ?: return false
    if (!inner.root.folder && inner.entries.isEmpty()) return false
    val outerByPath = outer.entries.associateBy { it.path }
    return inner.entries.all { entry ->
        val path = if (inner.root.folder) "$rel/${entry.path}" else rel
        val copy = outerByPath[path] ?: return@all false
        copy.localSize >= 0 &&
            (entry.id.isEmpty() || entry.id == copy.id) &&
            (entry.entity.isEmpty() || entry.entity == copy.entity)
    }
}

/**
 * Space promised to downloads that are still running, so two at once cannot together eat into
 * the reserve: free - reserved - this file must stay at least [reserve].
 */
class SpaceReservation(private val reserve: Long = OFFLINE_RESERVE_BYTES) {
    private var reserved = 0L

    @Synchronized
    fun tryReserve(size: Long, freeBytes: Long): Boolean {
        if (!hasRoomFor(size + reserved, freeBytes, reserve)) return false
        reserved += size
        return true
    }

    @Synchronized
    fun release(size: Long) {
        reserved -= size
    }
}

/** Bytes that must stay free on the phone after a download. */
const val OFFLINE_RESERVE_BYTES = 1024L * 1024 * 1024

/** True when [size] more bytes still leave the reserve free. */
fun hasRoomFor(size: Long, freeBytes: Long, reserve: Long = OFFLINE_RESERVE_BYTES): Boolean =
    freeBytes - size >= reserve

/**
 * The folder name of an account's offline data: a hash of the server and the user id (or the
 * email when the session has no id), so data of another account is never shown.
 */
fun accountKey(serverUrl: String, userId: String, email: String): String {
    val who = if (userId.isNotEmpty()) "id:$userId" else "email:${email.trim().lowercase()}"
    val digest = MessageDigest.getInstance("SHA-256").digest("${serverUrl.trim().trimEnd('/')}\n$who".toByteArray())
    return digest.joinToString("") { "%02x".format(it) }.take(24)
}
