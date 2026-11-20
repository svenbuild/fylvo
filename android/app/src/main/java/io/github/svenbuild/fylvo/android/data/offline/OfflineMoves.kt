package io.github.svenbuild.fylvo.android.data.offline

import java.io.File

private class Parked(val move: SyncAction.Move, val old: OfflineEntry, val tmp: File, var created: List<File> = emptyList())

/**
 * Moves the copies of [moves] through offline-tmp in two phases: first every source is parked
 * (named after its old path, so a stop can put it back, see recoverMoves), then each goes to
 * its new place. So no old file blocks another's new place (`a` -> `a/photo.jpg`, or `a` and
 * `b` moving into each other's names). Runs under the write lock; returns the moves that
 * failed (fetched at their new place instead, the old copy is kept).
 */
internal fun moveCopies(store: OfflineStore, writer: IndexWriter, id: String, moves: List<SyncAction.Move>): List<SyncAction.Move> {
    if (moves.isEmpty()) return emptyList()
    val failed = ArrayList<SyncAction.Move>()
    // Phase 1: park every source. The index still points to the old paths.
    val parked = ArrayList<Parked>()
    for (move in moves) {
        val old = writer.entry(move.from)
        val tmp = store.moveTmpOf(id, move.from)
        tmp.parentFile?.mkdirs()
        tmp.delete()
        if (old == null || !store.fileOf(id, move.from).renameTo(tmp)) failed += move else parked += Parked(move, old, tmp)
    }
    parked.forEach { pruneEmptyDirs(store.dirOf(id), store.fileOf(id, it.move.from).parentFile) }
    // Phase 2: each to its new place.
    val notInstalled = ArrayList<Parked>()
    for (p in parked) {
        val dst = store.fileOf(id, p.move.file.path)
        p.created = missingDirs(store.dirOf(id), dst.parentFile)
        dst.parentFile?.mkdirs()
        if (!dst.exists() && p.tmp.renameTo(dst)) {
            // The old path may hold another moved file's new entry by now.
            if (writer.entry(p.move.from) == p.old) writer.remove(p.move.from)
            writer.put(p.old.copy(id = p.move.file.id, path = p.move.file.path))
        } else {
            notInstalled += p
        }
    }
    // Then the ones that did not fit: folders made for them go, the copy goes back to its old
    // place when that is free (its entry still points there), else it stays parked for the
    // next run, or goes when another file took its old place.
    for (p in notInstalled) {
        failed += p.move
        p.created.asReversed().forEach { if (it.list()?.isEmpty() == true) it.delete() }
        val src = store.fileOf(id, p.move.from)
        if (writer.entry(p.move.from) != p.old) {
            p.tmp.delete()
            continue
        }
        src.parentFile?.mkdirs()
        if (!src.exists()) p.tmp.renameTo(src)
    }
    return failed
}

/** Folders from [rootDir] down to [dir] that do not exist yet, outermost first. */
private fun missingDirs(rootDir: File, dir: File?): List<File> {
    val out = ArrayList<File>()
    var current = dir
    while (current != null && current != rootDir && current.path.startsWith(rootDir.path) && !current.exists()) {
        out += current
        current = current.parentFile
    }
    return out.asReversed()
}

/** Removes empty folders from [dir] up to (not including) [rootDir]. */
internal fun pruneEmptyDirs(rootDir: File, dir: File?) {
    var current = dir
    while (current != null && current != rootDir && current.path.startsWith(rootDir.path)) {
        if (current.list()?.isEmpty() != true || !current.delete()) return
        current = current.parentFile
    }
}
