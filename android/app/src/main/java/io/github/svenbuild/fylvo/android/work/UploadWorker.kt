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
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ConnectedUploadState
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.UPLOAD_DONE
import io.github.svenbuild.fylvo.android.data.UPLOAD_FAILED
import io.github.svenbuild.fylvo.android.data.UPLOAD_RECEIVING
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.ErrorText
import io.github.svenbuild.fylvo.android.util.localized
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * UploadWorker copies SAF/content URIs into cache, a few at a time, then streams them through
 * the Cloudreve upload-session flow. The list of files is an [UploadBatch] on disk. WorkManager survives process death for
 * large transfers. Progress goes two ways: a determinate foreground
 * notification with a cancel action, and [WorkInfo.progress] for the in-app
 * transfer card (see [UploadStatus.from]).
 */
class UploadWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {

    private val notifications =
        applicationContext.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    private var lastNotified = 0L
    /** One notification per batch: batches can run side by side. */
    private val notificationId = NOTIF_ID_BASE + (id.hashCode() and 0xFFFF)

    override suspend fun doWork(): Result {
        val app = applicationContext as FylvoApp
        val queue = queue(applicationContext)
        var batch = inputData.getString(KEY_BATCH_ID)?.let(queue::load)
            ?: legacyBatch(queue)
            // Nothing to do (the list is gone); success, so no other upload is affected.
            ?: return Result.success(workDataOf(KEY_UPLOADED to 0, KEY_FAILED to 0))
        // Queued for another account (a sign-out raced with queuing): never upload it here.
        if (batch.account != null && batch.account != currentAccount(app)) {
            finish(queue, batch)
            return Result.success(workDataOf(KEY_UPLOADED to 0, KEY_FAILED to 0))
        }
        val parentUri = batch.parentUri
        val total = batch.items.size
        /** Fylvo: the chosen target folder is still the same (by id); null: cannot be told now. */
        suspend fun targetStillThere(): Boolean? =
            batch.expectedParentId?.let { app.fileRepository.folderIdMatches(parentUri, it) } ?: true
        val targetChanged = applicationContext.localized().getString(R.string.upload_target_changed)
        when (targetStillThere()) {
            null -> return Result.retry()
            false -> {
                // Before anything is sent or resumed: the folder was replaced or removed.
                batch.items.forEachIndexed { i, item ->
                    if (item.state == UploadItem.State.Pending) {
                        if (item.sessionId != null) cancelSession(app, item)
                        batch = batch.with(i) { it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null) }
                    }
                }
                queue.save(batch)
                finish(queue, batch)
                return Result.success(
                    workDataOf(KEY_UPLOADED to batch.uploaded, KEY_FAILED to batch.failed, KEY_PARENT_URI to parentUri, KEY_ERROR to targetChanged),
                )
            }
            true -> Unit
        }

        // Copies of this job left in the cache by a stop during the copy.
        stagingFiles().forEach { it.delete() }

