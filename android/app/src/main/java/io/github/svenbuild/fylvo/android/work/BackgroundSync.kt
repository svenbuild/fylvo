package io.github.svenbuild.fylvo.android.work

import androidx.work.Data
import androidx.work.WorkInfo
import androidx.work.workDataOf

/**
 * Progress contract of the periodic folder mirror, so the home page can
 * show what is running right now. The worker tags its requests with [TAG]
 * and publishes [progress] per item.
 */
object BackgroundSync {
    const val TAG = "background_sync"
    private const val KEY_KIND = "kind"
    private const val KEY_NAME = "name"
    private const val KEY_INDEX = "index"
    private const val KEY_TOTAL = "total"

    enum class Kind(val label: String) {
        FOLDER("Syncing folder"),
    }

    fun progress(kind: Kind, name: String, index: Int, total: Int): Data =
        workDataOf(KEY_KIND to kind.name, KEY_NAME to name, KEY_INDEX to index, KEY_TOTAL to total)

    data class Status(val kind: Kind, val name: String, val index: Int, val total: Int) {
        val fraction: Float get() = if (total > 0) index.toFloat() / total else 0f
    }

    /** Null unless the work is running and has reported at least one item. */
    fun statusOf(info: WorkInfo): Status? {
        if (info.state != WorkInfo.State.RUNNING) return null
        val kind = info.progress.getString(KEY_KIND)?.let { runCatching { Kind.valueOf(it) }.getOrNull() } ?: return null
        val total = info.progress.getInt(KEY_TOTAL, 0)
        if (total <= 0) return null
        return Status(kind, info.progress.getString(KEY_NAME).orEmpty(), info.progress.getInt(KEY_INDEX, 0), total)
    }
}
