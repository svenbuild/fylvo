package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.FileRepository
import io.github.svenbuild.fylvo.android.util.CrUri
import java.io.File
import java.io.IOException
import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * Fylvo: one sync run over all offline roots of the signed-in account (server to phone only in
 * this version). Every step can be stopped at any point: copies are fetched into offline-tmp and
 * renamed into place, the index records only finished copies, and the next run goes on from there.
 * The caller holds [OfflineManager.engineLock].
 */
class OfflineSyncEngine(private val repo: FileRepository, private val manager: OfflineManager) {

    private val storageFull = AtomicBoolean(false)
    private val space = SpaceReservation()

    private class RootGone : Exception()

    suspend fun run() = withContext(Dispatchers.IO) {
        // A "Sync now" from before this run is covered by it.
        manager.takeSyncRequest()
        val store = manager.store() ?: return@withContext
        manager.prepareAccount(store)
        manager.migrateLegacy(store)
        // Leftovers of a stopped run (tmp) or an interrupted "Remove" (copies without an index).
        manager.cleanUp(store)
        val done = HashSet<String>()
        try {
            // Roots switched on during the run are taken too.
            while (!storageFull.get()) {
                val next = store.indexes().filter { it.root.id !in done && !manager.isRemoved(it.root.id) }
                if (next.isEmpty()) {
                    // "Sync now" while this run was busy: every root once more.
                    if (!manager.takeSyncRequest()) break
                    done.clear()
                    continue
                }
                for (index in next) {
                    done += index.root.id
                    if (storageFull.get()) break
                    syncRoot(store, index)
                }
            }
            dropCoveredRoots(store)
        } finally {
            manager.setSyncing(null)
        }
    }

    private suspend fun syncRoot(store: OfflineStore, initial: OfflineIndex) {
        val root = initial.root
        val id = root.id
        manager.setSyncing(id)
        val writer = IndexWriter(store, initial, canWrite = { !manager.isRemoved(id) }, onSaved = manager::publish)
        try {
            syncRoot(store, writer, root)
        } catch (e: CancellationException) {
            // Stopped (time limit, constraints, sign-out): keep what is done for the next run.
            withContext(NonCancellable) { manager.writing(id) { writer.flush() } }
            throw e
        }
    }

    private suspend fun syncRoot(store: OfflineStore, writer: IndexWriter, root: OfflineRoot) {
        val id = root.id
        val remote = try {
            listing(root)
        } catch (e: CancellationException) {
            throw e
        } catch (_: RootGone) {
            // Renamed, moved or deleted on the server: the copies stay until it is switched off.
            finish(writer, id, OfflineState.Gone)
            return
        } catch (_: Exception) {
            finish(writer, id, OfflineState.Error)
            return
        }

        // Files without an index entry (a run stopped after writing one) are read-only copies
        // nobody points to; they could block a path the server now uses differently. Anything
        // needed is fetched again below.
        manager.writing(id) { store.removeUnindexed(id, writer.current.entries.mapTo(HashSet()) { it.path }) } ?: return

        val actions = planSync(remote, writer.current.entries) { rel ->
            store.fileOf(id, rel).takeIf { it.isFile }?.let { LocalStat(it.length(), it.lastModified()) }
        }
        var failed = false

        for (delete in actions.filterIsInstance<SyncAction.Delete>()) {
            if (manager.isRemoved(id)) return
            // The confirm path is the file itself only for a lone file whose folders still exist.
            val single = delete.paths.singleOrNull()?.takeIf { it == delete.confirm }
            val gone = try {
                val found = repo.lookup(CrUri.join(root.uri, delete.confirm))
                confirmsGone(found, expectFolder = single == null, expectedId = single?.let { writer.entry(it)?.id })
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                failed = true
                false
            }
            // Still there or no clear answer: keep the copies.
            if (!gone) continue
            manager.writing(id) {
                delete.paths.forEach { path ->
                    val file = store.fileOf(id, path)
                    file.delete()
                    writer.remove(path)
                    pruneEmptyDirs(store.dirOf(id), file.parentFile)
                }
            }
        }

        // Moves after deletions: a moved file may go where a deleted one was (a file `a` replaced
        // by a folder `a/` holding a moved file). A move that does not work becomes a download.
        val downloads = actions.filterIsInstance<SyncAction.Download>().map { it.file }.toMutableList()
        actions.filterIsInstance<SyncAction.Move>().forEach { move ->
            val moved = manager.writing(id) { moveCopy(store, writer, id, move) } ?: return
            if (!moved && downloads.none { it.path == move.file.path }) downloads += move.file
        }

        val failures = AtomicInteger(0)
        val gate = Semaphore(PARALLEL_DOWNLOADS)
        coroutineScope {
            downloads.map { file -> SyncAction.Download(file) }.map { download ->
                async {
                    gate.withPermit {
                        if (storageFull.get() || manager.isRemoved(id)) return@withPermit
                        if (!download(store, writer, id, download.file)) failures.incrementAndGet()
                    }
                }
            }.awaitAll()
        }
        if (failures.get() > 0) failed = true

        manager.writing(id) {
            if (remote.complete) writer.setFolders(remote.folders.sorted())
            else writer.setFolders((writer.current.folders + remote.folders).distinct().sorted())
        }
        val state = when {
            storageFull.get() -> OfflineState.StorageFull
            failed || !remote.complete -> OfflineState.Error
            else -> OfflineState.Synced
        }
        finish(writer, id, state)
    }