        createChannel()
        var lastError: String? = null
        // The batch is changed by several uploads at once: every change goes through [update].
        val lock = Any()
        fun update(i: Int, change: (UploadItem) -> UploadItem) = synchronized(lock) {
            batch = batch.with(i, change)
            queue.save(batch)
        }
        var progressed = false // a file finished in this run (read and written under [lock])
        try {
            // Sessions left by a run that was stopped half way. While our session exists, its
            // placeholder and lock hold the name, so nobody else can put a file there: if the file
            // there already has the full size, the upload finished and only its answer got lost.
            // Otherwise cancel the session, so its 0 B placeholder and lock go away before the
            // file is sent again. If the server cannot be asked now, wait and try later.
            batch.items.forEachIndexed { i, item ->
                // Fylvo: a connected storage says itself where the upload stands (see resolveConnected).
                if (item.state == UploadItem.State.Pending && item.sessionId != null && Connected.isConnected(parentUri)) {
                    when (val r = resolveConnected(app, item)) {
                        Resume.Done -> {
                            update(i) { it.copy(state = UploadItem.State.Done, sessionId = null, targetUri = null) }
                            progressed = true
                        }
                        Resume.Resend -> update(i) { it.copy(sessionId = null, targetUri = null, interrupted = true) }
                        Resume.Unknown -> update(i) { it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null, unknownOutcome = true) }
                        is Resume.Failed -> {
                            update(i) { it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null) }
                            lastError = describe(ApiException(r.code, "upload failed"), item.name)
                        }
                        Resume.Later -> {
                            // Counted like a run without progress (network trouble too), so it cannot
                            // wait for ever; it then ends as unknown and is never sent again.
                            val runs = if (progressed) 1 else batch.serverRetries + 1
                            if (runs < MAX_IDLE_RUNS) {
                                synchronized(lock) { batch = batch.copy(serverRetries = runs).also(queue::save) }
                                return Result.retry()
                            }
                            update(i) { it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null, unknownOutcome = true) }
                        }
                    }
                    return@forEachIndexed
                }
                if (item.state == UploadItem.State.Pending && item.sessionId != null) {
                    val target = item.targetUri
                    val replace = item.overwrite ?: batch.overwrite
                    if (!replace && target != null && item.uploadSize > 0) {
                        // Any unclear answer (no network, server trouble, a login page of a
                        // public Wi-Fi instead of JSON): ask again later, never guess.
                        val serverSize = try {
                            app.fileRepository.sizeOf(target)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            return Result.retry()
                        }
                        if (finishedBeforeStop(replace, item.uploadSize, serverSize)) {
                            update(i) { it.copy(state = UploadItem.State.Done, sessionId = null, targetUri = null) }
                            return@forEachIndexed
                        }
                    }
                    if (!cancelSession(app, item)) return Result.retry()
                    update(i) { it.copy(sessionId = null, targetUri = null, interrupted = true) }
                }
            }

            val pending = batch.items.indices.filter { batch.items[it].state == UploadItem.State.Pending }
            val cursor = AtomicInteger(0)
            // Per file in flight: how far it is, in thousandths. Read and written under [lock], so
            // every report is one consistent picture.
            val inFlight = HashMap<Int, Int>()
            fun reportAll(name: String, i: Int, sent: Long, size: Long, force: Boolean = false) = synchronized(lock) {
                if (i in inFlight) inFlight[i] = if (size > 0) (sent * 1000 / size).toInt().coerceIn(0, 1000) else 0
                val done = batch.uploaded + batch.failed
                report(name, done, total, inFlight.values.sum(), inFlight.size, parentUri, force)
            }
            // A network or server problem in one upload: the others finish the file they are on
            // (cancelling them could leave a session the list does not know), then the run stops.
            val stop = java.util.concurrent.atomic.AtomicReference<Outcome.TryLater?>(null)

            // Fylvo: one file at a time into a connected storage (the server talks to the provider with two).
            val lanes = if (Connected.isConnected(parentUri)) 1 else parallelUploads()
            val memory = Semaphore(lanes)
            val bigFiles = Mutex() // one big file collects its permits at a time: no deadlock
            suspend fun <T> withMemory(permits: Int, block: suspend () -> T): T {
                if (permits == 1) memory.acquire() else bigFiles.withLock { repeat(permits) { memory.acquire() } }
                try {
                    return block()
                } finally {
                    repeat(permits) { memory.release() }
                }
            }
            coroutineScope {
                    repeat(lanes) {
                        launch {
                            while (stop.get() == null) {
                                val i = pending.getOrNull(cursor.getAndIncrement()) ?: break
                                val item = synchronized(lock) { batch.items[i] }
                                // A big file needs a buffer of a whole chunk (up to 64 MB): it runs
                                // alone, so several of them never fill the memory at once.
                                val size = sizeOf(item)
                                val permits = if (size == 0L || size > BIG_FILE_BYTES) lanes else 1
                                withMemory(permits) {
                                    // Another upload may have stopped the run while this one waited.
                                    if (stop.get() != null) return@withMemory
                                    // Fylvo: the chosen folder is checked again before every file.
                                    when (targetStillThere()) {
                                        null -> {
                                            stop.compareAndSet(null, Outcome.TryLater(targetChanged, network = true))
                                            return@withMemory
                                        }
                                        false -> {
                                            update(i) { it.copy(state = UploadItem.State.Failed) }
                                            synchronized(lock) { lastError = targetChanged }
                                            return@withMemory
                                        }
                                        true -> Unit
                                    }
                                    synchronized(lock) { inFlight[i] = 0 }
                                    reportAll(item.name, i, 0, 0, force = true)
                                    val outcome = uploadOne(app, item, parentUri, item.overwrite ?: batch.overwrite, onSession = { sessionId, targetUri, size ->
                                        update(i) { it.copy(sessionId = sessionId, targetUri = targetUri, uploadSize = size) }
                                    }) { sent, size -> reportAll(item.name, i, sent, size) }
                                    when (outcome) {
                                        is Outcome.Done -> synchronized(lock) {
                                            inFlight.remove(i)
                                            update(i) { it.copy(state = UploadItem.State.Done, sessionId = null, targetUri = null, interrupted = false) }
                                            progressed = true
                                        }
                                        is Outcome.Failed -> {
                                            val end = endSession(app, synchronized(lock) { batch.items[i] }, parentUri)
                                            synchronized(lock) {
                                                inFlight.remove(i)
                                                update(i) {
                                                    it.copy(
                                                        state = if (end == Resume.Done) UploadItem.State.Done else UploadItem.State.Failed,
                                                        sessionId = null, targetUri = null, interrupted = false, unknownOutcome = end == Resume.Unknown,
                                                    )
                                                }
                                                if (end == Resume.Done) progressed = true else lastError = outcome.message
                                            }
                                        }
                                        // Fylvo: never sent again, the file on the phone stays as it is.
                                        is Outcome.Unknown -> synchronized(lock) {
                                            inFlight.remove(i)
                                            update(i) { it.copy(state = UploadItem.State.Failed, sessionId = null, targetUri = null, interrupted = false, unknownOutcome = true) }
                                        }
                                        // Its session stays in the list and is cancelled at the start
                                        // of the next run.
                                        is Outcome.TryLater -> {
                                            val tries = synchronized(lock) { batch.items[i].serverRetries }
                                            if (!outcome.network && tries + 1 >= MAX_ATTEMPTS) {
                                                // This one file keeps failing on the server: give it up
                                                // alone, the others go on.
                                                val end = endSession(app, synchronized(lock) { batch.items[i] }, parentUri)
                                                synchronized(lock) {
                                                    inFlight.remove(i)
                                                    update(i) {
                                                        it.copy(
                                                            state = if (end == Resume.Done) UploadItem.State.Done else UploadItem.State.Failed,
                                                            sessionId = null, targetUri = null, unknownOutcome = end == Resume.Unknown,
                                                        )
                                                    }
                                                    if (end == Resume.Done) progressed = true else lastError = outcome.message
                                                }
                                            } else {
                                                // Fylvo: a session that is gone stored nothing (40011): start afresh next run.
                                                if (outcome.sessionGone) update(i) { it.copy(sessionId = null, targetUri = null) }
                                                if (!outcome.network) update(i) { it.copy(serverRetries = it.serverRetries + 1) }
                                                stop.compareAndSet(null, outcome)
                                                synchronized(lock) { inFlight.remove(i) }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            // No network or the server is away: keep the list and the session ids, WorkManager
            // runs the batch again later. A missing network never ends the batch; a file that
            // keeps failing on the server is given up alone after a few tries (above).
            stop.get()?.let { outcome ->
                if (outcome.network) return Result.retry()
                // Server trouble: a run that finished nothing counts; a lasting server-wide
                // problem ends the batch instead of waiting for weeks.
                val idle = synchronized(lock) { !progressed }
                val runs = if (idle) batch.serverRetries + 1 else 0
                if (runs < MAX_IDLE_RUNS) {
                    synchronized(lock) { batch = batch.copy(serverRetries = runs).also(queue::save) }
                    return Result.retry()
                }
                var anyFailed = false
                batch.items.forEachIndexed { i, item ->
                    if (item.state == UploadItem.State.Pending) {
                        val end = endSession(app, item, parentUri)
                        if (end != Resume.Done) anyFailed = true
                        update(i) {
                            it.copy(
                                state = if (end == Resume.Done) UploadItem.State.Done else UploadItem.State.Failed,
                                sessionId = null, targetUri = null, unknownOutcome = end == Resume.Unknown,
                            )
                        }
                    }
                }
                if (anyFailed) lastError = outcome.message
            }
        } catch (e: CancellationException) {
            // Stopped: by the person (cancel), or by the system, which runs the job again later.
            // A system stop keeps the sessions: the next run checks them first (see above).
            withContext(NonCancellable) {
                stagingFiles().forEach { it.delete() }
                if (isCancelledByPerson()) {
                    batch.items.forEachIndexed { i, item ->
                        if (item.state == UploadItem.State.Pending && item.sessionId != null && cancelSession(app, item)) {
                            update(i) { it.copy(sessionId = null, targetUri = null) }
                        }
                    }
                    finish(queue, batch)
                }
            }
            throw e
        } finally {
            notifications.cancel(notificationId)
        }

        // Fylvo: an upload whose outcome is not known is told whatever else happened (kept in the batch).
        batch.items.firstOrNull { it.unknownOutcome }?.let { lastError = applicationContext.localized().getString(R.string.upload_outcome_unknown, it.name) }
        finish(queue, batch)
        val output = workDataOf(
            KEY_PARENT_URI to parentUri,
            KEY_UPLOADED to batch.uploaded,
            KEY_FAILED to batch.failed,
            KEY_ERROR to lastError,
        )
        // Always success: the counts tell what happened. A failed job would also fail every job
        // that waits for it in WorkManager.
        return Result.success(output)
    }

    /**
     * An upload queued by 1.0.0 (the file list in the job input): turn it into a batch, under the
     * job's own id, so a later run of the same job goes on with the same batch.
     */
    private fun legacyBatch(queue: UploadQueue): UploadBatch? {
        val uris = inputData.getStringArray(LEGACY_KEY_URIS)?.filterNotNull() ?: return null
        queue.load(id.toString())?.let { return it }
        val parent = inputData.getString(KEY_PARENT_URI) ?: return null
        val names = inputData.getStringArray(LEGACY_KEY_NAMES)?.filterNotNull()?.takeIf { it.size == uris.size }
            ?: uris.map { resolveDisplayName(Uri.parse(it)) }
        return queue.create(uris, names, parent, inputData.getBoolean(LEGACY_KEY_OVERWRITE, false), id = id.toString())
    }

    /** Required for expedited work below Android 12, which starts as a foreground service. */
    override suspend fun getForegroundInfo(): ForegroundInfo {
        createChannel()
        return foregroundInfo("", 0, 0, 0, 0)
    }

    private fun stagingFiles(): List<File> =
        File(applicationContext.cacheDir, "upload_staging").listFiles { f -> f.name.startsWith("$id-") }.orEmpty().toList()

    /** Last change of the picked file as the provider reports it, or null. */
    private fun lastModifiedOf(uri: Uri): Long? = runCatching {
        applicationContext.contentResolver.query(uri, arrayOf(android.provider.DocumentsContract.Document.COLUMN_LAST_MODIFIED), null, null, null)?.use {
            if (it.moveToFirst() && !it.isNull(0)) it.getLong(0).takeIf { t -> t > 0 } else null
        }
    }.getOrNull()

    /** Size of the picked file, or 0 when the provider does not say. */
    private fun sizeOf(item: UploadItem): Long = runCatching {
        applicationContext.contentResolver.query(Uri.parse(item.uri), arrayOf(android.provider.OpenableColumns.SIZE), null, null, null)?.use {
            if (it.moveToFirst() && !it.isNull(0)) it.getLong(0) else 0L
        } ?: 0L
    }.getOrDefault(0L)

    /**
     * How many files go up at once. More than one fills the pauses between the small requests
     * of each file (session, parts, finish) on a good line; on mobile data two keep a slow line
     * from being split too thin.
     */
    private fun parallelUploads(): Int {
        val cm = applicationContext.getSystemService(Context.CONNECTIVITY_SERVICE) as android.net.ConnectivityManager
        return if (cm.isActiveNetworkMetered) PARALLEL_METERED else PARALLEL_UNMETERED
    }

    private sealed interface Outcome {
        data object Done : Outcome
        /** This file cannot be uploaded (the server said no, or the file can no longer be read). */
        data class Failed(val message: String) : Outcome
        /**
         * Network ([network]) or server trouble: the whole batch should wait and try again.
         * [sessionGone]: the server no longer has the session and stored nothing (40011).
         */
        data class TryLater(val message: String, val network: Boolean, val sessionGone: Boolean = false) : Outcome
        /** Fylvo: connected storages: it is not known whether the file arrived (40927). Never sent again. */
        data object Unknown : Outcome
    }

    /** Fylvo: what to do with the session of an upload into a connected storage. */
    sealed interface Resume {
        data object Done : Resume
        /** Nothing was stored: send the file again. */
        data object Resend : Resume
        data object Unknown : Resume
        data class Failed(val code: Int) : Resume
        /** No clear answer now: ask again in a later run. */
        data object Later : Resume
    }

    /**
     * Fylvo: where the upload of [item] into a connected storage stands, from the server's state
     * route. A session still receiving is cancelled first; a cancel also answers 0 when the upload
     * finished meanwhile, so the state is read again after it. Never guesses: anything unclear is
     * [Resume.Later] (network, server trouble) or [Resume.Unknown].
     */
    private suspend fun resolveConnected(app: FylvoApp, item: UploadItem): Resume {
        val session = item.sessionId ?: return Resume.Resend
        return try {
            connectedResume(app.fileRepository.connected.uploadState(session))?.let { return it }
            val target = item.targetUri ?: return Resume.Unknown
            when (val cancel = app.fileRepository.cancelUploadStrict(session, target)) {
                null -> return Resume.Later
                0, ApiException.CODE_UPLOAD_SESSION_EXPIRED, ApiException.CODE_PATH_NOT_EXIST -> Unit
                // HTTP statuses of a proxy or a busy server (404 aside) and login trouble: ask again later.
                ApiException.CODE_LOGIN_REQUIRED, Connected.CODE_BUSY, ApiException.CODE_LOCK_CONFLICT,
                in 400..403, in 405..599, in 50000..59999 -> return Resume.Later
                else -> return Resume.Unknown
            }
            connectedResume(app.fileRepository.connected.uploadState(session)) ?: Resume.Later
        } catch (e: CancellationException) {
            throw e
        } catch (_: java.io.IOException) {
            Resume.Later
        } catch (_: kotlinx.serialization.SerializationException) {
            // Not the server's answer (a login page of a public Wi-Fi): ask again later.
            Resume.Later
        } catch (e: ApiException) {
            if (e.isTransient || e.code == Connected.CODE_BUSY) Resume.Later else Resume.Unknown
        }
    }

    /**
     * Ends the server session of [item] before the file is given up. Fylvo: inside a connected
     * storage it is resolved first, so an upload that arrived counts as done and an unclear one
     * as unknown; elsewhere it is cancelled as before.
     */
    private suspend fun endSession(app: FylvoApp, item: UploadItem, parentUri: String): Resume {
        if (item.sessionId == null) return Resume.Resend
        if (!Connected.isConnected(parentUri)) {
            cancelSession(app, item)
            return Resume.Resend
        }
        return when (val r = resolveConnected(app, item)) {
            Resume.Later -> Resume.Unknown
            else -> r
        }
    }

    private suspend fun uploadOne(
        app: FylvoApp,
        item: UploadItem,
        parentUri: String,
        overwrite: Boolean,
        onSession: (String, String, Long) -> Unit,
        onProgress: (sent: Long, size: Long) -> Unit,
    ): Outcome {
        val uri = Uri.parse(item.uri)
        val tmp = try {
            withContext(Dispatchers.IO) { stageToCache(uri) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Gone, or the picker's permission ended (e.g. after the app was killed).
            Log.w(LOG_TAG, "Cannot read a picked file: ${e.javaClass.simpleName}")
            return Outcome.Failed(applicationContext.localized().getString(R.string.could_not_read_file, item.name))
        }
        val connected = Connected.isConnected(parentUri)
        // Fylvo: a name with "/" (from the picked file's provider) would address another folder there.
        if (connected && '/' in item.name) {
            tmp.delete()
            return Outcome.Failed(
                applicationContext.localized().getString(
                    R.string.could_not_upload_reason, item.name, applicationContext.localized().getString(R.string.error_bad_name),
                )
            )
        }
        return try {
            val mime = applicationContext.contentResolver.getType(uri)
            // Fylvo: a connected session that is gone stored nothing (40011): sent again once at once.
            var tries = 0
            while (true) {
                try {
                    app.fileRepository.uploadFile(
                        tmp, parentUri, remoteName = item.name, overwrite = overwrite, mimeType = mime,
                        lastModified = lastModifiedOf(uri) ?: tmp.lastModified(),
                        onSession = { sessionId, target, _ -> onSession(sessionId, target, tmp.length()) }, onProgress = onProgress,
                    )
                    break
                } catch (e: ApiException) {
                    if (!connected || e.code != ApiException.CODE_UPLOAD_SESSION_EXPIRED || ++tries > 1) throw e
                }
            }
            Outcome.Done
        } catch (e: CancellationException) {
            throw e
        } catch (e: java.io.IOException) {
            Log.w(LOG_TAG, "Upload interrupted: ${e.javaClass.simpleName}")
            Outcome.TryLater(applicationContext.localized().getString(R.string.upload_waits_for_network), network = true)
        } catch (e: ApiException) {
            Log.w(LOG_TAG, "Upload refused: code ${e.code}")
            if (connected) {
                when {
                    e.code == Connected.CODE_OUTCOME_UNKNOWN -> return Outcome.Unknown
                    e.code == ApiException.CODE_UPLOAD_SESSION_EXPIRED ->
                        return Outcome.TryLater(describe(e, item.name), network = false, sessionGone = true)
                    // Busy, or the name is held for a while after an unknown outcome: wait.
                    e.code == Connected.CODE_BUSY -> return Outcome.TryLater(describe(e, item.name), network = false)
                }
            }
            // The proxy cannot reach the NAS (502/503/504): like a lost network, never counted
            // against the file.
            if (e.code in 502..504) return Outcome.TryLater(applicationContext.localized().getString(R.string.upload_waits_for_network), network = true)
            if (e.isTransient) Outcome.TryLater(describe(e, item.name), network = false) else Outcome.Failed(describe(e, item.name))
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

    /** Cancelled by the person (or sign-out), as opposed to a stop by the system. Any Android version. */
    private suspend fun isCancelledByPerson(): Boolean = withContext(Dispatchers.IO) {
        runCatching {
            val info = WorkManager.getInstance(applicationContext).getWorkInfoById(id).get()
            // Gone means cancelled and already pruned by the screen.
            info == null || info.state == WorkInfo.State.CANCELLED
        }.getOrDefault(false)
    }

    /** The batch is over: forget its list and give back read permissions no other upload needs. */
    private fun finish(queue: UploadQueue, batch: UploadBatch) {
        queue.delete(batch.id)
        releaseUnused(applicationContext, queue, batch.items.map { it.uri })
    }

    /**
     * Pushes progress to WorkManager and (throttled) to the notification.
     * Called from OkHttp's write loop, hence the non-suspending Async variants.
     */
    private fun report(
        name: String, done: Int, total: Int, permille: Int, inFlight: Int, parentUri: String, force: Boolean = false,
    ) {
        setProgressAsync(
            workDataOf(
                KEY_PARENT_URI to parentUri,
                KEY_PROGRESS_NAME to name,
                KEY_PROGRESS_INDEX to done,
                KEY_PROGRESS_TOTAL to total,
                KEY_PROGRESS_PERMILLE to permille,
            )
        )
        val now = System.currentTimeMillis()
        if (force || now - lastNotified >= NOTIFY_INTERVAL_MS) {
            lastNotified = now
            setForegroundAsync(foregroundInfo(name, done, total, done * 1000L + permille, total * 1000L))
        }
    }

    /** Server error codes read like log lines; say what happened to this file instead. */
    private fun describe(e: Exception, name: String): String {
        val texts = applicationContext.localized()
        return when {
            e is ApiException && e.code == CODE_OBJECT_EXISTS -> texts.getString(R.string.file_exists_in_folder, name)
            e is ApiException && e.code == CODE_QUOTA_EXCEEDED -> texts.getString(R.string.not_enough_storage, name)
            // Fylvo: codes of connected storages (and names refused there) say what happened.
            e is ApiException && e.code in CONNECTED_REASONS -> texts.getString(R.string.could_not_upload_reason, name, ErrorText.of(e).resolve(texts))
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
        val tmp = File(dir, "$id-${staged.getAndIncrement()}")
        applicationContext.contentResolver.openInputStream(uri)!!.use { input ->
            FileOutputStream(tmp).use { out -> input.copyTo(out, 256 * 1024) }
        }
        return tmp
    }

    private val staged = AtomicInteger(0)

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
            ForegroundInfo(notificationId, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(notificationId, notification)
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
        const val PARENT_TAG_PREFIX = "upload-parent:"
        private const val BATCH_TAG_PREFIX = "upload-batch:"
        // Input of jobs queued by 1.0.0.
        private const val LEGACY_KEY_URIS = "uris"
        private const val LEGACY_KEY_NAMES = "names"
        private const val LEGACY_KEY_OVERWRITE = "overwrite"
        private const val MAX_ATTEMPTS = 8
        /** Runs in a row with server trouble and no finished file before a batch ends. */
        private const val MAX_IDLE_RUNS = 16
        /** Files at once on Wi-Fi or another unmetered line, and on mobile data. */
        private const val PARALLEL_UNMETERED = 3
        private const val PARALLEL_METERED = 2
        /** Files larger than this upload alone (their buffer is a whole chunk). */
        private const val BIG_FILE_BYTES = 16L * 1024 * 1024
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
        /** Sum of the files in flight, each in thousandths (0 to 1000). */
        const val KEY_PROGRESS_PERMILLE = "permille"
        private const val CHANNEL_ID = "uploads"
        private const val NOTIF_ID_BASE = 0x10000
        private const val NOTIFY_INTERVAL_MS = 500L
        // pkg/serializer/error.go
        private const val CODE_OBJECT_EXISTS = 40004
        /** Fylvo: codes whose own sentence is shown for a failed upload. */
        private val CONNECTED_REASONS = setOf(40052, 40076, 40905, 40920, 40921, 40922, 40923, 40924, 40925, 40927)
        private const val CODE_QUOTA_EXCEEDED = 40051

        /**
         * Queues an upload of [uris] into [parentUri]. The list goes into a file of the app
         * ([UploadQueue]), the job only carries its id: any number of files fits. The read
         * permission of each picked file is kept beyond the app's lifetime where Android allows
         * it, so the upload can go on after the app was closed or killed. Each batch is its own
         * job and sends up to three files at a time (two on mobile data).
         */
        fun enqueue(
            context: Context,
            uris: List<Uri>,
            parentUri: String,
            names: List<String>? = null,
            overwrite: Boolean = false,
            overwriteOnly: Set<String>? = null,
            /** Fylvo: the target folder's id, checked before every file (sharing into Fylvo). */
            expectedParentId: String? = null,
        ) {
            if (uris.isEmpty()) return
            // Shared files (no folder listing here): two with the same name would race for one
            // address, so they get unique names. Picked files come already renamed by the screen,
            // which also knows the names in the folder.
            val fileNames = names ?: uris.map { displayName(context, it) }.let { list ->
                val taken = mutableSetOf<String>()
                list.map { n -> uniqueName(n, taken).also { taken += it } }
            }
            // Keep the read permissions while Android's cap allows it with room to spare (it drops
            // the oldest ones beyond, e.g. the folder-sync folder). Only picker documents can be
            // kept; without it a file can still be read until the app is closed.
            val room = grantBudget() - context.contentResolver.persistedUriPermissions.size
            uris.take(room.coerceAtLeast(0)).forEach { uri ->
                runCatching { context.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            }
            val account = kotlinx.coroutines.runBlocking { currentAccount(context.applicationContext as FylvoApp) }
            var batch = queue(context).create(uris.map { it.toString() }, fileNames, parentUri, overwrite, account = account)
            if (overwrite && overwriteOnly != null) {
                batch = batch.copy(items = batch.items.map { it.copy(overwrite = it.name in overwriteOnly) })
                queue(context).save(batch)
            }
            if (expectedParentId != null) {
                batch = batch.copy(expectedParentId = expectedParentId)
                queue(context).save(batch)
            }
            val request = OneTimeWorkRequestBuilder<UploadWorker>()
                .addTag(TAG)
                .addTag(BATCH_TAG_PREFIX + batch.id)
                // The target folder as a tag: a waiting job has no progress data to read it from.
                .addTag(PARENT_TAG_PREFIX + parentUri)
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
                .setInputData(workDataOf(KEY_BATCH_ID to batch.id, KEY_PARENT_URI to parentUri))
                .build()
            // Each batch is its own job: cancelling or failing one never touches another.
            WorkManager.getInstance(context).enqueue(request).result.get()
        }

        /** The signed-in account as "server email", to bind a batch to it. */
        suspend fun currentAccount(app: FylvoApp): String =
            "${app.sessionManager.serverUrlNow()} ${app.sessionManager.userEmail.first()}"

        /**
         * Cancels a batch. A job that waits (for the network) runs no code on cancel, so its
         * server session, list and permissions are cleaned up here. Call off the main thread.
         */
        suspend fun cancel(context: Context, workId: java.util.UUID) {
            val wm = WorkManager.getInstance(context)
            val info = runCatching { wm.getWorkInfoById(workId).get() }.getOrNull()
            wm.cancelWorkById(workId).result.get()
            if (info == null) return
            // A running worker cleans up itself; if it ended just before the cancel arrived, nobody
            // would, so look again after it had time to finish.
            if (info.state == WorkInfo.State.RUNNING) kotlinx.coroutines.delay(10_000)
            // A job queued by 1.0.0 keeps its batch under the job's own id.
            val batchId = info.tags.firstOrNull { it.startsWith(BATCH_TAG_PREFIX) }?.removePrefix(BATCH_TAG_PREFIX) ?: workId.toString()
            val queue = queue(context)
            val batch = queue.load(batchId) ?: return // already cleaned up by the worker
            val app = context.applicationContext as FylvoApp
            batch.items.forEach { item ->
                val sessionId = item.sessionId
                val target = item.targetUri
                if (sessionId != null && target != null) app.fileRepository.deleteUploadSession(sessionId, target)
            }
            queue.delete(batchId)
            releaseUnused(context, queue, batch.items.map { it.uri })
            File(context.cacheDir, "upload_staging").listFiles { f -> f.name.startsWith("$workId-") }?.forEach { it.delete() }
        }

        /**
         * Sign-out: stops all uploads and cancels their open server sessions while the sign-in is
         * still valid, so no locked 0 B placeholders stay behind. Then forgets the lists.
         */
        suspend fun cancelAllForSignOut(context: Context) {
            WorkManager.getInstance(context).cancelAllWorkByTag(TAG).result.get()
            val app = context.applicationContext as FylvoApp
            val queue = queue(context)
            queue.ids().forEach { id ->
                queue.load(id)?.items?.forEach { item ->
                    val sessionId = item.sessionId
                    val target = item.targetUri
                    if (item.state == UploadItem.State.Pending && sessionId != null && target != null) {
                        app.fileRepository.deleteUploadSession(sessionId, target)
                    }
                }
                queue.delete(id)
            }
        }

        /**
         * [enqueue] for screens: never throws. If the upload cannot be queued (for example the
         * phone's storage is full), the person gets a short message instead of a crash.
         */
        fun enqueueSafely(
            context: Context,
            uris: List<Uri>,
            parentUri: String,
            names: List<String>? = null,
            overwrite: Boolean = false,
            overwriteOnly: Set<String>? = null,
            expectedParentId: String? = null,
        ) {
            try {
                enqueue(context, uris, parentUri, names, overwrite, overwriteOnly, expectedParentId)
            } catch (e: Exception) {
                Log.w(LOG_TAG, "Could not queue an upload: ${e.javaClass.simpleName}")
                android.os.Handler(android.os.Looper.getMainLooper()).post {
                    android.widget.Toast.makeText(context, context.localized().getString(R.string.upload_could_not_start), android.widget.Toast.LENGTH_LONG).show()
                }
            }
        }

        /** Below Android 11 the cap is 128 kept permissions, from Android 11 on 512. */
        private fun grantBudget() = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) 400 else 100

        /** Gives back the read permission of [uris] unless a waiting upload still lists them. */
        fun releaseUnused(context: Context, queue: UploadQueue, uris: List<String>) {
            val stillNeeded = queue.ids().flatMap { id -> queue.load(id)?.items?.map { it.uri }.orEmpty() }.toSet()
            uris.filter { it !in stillNeeded }.forEach { uri ->
                runCatching {
                    context.contentResolver.releasePersistableUriPermission(Uri.parse(uri), Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
            }
        }

        fun queue(context: Context) = UploadQueue(File(context.filesDir, "upload_batches"))

        /**
         * Lists no job will finish any more (older than a week and without a waiting or running
         * job), with their permissions. Runs off the main thread.
         */
        fun cleanUpOldBatches(context: Context) {
            val queue = queue(context)
            val live = WorkManager.getInstance(context).getWorkInfosByTag(TAG).get()
                .filter { !it.state.isFinished }
                .flatMap { info -> info.tags.filter { it.startsWith(BATCH_TAG_PREFIX) }.map { it.removePrefix(BATCH_TAG_PREFIX) } }
                .toSet()
            queue.idsOlderThan(TimeUnit.DAYS.toMillis(7)).filter { it !in live }.forEach { id ->
                val uris = queue.load(id)?.items?.map { it.uri }.orEmpty()
                queue.delete(id)
                releaseUnused(context, queue, uris)
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

        /**
         * Names of one selection: the first file of a name keeps it (a conflict with the folder is
         * then asked about), further files with the same name get a name that is free both in the
         * selection and in the folder ([existing]), so no rename can hit a file in the folder.
         */
        fun uniqueWithinSelection(names: List<String>, existing: Set<String>): List<String> {
            val seen = mutableSetOf<String>()
            val taken = existing.toMutableSet()
            return names.map { n ->
                val name = if (n in seen) uniqueName(n, taken + seen) else n
                seen += name
                taken += name
                name
            }
        }

        /**
         * Whether an upload whose run was stopped had in fact finished: while its session exists,
         * its placeholder holds the name and has size 0 until the upload completes, so the full
         * size there means it arrived. Not for Replace, where the old version has a size too.
         */
        fun finishedBeforeStop(replace: Boolean, uploadSize: Long, serverSize: Long?): Boolean =
            !replace && uploadSize > 0 && serverSize == uploadSize

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
    /** Stopped for lack of network (or server) and waiting to go on by itself. */
    val waitingForNetwork: Boolean = false,
    /** Sum of the files in flight, each in thousandths. */
    val permille: Int = 0,
) {
    val active: Boolean get() = !state.isFinished
    /** Whole-batch fraction: finished files plus the parts of the files in flight. */
    val fraction: Float?
        get() {
            if (total <= 0) return null
            if (index == 0 && permille == 0) return null
            return ((index + permille / 1000f) / total).coerceIn(0f, 1f)
        }

    companion object {
        fun from(info: WorkInfo): UploadStatus {
            val p = info.progress
            val o = info.outputData
            return UploadStatus(
                id = info.id,
                state = info.state,
                parentUri = p.getString(UploadWorker.KEY_PARENT_URI) ?: o.getString(UploadWorker.KEY_PARENT_URI)
                    ?: info.tags.firstOrNull { it.startsWith(UploadWorker.PARENT_TAG_PREFIX) }?.removePrefix(UploadWorker.PARENT_TAG_PREFIX)
                    ?: "",
                name = p.getString(UploadWorker.KEY_PROGRESS_NAME) ?: "",
                index = p.getInt(UploadWorker.KEY_PROGRESS_INDEX, 0),
                total = p.getInt(UploadWorker.KEY_PROGRESS_TOTAL, 0),
                sent = p.getLong(UploadWorker.KEY_PROGRESS_SENT, 0),
                size = p.getLong(UploadWorker.KEY_PROGRESS_SIZE, 0),
                uploaded = o.getInt(UploadWorker.KEY_UPLOADED, 0),
                failed = o.getInt(UploadWorker.KEY_FAILED, 0),
                error = o.getString(UploadWorker.KEY_ERROR),
                waitingForNetwork = info.state == WorkInfo.State.ENQUEUED && info.runAttemptCount > 0,
                permille = p.getInt(UploadWorker.KEY_PROGRESS_PERMILLE, 0),
            )
        }
    }
}

/**
 * Fylvo: the decision for one answer of the state route of a connected upload (unit-tested).
 * Null: still receiving, the session has to be cancelled before anything is decided.
 */
internal fun connectedResume(state: ConnectedUploadState): UploadWorker.Resume? = when (state.state) {
    UPLOAD_DONE -> UploadWorker.Resume.Done
    UPLOAD_FAILED -> when (val code = state.code) {
        // Nothing was stored (session gone or cancelled), or the provider was busy: send again.
        ApiException.CODE_UPLOAD_SESSION_EXPIRED, Connected.CODE_BUSY -> UploadWorker.Resume.Resend
        // No code: not clear what happened, never sent again.
        null -> UploadWorker.Resume.Unknown
        Connected.CODE_OUTCOME_UNKNOWN -> UploadWorker.Resume.Unknown
        else -> UploadWorker.Resume.Failed(code)
    }
    UPLOAD_RECEIVING -> null
    else -> UploadWorker.Resume.Unknown
}
