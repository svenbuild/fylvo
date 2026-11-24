package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ArchiveEntry
import io.github.svenbuild.fylvo.android.api.ArchiveListRequest
import io.github.svenbuild.fylvo.android.api.TaskItem
import io.github.svenbuild.fylvo.android.api.WorkflowRequest
import io.github.svenbuild.fylvo.android.util.CrUri

/**
 * Fylvo 1.6: archives (look inside, extract, pack as ZIP) and the account's tasks
 * (design/android-parity-plan.md, 11). The list of an archive with encrypted names takes a
 * password for that one request (1.6.5); extracting takes none, and no address with a share
 * password either (util/Permissions).
 */
class ArchiveRepository(private val api: ApiClient, private val session: SessionManager) {

    /** The entries of [uri]. [password]: for an archive with encrypted names, used for this call only. */
    suspend fun entries(uri: String, password: String? = null): List<ArchiveEntry> =
        if (password.isNullOrEmpty()) api.service().archiveEntries(uri).unwrap().files
        else api.service().archiveEntriesWithPassword(ArchiveListRequest(uri, password)).unwrap().files

    /** Extracts all of [archiveUri] into the folder [dst]; the task runs on the server. */
    suspend fun extract(archiveUri: String, dst: String): TaskItem =
        api.service().extractArchive(WorkflowRequest(listOf(archiveUri), dst), session.sessionTag()).unwrap()

    /** Packs [sources] into the new ZIP file [dst] (full address with its name). */
    suspend fun compress(sources: List<String>, dst: String): TaskItem =
        api.service().createArchive(WorkflowRequest(sources, dst), session.sessionTag()).unwrap()

    data class Page(val tasks: List<TaskItem>, val next: String?)

    suspend fun tasks(next: String? = null): Page {
        val res = api.service().listTasks(pageSize = 20, category = "general", nextPageToken = next).unwrap()
        return Page(res.tasks, res.pagination?.nextToken?.takeIf { it.isNotEmpty() })
    }

    companion object {
        private val EXTRACTABLE = listOf(".zip", ".rar", ".7z", ".tar", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.bz2", ".tbz2")
        private val VIEWABLE = listOf(".zip", ".7z")

        /** The server can extract this file (a single .gz, .xz or .bz2 without tar it cannot). */
        fun extractable(name: String): Boolean = EXTRACTABLE.any { name.lowercase().endsWith(it) }

        /** The server can list the contents (zip and 7z only). */
        fun viewable(name: String): Boolean = VIEWABLE.any { name.lowercase().endsWith(it) }

        /** "Photos.zip" for packing [names]: the one item's name, or [fallback] for several. */
        fun zipName(names: List<String>, fallback: String): String {
            val base = names.singleOrNull()?.let { n -> n.substringBeforeLast('.', n).ifEmpty { n } } ?: fallback
            return "$base.zip"
        }

        /** Running states of a task (more to come). */
        fun running(task: TaskItem): Boolean = task.status in setOf("queued", "processing", "suspending")

        /** The entries directly in [dir] of an archive listing ("" = top), folders first. */
        fun childrenOf(entries: List<ArchiveEntry>, dir: String): List<ArchiveEntry> {
            val prefix = if (dir.isEmpty()) "" else dir.trimEnd('/') + "/"
            val direct = mutableMapOf<String, ArchiveEntry>()
            entries.forEach { e ->
                val name = e.name.trim('/')
                if (!name.startsWith(prefix) || name == prefix.trimEnd('/')) return@forEach
                val rest = name.removePrefix(prefix)
                val first = rest.substringBefore('/')
                if ('/' in rest) {
                    // A deeper entry also stands for its folder (archives may not list folders).
                    direct.putIfAbsent(prefix + first, ArchiveEntry(name = prefix + first, isDirectory = true))
                } else {
                    direct[prefix + first] = e.copy(name = prefix + first)
                }
            }
            return direct.values.sortedWith(compareBy({ !it.isDirectory }, { it.name.lowercase() }))
        }

        /** The folder an archive's task result lands in, to open from the task list. */
        fun openable(uri: String?): String? = uri?.takeIf { it.startsWith(CrUri.MY_PREFIX) || CrUri.isShare(it) }
    }
}
