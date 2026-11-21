package io.github.svenbuild.fylvo.android.data.offline

import kotlinx.serialization.json.Json
import java.io.File
import java.io.InputStream
import java.security.MessageDigest
import java.util.UUID

/**
 * Fylvo: the working copies of one account on disk, below [base] (the app's filesDir), apart from
 * the synced copies so that nothing of the sync (clean-ups, switching a root off, other accounts,
 * sign-out) ever touches them:
 * - `offline-edit/files/<account>/<id>/<name>`: working copies in use (the only folder other apps
 *   reach, through the FileProvider, which refuses to delete there),
 * - `offline-edit/done/<account>/<id>/<name>`: finished copies, watched for late writes for 7 days,
 * - `offline-edit/index/<account>.json`: the edit index, written to a temporary file first,
 * - `offline-edit/tmp/<account>/`: snapshots for uploads and copies being made.
 */
class EditStore(private val base: File, val accountKey: String) {

    private val indexFile = File(base, "$ROOT/index/$accountKey.json")
    private val filesDir = File(base, "$ROOT/files/$accountKey")
    private val doneDir = File(base, "$ROOT/done/$accountKey")
    private val tmpDir = File(base, "$ROOT/tmp/$accountKey")

    /** The edit index; empty when there is none (or it cannot be read: the copies then count as orphans). */
    fun load(): EditIndex =
        runCatching { json.decodeFromString(EditIndex.serializer(), indexFile.readText()) }.getOrNull() ?: EditIndex()

    fun hasEntries(): Boolean = indexFile.isFile && load().entries.isNotEmpty()

    fun save(index: EditIndex) {
        indexFile.parentFile?.mkdirs()
        val tmp = File(indexFile.path + ".tmp")
        tmp.writeText(json.encodeToString(EditIndex.serializer(), index))
        if (!tmp.renameTo(indexFile)) {
            indexFile.delete()
            check(tmp.renameTo(indexFile)) { "could not store the edit index" }
        }
    }

    fun entry(id: String): EditEntry? = load().entries.firstOrNull { it.id == id }

    /** Writes [entry] (new or changed) into the index at once; returns the index as stored. */
    fun put(entry: EditEntry): EditIndex {
        val entries = load().entries
        val next = if (entries.any { it.id == entry.id }) entries.map { if (it.id == entry.id) entry else it } else entries + entry
        return EditIndex(next).also(::save)
    }

    fun remove(id: String): EditIndex = EditIndex(load().entries.filter { it.id != id }).also(::save)

    /** Where the working copy of [entry] is now: in use, or finished. */
    fun fileOf(entry: EditEntry): File = if (entry.retired) doneFileOf(entry) else activeFileOf(entry)

    fun activeFileOf(entry: EditEntry): File = File(filesDir, "${entry.id}/${entry.name}")

    fun doneFileOf(entry: EditEntry): File = File(doneDir, "${entry.id}/${entry.name}")

    /** The copy of the working copy that an upload sends (never the live file). */
    fun snapshotOf(entry: EditEntry): File = File(tmpDir, "snap-${entry.id}")

    /** Where a new working copy is written before it is renamed into place. */
    fun creatingOf(entry: EditEntry): File = File(tmpDir, "creating-${entry.id}")

    /**
     * Finishing: the folder of the working copy moves to done/ in one rename, so an editor that
     * still has the file open keeps writing into it there. True when it is in done/ now.
     */
    fun moveToDone(entry: EditEntry): Boolean {
        val from = File(filesDir, entry.id)
        val to = File(doneDir, entry.id)
        if (!from.exists()) return to.exists()
        doneDir.mkdirs()
        return from.renameTo(to)
    }

    /** True when the copy of [entry] lies in done/ only (a finish stopped before its index write). */
    fun isOnlyInDone(entry: EditEntry): Boolean = !File(filesDir, entry.id).exists() && File(doneDir, entry.id).exists()