    /**
     * Moves the copy of [move] through offline-tmp, so the old file never blocks its new place
     * (a file `a` that moved to `a/photo.jpg`). Runs under the write lock; false when it failed.
     */
    private fun moveCopy(store: OfflineStore, writer: IndexWriter, id: String, move: SyncAction.Move): Boolean {
        val old = writer.entry(move.from) ?: return false
        val src = store.fileOf(id, move.from)
        val dst = store.fileOf(id, move.file.path)
        val tmp = File(store.tmpDir, "$id-move-${UUID.randomUUID()}")
        store.tmpDir.mkdirs()
        if (!src.renameTo(tmp)) return false
        writer.remove(move.from)
        pruneEmptyDirs(store.dirOf(id), src.parentFile)
        dst.parentFile?.mkdirs()
        if (!dst.exists() && tmp.renameTo(dst)) {
            writer.put(old.copy(id = move.file.id, path = move.file.path))
            return true
        }
        // Something is in the way: the old copy goes back (so a failed download never loses it)
        // and the file is fetched at its new place instead.
        src.parentFile?.mkdirs()
        if (tmp.renameTo(src)) writer.put(old) else tmp.delete()
        return false
    }

    /** The server's view of [root]; throws [RootGone] when the server clearly has nothing there. */
    private suspend fun listing(root: OfflineRoot): RemoteListing {
        // The top of a shared folder has no file info (the server refuses it); its listing tells.
        val shareTop = root.folder && CrUri.isShare(root.uri) && CrUri.isRoot(root.uri)
        if (!shareTop) {
            val obj = repo.lookup(root.uri) ?: throw RootGone()
            if (obj.isFolder != root.folder) throw RootGone()
            if (!root.folder) {
                val name = CrUri.fileName(root.uri)
                val rel = relativePath("x", "x/$name") ?: throw IOException("unusable file name")
                return RemoteListing(listOf(RemoteFile(obj.id, rel, obj.primaryEntity, obj.size, root.uri)), emptyList(), complete = true)
            }
        }
        val tree = try {
            repo.listTree(root.uri)
        } catch (e: ApiException) {
            if (shareTop && e.code in GONE_CODES) throw RootGone()
            throw e
        }
        val rootCanonical = CrUri.canonical(root.uri)
        val files = tree.files.filter { !it.isUploading }.mapNotNull { f ->
            relativePath(rootCanonical, CrUri.canonical(f.path))?.let { RemoteFile(f.id, it, f.primaryEntity, f.size, f.path) }
        }
        val folders = tree.folders.mapNotNull { relativePath(rootCanonical, CrUri.canonical(it.path)) }
        return RemoteListing(files, folders, tree.complete)
    }

