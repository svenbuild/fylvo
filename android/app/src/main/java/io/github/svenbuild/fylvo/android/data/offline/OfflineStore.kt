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

    /** The local copy of [relativePath] in root [id]; the path was checked by [relativePath]. */
    fun fileOf(id: String, relativePath: String): File = File(dirOf(id), relativePath)

    companion object {
        const val FILES_DIR = "offline/files"
        const val INDEX_DIR = "offline-index"
        const val TMP_DIR = "offline-tmp"

        private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

        fun newId(): String = UUID.randomUUID().toString()

        private fun isValidId(id: String) = runCatching { UUID.fromString(id).toString() == id }.getOrDefault(false)

        private fun read(file: File): OfflineIndex? =
            runCatching { json.decodeFromString(OfflineIndex.serializer(), file.readText()) }.getOrNull()

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