    /** Deletes everything of [entry]: its folders in files/ and done/, its snapshot and half-made copy. */
    fun deleteFiles(entry: EditEntry) {
        File(filesDir, entry.id).deleteRecursively()
        File(doneDir, entry.id).deleteRecursively()
        snapshotOf(entry).delete()
        creatingOf(entry).delete()
    }

    /** Files in files/ or done/ that no entry of [index] points to (an editor wrote after a finish, a lost index). */
    fun orphans(index: EditIndex): List<File> {
        val ids = index.entries.mapTo(HashSet()) { it.id }
        return listOf(filesDir, doneDir).flatMap { dir ->
            dir.listFiles().orEmpty().filter { it.name !in ids }.flatMap { folder ->
                if (folder.isFile) listOf(folder) else folder.walkTopDown().filter { it.isFile }.toList()
            }
        }
    }

    /** Takes [file] (an orphan) into done/ under the new entry [entry]; true when it is there. */
    fun adoptOrphan(file: File, entry: EditEntry): Boolean {
        val dest = doneFileOf(entry)
        dest.parentFile?.mkdirs()
        if (!file.renameTo(dest)) return false
        // The orphan's folder, once empty.
        file.parentFile?.takeIf { it != filesDir && it != doneDir && it.list()?.isEmpty() == true }?.delete()
        return true
    }

    /** Leftovers in tmp/ that no entry needs: snapshots without marker, copies of no entry being made. */
    fun cleanTmp(index: EditIndex) {
        val keep = HashSet<File>()
        index.entries.forEach { entry ->
            if (entry.pending != null) keep += snapshotOf(entry)
            if (entry.state == EditState.Creating) keep += creatingOf(entry)
        }
        tmpDir.listFiles().orEmpty().filter { it !in keep }.forEach { it.deleteRecursively() }
    }

    fun tmpFolder(): File = tmpDir.also { it.mkdirs() }

    companion object {
        const val ROOT = "offline-edit"

        private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

        fun newId(): String = UUID.randomUUID().toString()

        /** A server name as a local file name (the server never sends "/", but the phone must not trust it). */
        fun safeName(name: String): String {
            val clean = name.replace('/', '_').replace('\u0000', '_')
            return if (clean.isEmpty() || clean == "." || clean == "..") "file" else clean
        }

        fun sha256(file: File): String = file.inputStream().use { copyHashing(it, null) }

        /** Copies [input] into [out] (when given) and returns the SHA-256 of what went through. */
        fun copyHashing(input: InputStream, out: java.io.OutputStream?): String {
            val digest = MessageDigest.getInstance("SHA-256")
            val buffer = ByteArray(256 * 1024)
            while (true) {
                val n = input.read(buffer)
                if (n < 0) break
                digest.update(buffer, 0, n)
                out?.write(buffer, 0, n)
            }
            return digest.digest().joinToString("") { "%02x".format(it) }
        }

        /**
         * Sessions from before 1.2 kept their data under a key made from the email; once the user id
         * is known it moves to the id key, item by item and only where the target is free, so a
         * stop half way goes on next time. Nothing is deleted except folders left empty.
         */
        fun adoptAccount(base: File, fromKey: String, toKey: String) {
            if (fromKey == toKey) return
            val from = EditStore(base, fromKey)
            val to = EditStore(base, toKey)
            if (from.indexFile.isFile) {
                val moving = from.load()
                val target = to.load()
                val known = target.entries.mapTo(HashSet()) { it.id }
                to.save(EditIndex(target.entries + moving.entries.filter { it.id !in known }))
                from.indexFile.delete()
            }
            for ((src, dst) in listOf(from.filesDir to to.filesDir, from.doneDir to to.doneDir, from.tmpDir to to.tmpDir)) {
                src.listFiles().orEmpty().forEach { child ->
                    val dest = File(dst, child.name)
                    if (!dest.exists()) {
                        dst.mkdirs()
                        child.renameTo(dest)
                    }
                }
                if (src.list()?.isEmpty() == true) src.delete()
            }
        }
    }
}