    /** Fetches [file] into offline-tmp and renames it into place; false on any trouble. */
    private suspend fun download(store: OfflineStore, writer: IndexWriter, id: String, file: RemoteFile): Boolean {
        if (!space.tryReserve(file.size, manager.freeBytes())) {
            storageFull.set(true)
            return false
        }
        val tmp = File(store.tmpDir, "$id-${UUID.randomUUID()}")
        return try {
            if (file.size == 0L) {
                // An empty file may have no version to ask for: nothing to fetch.
                tmp.parentFile?.mkdirs()
                tmp.createNewFile()
            } else {
                // The exact version of the listing, not whatever is current by now.
                val url = repo.downloadUrl(file.uri, entity = file.entity)
                repo.downloadToFile(url, tmp)
            }
            if (tmp.length() != file.size) throw IOException("size differs from the listing")
            manager.writing(id) {
                val dest = store.fileOf(id, file.path)
                // A folder left at this path with no indexed copy inside (the server turned the
                // folder into a file) gives way.
                if (dest.isDirectory && writer.current.entries.none { it.path.startsWith(file.path + "/") }) {
                    dest.deleteRecursively()
                }
                // Follow-up: this writes the whole index once per replaced file, which costs a lot
                // for very large folders; a small journal of pending entries would avoid it.
                // Replacing a copy: first record on disk that this path is being replaced, so a stop
                // between the rename and the next index write never leaves new bytes under the old
                // version (a pending entry counts as no copy and is fetched again).
                if (dest.exists() || (writer.entry(file.path)?.localSize ?: -1) >= 0) {
                    writer.put(pendingEntry(file))
                    writer.flush()
                }
                dest.parentFile?.mkdirs()
                if (!tmp.renameTo(dest)) throw IOException("could not move the copy into place")
                writer.put(
                    OfflineEntry(
                        id = file.id, path = file.path, entity = file.entity, size = file.size,
                        localSize = dest.length(), localMtime = dest.lastModified(), state = EntryState.Synced,
                    )
                )
            }
            true
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // Network trouble, a non-server answer, a full disk: try again next run.
            false
        } finally {
            tmp.delete()
            space.release(file.size)
        }
    }

    private suspend fun finish(writer: IndexWriter, id: String, state: OfflineState) {
        manager.writing(id) {
            val legacy = writer.current.root.legacyFile
            writer.setRoot {
                it.copy(state = state, lastSyncAt = if (state == OfflineState.Synced) System.currentTimeMillis() else it.lastSyncAt)
            }
            // The new copy is recorded on disk first; only then does the older app version's copy go.
            writer.flush()
            if (legacy != null && state == OfflineState.Synced) {
                manager.deleteLegacyCopy(legacy)
                writer.setRoot { it.copy(legacyFile = null) }
                writer.flush()
            }
        }
    }

    /**
     * A root inside an up-to-date offline folder goes once that folder holds copies of all its
     * files ([isCoveredBy]); its own copies are never deleted just because the folder was pinned.
     */
    private suspend fun dropCoveredRoots(store: OfflineStore) {
        val indexes = store.indexes().filter { !manager.isRemoved(it.root.id) }
        val synced = indexes.filter { it.root.folder && it.root.state == OfflineState.Synced }
        // Checked again under the lock right before each removal (the parent may go meanwhile).
        indexes.filter { inner -> synced.any { outer -> isCoveredBy(inner, outer) } }
            .forEach { manager.removeIfCovered(store, it.root.id) }
    }

    /** Removes empty folders from [dir] up to (not including) [rootDir]. */
    private fun pruneEmptyDirs(rootDir: File, dir: File?) {
        var current = dir
        while (current != null && current != rootDir && current.path.startsWith(rootDir.path)) {
            if (current.list()?.isEmpty() != true || !current.delete()) return
            current = current.parentFile
        }
    }

    private companion object {
        const val PARALLEL_DOWNLOADS = 2
        /** Answers on a shared folder's listing that mean it is gone. */
        val GONE_CODES = setOf(ApiException.CODE_PATH_NOT_EXIST, 404)
    }
}
