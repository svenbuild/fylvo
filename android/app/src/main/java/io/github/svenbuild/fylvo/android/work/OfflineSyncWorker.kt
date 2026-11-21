package io.github.svenbuild.fylvo.android.work

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequest
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.data.offline.OfflineSyncEngine
import java.util.concurrent.TimeUnit

/**
 * Fylvo: runs the offline sync ([OfflineSyncEngine]): every 6 hours, once when a root is switched
 * on, and on "Sync now". Over Wi-Fi only unless mobile data is allowed in the Account settings;
 * not on low battery. No foreground service: a run that is stopped goes on next time.
 */
class OfflineSyncWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val app = applicationContext as FylvoApp
        if (app.sessionManager.refreshTokenNow().isEmpty()) return Result.success()
        val manager = app.offlineManager
        while (true) {
            // Another run is busy: it also takes roots switched on (and "Sync now" asked) meanwhile.
            if (!manager.engineLock.tryLock()) return Result.success()
            try {
                OfflineSyncEngine(app.fileRepository, manager, app.offlineEdits).run()
            } finally {
                manager.engineLock.unlock()
            }
            // A "Sync now" that came at the very end (its enqueue was dropped while this job ran).
            if (!manager.hasSyncRequest()) return Result.success()
        }
    }

    companion object {
        private const val WORK_PERIODIC = "offline_sync_periodic"
        private const val WORK_ONCE = "offline_sync_once"

        internal fun constraints(mobileData: Boolean): Constraints =
            Constraints.Builder()
                .setRequiredNetworkType(if (mobileData) NetworkType.CONNECTED else NetworkType.UNMETERED)
                .setRequiresBatteryNotLow(true)
                .build()

        /** Keeps the 6-hour run with the current network setting. */
        fun apply(context: Context, mobileData: Boolean) {
            val periodic = PeriodicWorkRequestBuilder<OfflineSyncWorker>(6, TimeUnit.HOURS)
                .setConstraints(constraints(mobileData))
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(WORK_PERIODIC, ExistingPeriodicWorkPolicy.UPDATE, periodic)
        }

        /**
         * One run soon. A run already waiting (enqueued, or blocked behind a busy one) covers it;
         * while one is busy, one more is queued after it (never dropped). Blocking.
         */
        fun syncNow(context: Context, mobileData: Boolean) {
            enqueueOnce(context, WORK_ONCE, OneTimeWorkRequestBuilder<OfflineSyncWorker>().setConstraints(constraints(mobileData)).build())
        }

        /**
         * Fylvo: [request] as the unique work [name], unless one is already waiting (enqueued, or
         * blocked behind a busy one), which covers it; while one is busy it is queued after it.
         */
        internal fun enqueueOnce(context: Context, name: String, request: OneTimeWorkRequest) {
            val wm = WorkManager.getInstance(context)
            if (wm.getWorkInfosForUniqueWork(name).get().any { it.state in WAITING }) return
            wm.enqueueUniqueWork(name, ExistingWorkPolicy.APPEND_OR_REPLACE, request)
        }

        /**
         * The network setting changed: one-time runs still waiting (enqueued, or blocked behind a
         * busy one) are cancelled; with [mobileData] off a busy one too (it may be downloading over
         * mobile data; the engine goes on where it stopped). Then one new run is queued with the new
         * setting. APPEND_OR_REPLACE drops the cancelled runs from the chain, so the new run does not
         * wait for a busy one that was left alone; if it starts while that one holds the engine lock
         * it ends at once, and the busy run covers the work through the sync request flag. Blocking.
         */
        fun replaceWaiting(context: Context, mobileData: Boolean) {
            val wm = WorkManager.getInstance(context)
            val infos = wm.getWorkInfosForUniqueWork(WORK_ONCE).get()
            val stop = infos.filter { it.state in WAITING || (!mobileData && it.state == WorkInfo.State.RUNNING) }
            if (stop.isEmpty()) return
            stop.forEach { wm.cancelWorkById(it.id).result.get() }
            wm.enqueueUniqueWork(
                WORK_ONCE,
                ExistingWorkPolicy.APPEND_OR_REPLACE,
                OneTimeWorkRequestBuilder<OfflineSyncWorker>().setConstraints(constraints(mobileData)).build(),
            )
        }

        internal val WAITING = setOf(WorkInfo.State.ENQUEUED, WorkInfo.State.BLOCKED)

        fun cancel(context: Context) {
            val wm = WorkManager.getInstance(context)
            wm.cancelUniqueWork(WORK_PERIODIC)
            wm.cancelUniqueWork(WORK_ONCE)
        }
    }
}
