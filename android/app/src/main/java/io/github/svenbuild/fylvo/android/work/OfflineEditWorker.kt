package io.github.svenbuild.fylvo.android.work

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import io.github.svenbuild.fylvo.android.FylvoApp
import java.util.concurrent.TimeUnit

/**
 * Fylvo: the edit check, a short run of only the upload pass for working copies (see
 * OfflineEdits): a few minutes after "Bearbeiten" opened an editor, at app start when something
 * waits, and again while a copy is still being written or the server was busy. It belongs to the
 * account it was started for and does nothing once another one is signed in. Same network and
 * battery rules as the offline sync.
 */
class OfflineEditWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val app = applicationContext as FylvoApp
        val account = inputData.getString(KEY_ACCOUNT) ?: return Result.success()
        if (app.sessionManager.refreshTokenNow().isEmpty()) return Result.success()
        app.offlineEdits.check(account)
        return Result.success()
    }

    companion object {
        private const val WORK = "offline_edit_check"
        private const val KEY_ACCOUNT = "account"

        private fun request(accountKey: String, mobileData: Boolean, delayMs: Long) =
            OneTimeWorkRequestBuilder<OfflineEditWorker>()
                .setInitialDelay(delayMs.coerceAtLeast(0), TimeUnit.MILLISECONDS)
                .setConstraints(OfflineSyncWorker.constraints(mobileData))
                .setInputData(workDataOf(KEY_ACCOUNT to accountKey))
                .addTag(TAG_ACCOUNT + accountKey)
                .build()

        /** A check in [delayMs]; one already waiting covers it, a running one is never cancelled. Blocking. */
        fun schedule(context: Context, accountKey: String, mobileData: Boolean, delayMs: Long) {
            OfflineSyncWorker.enqueueOnce(context, WORK, request(accountKey, mobileData, delayMs))
        }

        /** The network setting changed: a waiting check is replaced by one with the new setting. Blocking. */
        fun replaceWaiting(context: Context, mobileData: Boolean) {
            val wm = WorkManager.getInstance(context)
            val waiting = wm.getWorkInfosForUniqueWork(WORK).get().filter { it.state in OfflineSyncWorker.WAITING }
            if (waiting.isEmpty()) return
            val account = waiting.firstNotNullOfOrNull { info -> info.tags.firstOrNull { it.startsWith(TAG_ACCOUNT) } }
            waiting.forEach { wm.cancelWorkById(it.id).result.get() }
            account?.removePrefix(TAG_ACCOUNT)?.let { schedule(context, it, mobileData, 0) }
        }

        fun cancel(context: Context) {
            WorkManager.getInstance(context).cancelUniqueWork(WORK)
        }

        private const val TAG_ACCOUNT = "account:"
    }
}
