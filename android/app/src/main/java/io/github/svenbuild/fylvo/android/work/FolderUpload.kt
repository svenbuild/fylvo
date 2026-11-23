package io.github.svenbuild.fylvo.android.work

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import java.io.File

/**
 * Fylvo 1.6: a whole folder of the phone uploaded into Fylvo (design/android-parity-plan.md, 15).
 * The folder is read once (its files with their place below it); the files go into the existing
 * upload queue, one batch per folder. The read right on the picked folder ("tree") is kept while
 * a batch still needs it and given back afterwards (also at the next start, after a kill).
 */
object FolderUpload {
    /** A file of the picked folder: [dir] is its folder below the picked one ("" = directly in it). */
    data class Item(val uri: Uri, val dir: String, val name: String, val size: Long)

    data class Scan(val tree: Uri, val name: String, val files: List<Item>, val dirs: List<String>) {
        val bytes: Long get() = files.sumOf { it.size }
    }

    /** From this many files on, the question says so clearly. */
    const val MANY = 2000

    /** Reads the picked folder; folders without files are kept too (they are made on the server). */
    fun scan(context: Context, tree: Uri): Scan? {
        val root = DocumentFile.fromTreeUri(context, tree) ?: return null
        val files = mutableListOf<Item>()
        val dirs = mutableListOf<String>()
        fun walk(dir: DocumentFile, path: String) {
            dir.listFiles().forEach { child ->
                val name = child.name ?: return@forEach
                if (name.isEmpty() || name == "." || name == ".." || '/' in name) return@forEach
                if (child.isDirectory) {
                    val sub = if (path.isEmpty()) name else "$path/$name"
                    dirs += sub
                    walk(child, sub)
                } else if (child.isFile) {
                    files += Item(child.uri, path, name, child.length())
                }
            }
        }
        walk(root, "")
        return Scan(tree, root.name ?: "Folder", files, dirs)
    }

    /** The files grouped by their folder, in a stable order (top first). */
    fun groups(scan: Scan): List<Pair<String, List<Item>>> =
        scan.files.groupBy { it.dir }.toList().sortedWith(compareBy({ it.first.count { c -> c == '/' } }, { it.first }))

    // ---- the kept read right -------------------------------------------------------------

    private const val PREFS = "folder_upload_trees"
    private const val KEY = "trees"

    fun keep(context: Context, tree: Uri) {
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        p.edit().putStringSet(KEY, p.getStringSet(KEY, emptySet()).orEmpty() + tree.toString()).apply()
    }

    /** Gives back the right on [tree] now. Only through [releaseFinished], which knows who still needs it. */
    private fun release(context: Context, tree: Uri) {
        runCatching { context.contentResolver.releasePersistableUriPermission(tree, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        p.edit().putStringSet(KEY, p.getStringSet(KEY, emptySet()).orEmpty() - tree.toString()).apply()
    }

    /**
     * Trees in use by a pick, its question or its queuing (a count: the same folder can be picked
     * twice): their right stays even while no stored batch needs it yet.
     */
    private val queuing = mutableMapOf<String, Int>()

    /**
     * A right on [tree] was just taken: it is noted at once (a stop of the app before the upload
     * starts gives it back at the next start) and stays in use until [done].
     */
    fun picked(context: Context, tree: Uri) {
        synchronized(queuing) { queuing[tree.toString()] = (queuing[tree.toString()] ?: 0) + 1 }
        keep(context, tree)
    }

    /**
     * The pick of [tree] is over (queued, cancelled or failed): its right goes back unless a stored
     * batch or another pick still needs it.
     */
    fun done(context: Context, tree: Uri) {
        synchronized(queuing) {
            val n = (queuing[tree.toString()] ?: 0) - 1
            if (n > 0) queuing[tree.toString()] = n else queuing.remove(tree.toString())
        }
        releaseFinished(context)
    }

    /** Gives back every kept right no stored upload batch needs any more. */
    fun releaseFinished(context: Context) {
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val busy = synchronized(queuing) { queuing.keys.toSet() }
        val trees = p.getStringSet(KEY, emptySet()).orEmpty()
        if (trees.isEmpty()) return
        val batches = File(context.filesDir, "upload_batches").listFiles { f -> f.name.endsWith(".json") }.orEmpty()
            .mapNotNull { runCatching { it.readText() }.getOrNull() }
        toRelease(trees, busy, batches).forEach { release(context, Uri.parse(it)) }
    }

    /** The kept [trees] no stored batch needs and that are not being queued ([busy]). */
    fun toRelease(trees: Set<String>, busy: Set<String>, batchTexts: List<String>): List<String> =
        trees.filter { it !in busy && !needed(it, batchTexts) }

    /** A stored batch still holds a file of [tree] (its documents' addresses start with the tree's). */
    fun needed(tree: String, batchTexts: List<String>): Boolean {
        val prefix = tree.trimEnd('/') + "/document/"
        return batchTexts.any { it.contains(prefix) }
    }
}
