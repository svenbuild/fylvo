package io.github.svenbuild.fylvo.android.work

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
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
                OfflineSyncEngine(app.fileRepository, manager).run()
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

        private fun constraints(mobileData: Boolean): Constraints =
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
         * One run soon. KEEP: a run that is waiting or busy already covers it (a busy run also
         * takes the roots switched on meanwhile).
         */
        fun syncNow(context: Context, mobileData: Boolean) {
            WorkManager.getInstance(context).enqueueUniqueWork(
                WORK_ONCE,
                ExistingWorkPolicy.KEEP,
                OneTimeWorkRequestBuilder<OfflineSyncWorker>().setConstraints(constraints(mobileData)).build(),
            )
        }

        /** A one-time run still waiting (not running) is replaced by one with the new network setting. Blocking. */
        fun replaceWaiting(context: Context, mobileData: Boolean) {
            val wm = WorkManager.getInstance(context)
            val waiting = wm.getWorkInfosForUniqueWork(WORK_ONCE).get().any { it.state == WorkInfo.State.ENQUEUED }
            if (!waiting) return
            wm.enqueueUniqueWork(
                WORK_ONCE,
                ExistingWorkPolicy.REPLACE,
                OneTimeWorkRequestBuilder<OfflineSyncWorker>().setConstraints(constraints(mobileData)).build(),
            )
        }

        fun cancel(context: Context) {
            val wm = WorkManager.getInstance(context)
            wm.cancelUniqueWork(WORK_PERIODIC)
            wm.cancelUniqueWork(WORK_ONCE)
        }
    }
}
