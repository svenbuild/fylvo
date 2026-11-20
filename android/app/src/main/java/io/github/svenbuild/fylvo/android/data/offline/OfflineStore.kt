package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.serialization.json.Json
import io.github.svenbuild.fylvo.android.data.FavoriteEntry
import io.github.svenbuild.fylvo.android.util.CrUri
import java.io.File
import java.util.UUID

/**
 * Fylvo: the offline data of one account on disk, below [base] (the app's filesDir):
 * - `offline/files/<account>/<root id>/<relative path>`: the synced copies (read-only for other apps),
 * - `offline-index/<account>/<root id>.json`: one index per root, written to a temporary file first,
 * - `offline-tmp/`: `.part` downloads, never inside the copies.
 */
class OfflineStore(private val base: File, val accountKey: String) {

    private val indexDir = File(base, "$INDEX_DIR/$accountKey")
    private val filesRoot = File(base, "$FILES_DIR/$accountKey")
    val tmpDir = File(base, TMP_DIR)

    fun indexes(): List<OfflineIndex> =
        indexDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty()
            .mapNotNull { read(it) }
            .sortedBy { it.root.createdAt }

    fun load(id: String): OfflineIndex? = if (isValidId(id)) read(File(indexDir, "$id.json")) else null

    fun save(index: OfflineIndex) {
        require(isValidId(index.root.id)) { "bad root id" }
        indexDir.mkdirs()
        val target = File(indexDir, "${index.root.id}.json")
        val tmp = File(indexDir, "${index.root.id}.json.tmp")
        tmp.writeText(json.encodeToString(OfflineIndex.serializer(), index))
        if (!tmp.renameTo(target)) {
            target.delete()
            check(tmp.renameTo(target)) { "could not store the offline index" }
        }
    }

    /** Removes a root: its index first (it is no longer offered), then its copies. */
    fun delete(id: String) {
        if (!isValidId(id)) return
        File(indexDir, "$id.json").delete()
        dirOf(id).deleteRecursively()
    }

    fun dirOf(id: String): File = File(filesRoot, id)

    /**
     * Deletes everything in root [id]'s copy folder that is not one of [indexed] (relative paths of
     * its index entries, pending ones included), then the folders left empty. Returns what was deleted.
     */
    fun removeUnindexed(id: String, indexed: Set<String>): List<String> {
        val dir = dirOf(id)
        if (!dir.isDirectory) return emptyList()
        val keepDirs = indexed.flatMapTo(HashSet()) { parentsOf(it) }
        val deleted = ArrayList<String>()
        fun walk(folder: File, prefix: String) {
            folder.listFiles().orEmpty().forEach { child ->
                val rel = prefix + child.name
                when {
                    child.isDirectory && rel in keepDirs -> walk(child, "$rel/")
                    child.isFile && rel in indexed -> Unit
                    else -> if (child.deleteRecursively()) deleted += rel
                }
            }
        }
        walk(dir, "")
        return deleted
    }

    /** Root [id]'s part of offline-tmp. Cleaned only after a complete listing of the root ([clearTmp]). */
    fun tmpOf(id: String): File = File(tmpDir, id)

    /** Where a move parks the copy of [relativePath]; named after the old path so it can be put back. */
    fun moveTmpOf(id: String, relativePath: String): File = File(tmpOf(id), "move/" + sha256(relativePath))

    /** A new file for one download into root [id]. */
    fun downloadTmpOf(id: String): File = File(tmpOf(id), "dl-" + UUID.randomUUID())

    fun clearTmp(id: String) {
        tmpOf(id).deleteRecursively()
    }

    /**
     * A move stopped after parking a copy: when an entry's copy is missing and its parked copy is
     * there, it goes back to its place. Returns the paths put back.
     */
    fun recoverMoves(id: String, entries: List<OfflineEntry>): List<String> = entries.filter { entry ->
        val src = fileOf(id, entry.path)
        val parked = moveTmpOf(id, entry.path)
        if (entry.localSize < 0 || src.exists() || !parked.isFile) return@filter false
        src.parentFile?.mkdirs()
        parked.renameTo(src)
    }.map { it.path }

    /** Parts of offline-tmp that belong to no root of this account (or older loose files). */
    fun orphanTmp(): List<File> {
        val ids = indexDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().mapTo(HashSet()) { it.name.removeSuffix(".json") }
        return tmpDir.listFiles().orEmpty().filter { !(it.isDirectory && it.name in ids) }
    }

    /** Folders of copies with no index (an interrupted "Remove"): nothing points to them any more. */
    fun orphanDirs(): List<File> {
        val ids = indexDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().mapTo(HashSet()) { it.name.removeSuffix(".json") }
        return filesRoot.listFiles().orEmpty().filter { it.name !in ids }
    }

