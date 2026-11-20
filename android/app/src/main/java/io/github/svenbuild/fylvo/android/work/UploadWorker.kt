package io.github.svenbuild.fylvo.android.work

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.util.Log
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.Data
import androidx.work.ForegroundInfo
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.localized
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.TimeUnit

/**
 * UploadWorker copies SAF/content URIs into cache, one at a time, then streams them through
 * the Cloudreve upload-session flow. The list of files is an [UploadBatch] on disk. WorkManager survives process death for
 * large transfers. Progress goes two ways: a determinate foreground
 * notification with a cancel action, and [WorkInfo.progress] for the in-app
 * transfer card (see [UploadStatus.from]).
 */
class UploadWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {

    private val notifications =
        applicationContext.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    private var lastNotified = 0L

    override suspend fun doWork(): Result {
        val app = applicationContext as FylvoApp
        val queue = queue(applicationContext)
        val batchId = inputData.getString(KEY_BATCH_ID) ?: return Result.failure()
        var batch = queue.load(batchId) ?: return Result.failure()
        val parentUri = batch.parentUri
        val total = batch.items.size

        createChannel()
        var lastError: String? = null
        try {
            while (true) {
                val i = batch.next()
                if (i < 0) break
                val item = batch.items[i]

                // A session left by a run that was stopped half way: cancel it on the server, so
                // its 0 B placeholder and lock go away before the file is sent again.
                item.sessionId?.let { sessionId ->
                    app.fileRepository.deleteUploadSession(sessionId, item.targetUri ?: CrUri.join(parentUri, item.name))
                    batch = batch.with(i) { it.copy(sessionId = null, targetUri = null) }.also(queue::save)
                }

                report(item.name, i, total, sent = 0, size = 0, parentUri = parentUri, force = true)
                val outcome = uploadOne(app, batch, i) { sessionId, targetUri ->
                    batch = batch.with(i) { it.copy(sessionId = sessionId, targetUri = targetUri) }.also(queue::save)
                }
                when (outcome) {
                    is Outcome.Done -> {
                        batch = batch.with(i) { it.copy(state = UploadItem.State.Done, sessionId = null, targetUri = null) }
                        queue.save(batch)
                        releaseRead(item.uri)
                    }
                    is Outcome.Failed -> {
                        cancelSession(app, batch.items[i])
                        batch = batch.with(i) { it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null) }
                        queue.save(batch)
                        releaseRead(item.uri)
                        lastError = outcome.message
                    }
                    is Outcome.TryLater -> {
                        // No network or the server is away: keep the list and the session id,
                        // WorkManager runs the batch again when the network is back.
                        if (runAttemptCount < MAX_ATTEMPTS) return Result.retry()
                        cancelSession(app, batch.items[i])
                        batch = batch.copy(items = batch.items.map {
                            if (it.state == UploadItem.State.Pending) it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null) else it
                        })
                        queue.save(batch)
                        lastError = outcome.message
                        break
                    }
                }
            }
        } catch (e: CancellationException) {
            // Stopped: by the person (cancel), or by the system, which runs the job again later.
            withContext(NonCancellable) {
                val current = batch.next().takeIf { it >= 0 }?.let { batch.items[it] }
                if (current?.sessionId != null && cancelSession(app, current)) {
                    batch = batch.with(batch.next()) { it.copy(sessionId = null, targetUri = null) }
                    queue.save(batch)
                }
                if (isCancelledByPerson()) finish(queue, batch)
            }
            throw e
        } finally {
            notifications.cancel(NOTIF_ID)
        }

        finish(queue, batch)
        val output = workDataOf(
            KEY_PARENT_URI to parentUri,
            KEY_UPLOADED to batch.uploaded,
            KEY_FAILED to batch.failed,
            KEY_ERROR to lastError,
        )
        return if (batch.failed == 0) Result.success(output) else Result.failure(output)
    }

    private sealed interface Outcome {
        data object Done : Outcome
        /** This file cannot be uploaded (the server said no, or the file can no longer be read). */
        data class Failed(val message: String) : Outcome
        /** Network or server trouble: the whole batch should wait and try again. */
        data class TryLater(val message: String) : Outcome
    }

    private suspend fun uploadOne(
        app: FylvoApp, batch: UploadBatch, i: Int, onSession: (String, String) -> Unit,
    ): Outcome {
        val item = batch.items[i]
        val uri = Uri.parse(item.uri)
        val tmp = try {
            stageToCache(uri)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Gone, or the picker's permission ended (e.g. after the app was killed).
            Log.w(LOG_TAG, "Cannot read a picked file: ${e.javaClass.simpleName}")
            return Outcome.Failed(applicationContext.localized().getString(R.string.could_not_read_file, item.name))
        }
        return try {
            val mime = applicationContext.contentResolver.getType(uri)
            app.fileRepository.uploadFile(
                tmp, batch.parentUri, remoteName = item.name, overwrite = batch.overwrite, mimeType = mime,
                onSession = onSession,
            ) { sent, size -> report(item.name, i, batch.items.size, sent, size, batch.parentUri) }
            Outcome.Done
        } catch (e: CancellationException) {
            throw e
        } catch (e: java.io.IOException) {
            Log.w(LOG_TAG, "Upload interrupted: ${e.javaClass.simpleName}")
            Outcome.TryLater(applicationContext.localized().getString(R.string.upload_waits_for_network))
        } catch (e: ApiException) {
            Log.w(LOG_TAG, "Upload refused: code ${e.code}")
            if (e.isTransient) Outcome.TryLater(describe(e, item.name)) else Outcome.Failed(describe(e, item.name))
        } catch (e: Exception) {
            Log.w(LOG_TAG, "Upload failed: ${e.javaClass.simpleName}")
            Outcome.Failed(applicationContext.localized().getString(R.string.could_not_upload, item.name))
        } finally {
            tmp.delete()
        }
    }

    /** Cancels the item's server session if it has one; true when there is none left. */
    private suspend fun cancelSession(app: FylvoApp, item: UploadItem): Boolean {
        val sessionId = item.sessionId ?: return true
        return app.fileRepository.deleteUploadSession(sessionId, item.targetUri ?: return true)
    }

    private fun isCancelledByPerson(): Boolean =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S && stopReason == WorkInfo.STOP_REASON_CANCELLED_BY_APP

    /** The batch is over: forget its list and give the read permissions back. */
    private fun finish(queue: UploadQueue, batch: UploadBatch) {
        batch.items.forEach { releaseRead(it.uri) }
        queue.delete(batch.id)
    }

    private fun releaseRead(uri: String) {
        runCatching {
            applicationContext.contentResolver.releasePersistableUriPermission(Uri.parse(uri), Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
    }

    /**
     * Pushes progress to WorkManager and (throttled) to the notification.
     * Called from OkHttp's write loop, hence the non-suspending Async variants.
     */
    private fun report(
        name: String, index: Int, total: Int, sent: Long, size: Long, parentUri: String, force: Boolean = false,
    ) {
        setProgressAsync(
            workDataOf(
                KEY_PARENT_URI to parentUri,
                KEY_PROGRESS_NAME to name,
                KEY_PROGRESS_INDEX to index,
                KEY_PROGRESS_TOTAL to total,
                KEY_PROGRESS_SENT to sent,
                KEY_PROGRESS_SIZE to size,
            )
        )
        val now = System.currentTimeMillis()
        if (force || now - lastNotified >= NOTIFY_INTERVAL_MS) {
            lastNotified = now
            setForegroundAsync(foregroundInfo(name, index, total, sent, size))
        }
    }

    /** Server error codes read like log lines; say what happened to this file instead. */
    private fun describe(e: Exception, name: String): String {
        val texts = applicationContext.localized()
        return when {
            e is ApiException && e.code == CODE_OBJECT_EXISTS -> texts.getString(R.string.file_exists_in_folder, name)
            e is ApiException && e.code == CODE_QUOTA_EXCEEDED -> texts.getString(R.string.not_enough_storage, name)
            else -> texts.getString(R.string.could_not_upload, name)
        }
    }

    private fun resolveDisplayName(uri: Uri): String {
        val cursor = applicationContext.contentResolver.query(uri, null, null, null, null)
        cursor?.use {
            val idx = it.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
            if (it.moveToFirst() && idx >= 0) return it.getString(idx)
        }
        return uri.lastPathSegment?.substringAfterLast('/') ?: "upload.bin"
    }

    /**
     * Copies the picked document into cache. The staging name is made by the app (worker id
     * plus a counter), never taken from the document, so a crafted display name cannot
     * point outside the staging folder.
     */
    private fun stageToCache(uri: Uri): File {
        if (!isForeignContentUri(applicationContext, uri)) throw IllegalArgumentException("Unsupported file source")
        val dir = File(applicationContext.cacheDir, "upload_staging").apply { mkdirs() }
        val tmp = File(dir, "$id-${staged++}")
        applicationContext.contentResolver.openInputStream(uri)!!.use { input ->
            FileOutputStream(tmp).use { out -> input.copyTo(out, 256 * 1024) }
        }
        return tmp
    }

    private var staged = 0

    private fun foregroundInfo(name: String, index: Int, total: Int, sent: Long, size: Long): ForegroundInfo {
        val cancel = WorkManager.getInstance(applicationContext).createCancelPendingIntent(id)
        val percent = if (size > 0) (sent * 100 / size).toInt() else 0
        val texts = applicationContext.localized()
        val title = if (total > 1) texts.getString(R.string.uploading_n_of, index + 1, total) else texts.getString(R.string.uploading)
        val notification = NotificationCompat.Builder(applicationContext, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle(title)
            .setContentText(name)
            .setProgress(100, percent, size <= 0)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .addAction(android.R.drawable.ic_menu_close_clear_cancel, texts.getString(R.string.cancel), cancel)
            .build()
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ForegroundInfo(NOTIF_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(NOTIF_ID, notification)
        }
    }

    private fun createChannel() {
        notifications.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, applicationContext.localized().getString(R.string.channel_uploads), NotificationManager.IMPORTANCE_LOW)
        )
    }

    companion object {
        const val TAG = "upload"
        /** Id of the [UploadBatch] with the files; the list itself is not in the job's input. */
        const val KEY_BATCH_ID = "batch_id"
        private const val UNIQUE_WORK = "uploads"
        private const val MAX_ATTEMPTS = 8
        private const val LOG_TAG = "FylvoUpload"
        const val KEY_PARENT_URI = "parent_uri"
        const val KEY_UPLOADED = "uploaded"
        const val KEY_FAILED = "failed"
        const val KEY_ERROR = "error"
        const val KEY_PROGRESS_NAME = "name"
        const val KEY_PROGRESS_INDEX = "index"
        const val KEY_PROGRESS_TOTAL = "total"
        const val KEY_PROGRESS_SENT = "sent"
        const val KEY_PROGRESS_SIZE = "size"
        private const val CHANNEL_ID = "uploads"
        private const val NOTIF_ID = 41
        private const val NOTIFY_INTERVAL_MS = 500L
        // pkg/serializer/error.go
        private const val CODE_OBJECT_EXISTS = 40004
        private const val CODE_QUOTA_EXCEEDED = 40051

        /**
         * Queues an upload of [uris] into [parentUri]. The list goes into a file of the app
         * ([UploadQueue]), the job only carries its id: any number of files fits. The read
         * permission of each picked file is kept beyond the app's lifetime where Android allows
         * it, so the upload can go on after the app was closed or killed. Batches run one after
         * the other, one file at a time.
         */
        fun enqueue(
            context: Context,
            uris: List<Uri>,
            parentUri: String,
            names: List<String>? = null,
            overwrite: Boolean = false,
        ) {
            if (uris.isEmpty()) return
            val fileNames = names ?: uris.map { displayName(context, it) }
            uris.forEach { uri ->
                // Only documents from the system picker can be kept; shared files keep the
                // permission they came with. Android caps kept permissions (128 to 512 per app).
                runCatching {
                    context.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
            }
            val batch = queue(context).create(uris.map { it.toString() }, fileNames, parentUri, overwrite)
            val request = OneTimeWorkRequestBuilder<UploadWorker>()
                .addTag(TAG)
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
                .setInputData(workDataOf(KEY_BATCH_ID to batch.id, KEY_PARENT_URI to parentUri))
                .build()
            WorkManager.getInstance(context).enqueueUniqueWork(UNIQUE_WORK, ExistingWorkPolicy.APPEND_OR_REPLACE, request)
        }

        fun queue(context: Context) = UploadQueue(File(context.filesDir, "upload_batches"))

        /** Lists no job will finish any more (older than a week), with their permissions. */
        fun cleanUpOldBatches(context: Context) {
            val queue = queue(context)
            queue.idsOlderThan(TimeUnit.DAYS.toMillis(7)).forEach { id ->
                queue.load(id)?.items?.forEach { item ->
                    runCatching {
                        context.contentResolver.releasePersistableUriPermission(Uri.parse(item.uri), Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    }
                }
                queue.delete(id)
            }
        }

        /**
         * Fylvo: only `content://` documents of other apps may be uploaded. A `file://` URI or
         * one of this app's own providers could make the app upload its private files.
         */
        fun isForeignContentUri(context: Context, uri: Uri): Boolean {
            if (uri.scheme != android.content.ContentResolver.SCHEME_CONTENT) return false
            // A "0@" user prefix in the authority is dropped when Android resolves the provider.
            val authority = uri.authority?.substringAfterLast('@') ?: return false
            if (authority.startsWith(context.packageName)) return false
            val owner = context.packageManager.resolveContentProvider(authority, 0)?.packageName
            return owner != context.packageName
        }

        /** Display name of a picked document, as the picker shows it. */
        fun displayName(context: Context, uri: Uri): String {
            context.contentResolver.query(uri, arrayOf(android.provider.OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst() && !it.isNull(0)) return it.getString(0)
            }
            return uri.lastPathSegment?.substringAfterLast('/') ?: "upload.bin"
        }

        /** "report.pdf" -> "report (1).pdf", counting up until the name is free. */
        fun uniqueName(name: String, taken: Set<String>): String {
            if (name !in taken) return name
            val dot = name.lastIndexOf('.').takeIf { it > 0 } ?: name.length
            val stem = name.substring(0, dot)
            val ext = name.substring(dot)
            var n = 1
            while ("$stem ($n)$ext" in taken) n++
            return "$stem ($n)$ext"
        }
    }
}

/** In-app view of one upload request, derived from [WorkInfo]. */
data class UploadStatus(
    val id: java.util.UUID,
    val state: WorkInfo.State,
    val parentUri: String,
    val name: String,
    val index: Int,
    val total: Int,
    val sent: Long,
    val size: Long,
    val uploaded: Int,
    val failed: Int,
    val error: String?,
) {
    val active: Boolean get() = !state.isFinished
    /** Whole-batch fraction, or null while the current file's size is unknown. */
    val fraction: Float?
        get() {
            if (total <= 0) return null
            val current = if (size > 0) sent.toFloat() / size else return if (index > 0) index.toFloat() / total else null
            return ((index + current) / total).coerceIn(0f, 1f)
        }

    companion object {
        fun from(info: WorkInfo): UploadStatus {
            val p = info.progress
            val o = info.outputData
            return UploadStatus(
                id = info.id,
                state = info.state,
                parentUri = p.getString(UploadWorker.KEY_PARENT_URI) ?: o.getString(UploadWorker.KEY_PARENT_URI) ?: "",
                name = p.getString(UploadWorker.KEY_PROGRESS_NAME) ?: "",
                index = p.getInt(UploadWorker.KEY_PROGRESS_INDEX, 0),
                total = p.getInt(UploadWorker.KEY_PROGRESS_TOTAL, 0),
                sent = p.getLong(UploadWorker.KEY_PROGRESS_SENT, 0),
                size = p.getLong(UploadWorker.KEY_PROGRESS_SIZE, 0),
                uploaded = o.getInt(UploadWorker.KEY_UPLOADED, 0),
                failed = o.getInt(UploadWorker.KEY_FAILED, 0),
                error = o.getString(UploadWorker.KEY_ERROR),
            )
        }
    }
}