    /** The local copy of [relativePath] in root [id]; the path was checked by [relativePath]. */
    fun fileOf(id: String, relativePath: String): File = File(dirOf(id), relativePath)

    companion object {
        const val FILES_DIR = "offline/files"
        const val INDEX_DIR = "offline-index"
        const val TMP_DIR = "offline-tmp"

        private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

        fun newId(): String = UUID.randomUUID().toString()

        private fun sha256(text: String): String =
            java.security.MessageDigest.getInstance("SHA-256").digest(text.toByteArray())
                .joinToString("") { "%02x".format(it) }.take(40)

        private fun isValidId(id: String) = runCatching { UUID.fromString(id).toString() == id }.getOrDefault(false)

        private fun read(file: File): OfflineIndex? =
            runCatching { json.decodeFromString(OfflineIndex.serializer(), file.readText()) }.getOrNull()

        /**
         * Sessions of app versions before 1.2 had no user id, so their data lies under the key made
         * from the email. Once the id is known, that data moves to the id key (when there is none yet).
         */
        fun adoptAccount(base: File, fromKey: String, toKey: String) {
            if (fromKey == toKey) return
            for (dir in listOf(INDEX_DIR, FILES_DIR)) {
                val from = File(base, "$dir/$fromKey")
                val to = File(base, "$dir/$toKey")
                if (from.exists() && !to.exists()) from.renameTo(to)
            }
        }

        /** Offline data of every other account on this phone (read-only copies, nothing to lose). */
        fun deleteOtherAccounts(base: File, keepKey: String) {
            for (dir in listOf(INDEX_DIR, FILES_DIR)) {
                File(base, dir).listFiles().orEmpty().filter { it.name != keepKey }.forEach { it.deleteRecursively() }
            }
        }

        /** Every account's offline data (sign-out). */
        fun deleteAll(base: File) {
            File(base, FILES_DIR).deleteRecursively()
            File(base, INDEX_DIR).deleteRecursively()
            File(base, TMP_DIR).deleteRecursively()
        }
    }
}

/**
 * The index of one root while a sync run changes it. Changes are kept in memory and written in
 * batches: after [batchSize] changes or [intervalMs], and on [flush]. [canWrite] is asked before
 * each write, so a root switched off meanwhile is not written back.
 */
class IndexWriter(
    private val store: OfflineStore,
    initial: OfflineIndex,
    private val canWrite: () -> Boolean = { true },
    private val onSaved: (OfflineIndex) -> Unit = {},
    private val clock: () -> Long = System::currentTimeMillis,
    private val batchSize: Int = 50,
    private val intervalMs: Long = 5_000,
) {
    private var root = initial.root
    private var folders = initial.folders
    private val entries = LinkedHashMap<String, OfflineEntry>().apply { initial.entries.forEach { put(it.path, it) } }
    private var changes = 0
    private var lastWrite = clock()

    val current: OfflineIndex get() = OfflineIndex(root, entries.values.toList(), folders)

    fun entry(path: String): OfflineEntry? = entries[path]

    fun put(entry: OfflineEntry) {
        entries[entry.path] = entry
        changed()
    }

    fun remove(path: String) {
        if (entries.remove(path) != null) changed()
    }

    fun setRoot(change: (OfflineRoot) -> OfflineRoot) {
        root = change(root)
        changed()
    }

    fun setFolders(list: List<String>) {
        folders = list
        changed()
    }

    private fun changed() {
        changes++
        if (changes >= batchSize || clock() - lastWrite >= intervalMs) flush()
    }

    fun flush() {
        if (changes == 0) return
        changes = 0
        lastWrite = clock()
        if (!canWrite()) return
        val index = current
        store.save(index)
        onSaved(index)
    }
}

/**
 * Turns the single offline files of app versions before 1.2 into single-file roots. Idempotent:
 * a file that already has a root is skipped, so a run stopped before the old list was cleared
 * adds nothing twice. The old copy is kept (and used) until the new one exists.
 */
fun migrateFavorites(legacy: List<FavoriteEntry>, store: OfflineStore, now: Long = System.currentTimeMillis()): Int {
    val known = store.indexes().mapTo(HashSet()) { CrUri.canonical(it.root.uri) }
    var added = 0
    for (fav in legacy) {
        if (!known.add(CrUri.canonical(fav.path))) continue
        val name = CrUri.fileName(fav.path)
        store.save(
            OfflineIndex(
                root = OfflineRoot(
                    id = OfflineStore.newId(),
                    uri = fav.path,
                    name = fav.name.ifEmpty { name },
                    folder = false,
                    createdAt = now + added,
                    legacyFile = fav.localFile.ifEmpty { null },
                ),
                // Unknown version: the next sync downloads it.
                entries = listOf(OfflineEntry(id = "", path = name, size = fav.size, state = EntryState.Pending)),
            )
        )
        added++
    }
    return added
}
