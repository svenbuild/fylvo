package io.github.svenbuild.fylvo.android.data

import android.app.ActivityManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.Signature
import android.provider.Settings
import androidx.annotation.StringRes
import androidx.core.content.FileProvider
import androidx.core.net.toUri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.AndroidRelease
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ApiResponse
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest

/**
 * Fylvo: in-app updates from the own server (the app is sideloaded, there is no store).
 * [check] asks the server for the newest release, [download] fetches and verifies the APK,
 * [install] hands it to the system installer, only after a tap while the app is in front.
 * The shared [state] drives the Account screen and the dot on the Account tab.
 */
class AppUpdater(private val context: Context, private val api: ApiClient) {

    sealed interface State {
        data object Idle : State
        data object Checking : State
        data object UpToDate : State
        data object CheckFailed : State
        /** [error] is a short text after a failed download or install attempt. */
        data class Available(val release: AndroidRelease, @StringRes val error: Int? = null) : State
        data class Downloading(val release: AndroidRelease, val percent: Int) : State
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val _state = MutableStateFlow<State>(State.Idle)
    val state: StateFlow<State> = _state.asStateFlow()

    private var job: Job? = null
    private var checkedThisStart = false
    /** The system installer was opened and the person has not come back yet. */
    private var installerOpened = false

    private val updatesDir get() = File(context.cacheDir, "updates")

    /** The installed versionCode (from the package manager, the source of truth). */
    private val installedVersionCode: Long
        get() = context.packageManager.getPackageInfo(context.packageName, 0).longVersionCode

    /**
     * Once per app start after sign-in: removes update files that are already installed and
     * looks for a new release. Quiet: only sets the state (and so the tab dot), never installs.
     */
    fun checkOnStart() {
        if (checkedThisStart) return
        checkedThisStart = true
        runCheck(quiet = true)
    }

    /** The manual "check for updates" action. */
    fun checkNow() = runCheck(quiet = false)

    private fun runCheck(quiet: Boolean) {
        if (job?.isActive == true) return
        val before = _state.value
        if (!quiet) _state.value = State.Checking
        job = scope.launch {
            if (quiet) withContext(Dispatchers.IO) { deleteStaleFiles() }
            _state.value = try {
                check()?.let { State.Available(it) } ?: if (quiet) before else State.UpToDate
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                if (quiet) before else State.CheckFailed
            }
        }
    }

    /**
     * The "Install" tap: downloads (or reuses an already verified file), checks the package and
     * starts the system installer. A second tap while this runs does nothing.
     */
    fun downloadAndInstall() {
        val release = (_state.value as? State.Available)?.release ?: return
        if (job?.isActive == true) return
        job = scope.launch {
            val file = try {
                download(release) { percent -> _state.value = State.Downloading(release, percent) }
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                _state.value = when (e) {
                    is ReleaseChangedException -> {
                        // Look again; the user retries with what the server offers now.
                        try {
                            check()?.let { State.Available(it, R.string.update_changed) } ?: State.UpToDate
                        } catch (_: Exception) {
                            State.Available(release, R.string.update_download_failed)
                        }
                    }
                    is ChecksumException -> State.Available(release, R.string.update_checksum_failed)
                    else -> State.Available(release, R.string.update_download_failed)
                }
                return@launch
            }
            if (!withContext(Dispatchers.IO) { isValidPackage(file, release) }) {
                file.delete()
                _state.value = State.Available(release, R.string.update_invalid)
                return@launch
            }
            // The download may have taken a while; the installer only opens over the app itself.
            // Otherwise the verified file waits and the next tap reuses it.
            val error = if (isInForeground()) install(file) else null
            _state.value = State.Available(release, error)
        }
    }

    /**
     * Back in the app after the "install unknown apps" setting: if the user allowed it there,
     * go on with the install they asked for instead of leaving the hint on screen.
     */
    fun onResume() {
        val s = _state.value as? State.Available ?: return
        // Back from the installer, but the old version still runs: cancelled, or blocked (e.g. by
        // Play Protect). Say so once; the update stays offered, nothing starts by itself.
        if (installerOpened) {
            installerOpened = false
            if (installedVersionCode < s.release.versionCode && s.error == null) {
                _state.value = s.copy(error = R.string.update_not_installed)
            }
            return
        }
        if (s.error == R.string.update_allow_install && context.packageManager.canRequestPackageInstalls()) {
            _state.value = s.copy(error = null)
            downloadAndInstall()
        }
    }

    /** Sign-out: forget the state so the next account starts fresh (files go with cacheDir). */
    fun reset() {
        job?.cancel()
        job = null
        checkedThisStart = false
        _state.value = State.Idle
    }

    /** The newer release the server offers, or null when none is published or it is not newer. */
    suspend fun check(): AndroidRelease? {
        val resp = api.service().androidLatest()
        if (resp.code() == 404) return null
        if (!resp.isSuccessful) throw ApiException(resp.code(), "Update check failed: HTTP ${resp.code()}")
        val release = releaseFrom(resp.body() ?: throw ApiException(-1, "Empty answer"))
        return release?.takeIf { isUpdate(it, installedVersionCode) }
    }

    /**
     * Streams the APK to `cacheDir/updates/fylvo-<code>.apk` through a `.part` file, stops when
     * more than the advertised size arrives, checks size and SHA-256 (a mismatch deletes it and
     * throws [ChecksumException]) and removes other files in that folder. A file that is already
     * there and verifies (e.g. after the "install unknown apps" grant restarted the app) is reused.
     */
    suspend fun download(release: AndroidRelease, onProgress: (Int) -> Unit): File = withContext(Dispatchers.IO) {
        val dir = updatesDir.apply { mkdirs() }
        val dest = File(dir, fileName(release.versionCode.toLong()))
        dir.listFiles()?.filter { it != dest }?.forEach { it.delete() }
        if (dest.exists() && verifyFile(dest, release.size, release.sha256)) return@withContext dest
        dest.delete()

        // No redirects: the bearer token must not follow one to another server.
        val resp = api.authService().androidApk(release.sha256)
        if (resp.code() == 404) throw ReleaseChangedException()
        if (!resp.isSuccessful) throw ApiException(resp.code(), "Download failed: HTTP ${resp.code()}")
        val body = resp.body() ?: throw ApiException(-1, "Empty download")
        // A JSON envelope (or anything else) instead of the APK: the release changed meanwhile.
        val type = body.contentType()?.let { "${it.type}/${it.subtype}" }
        if (type != APK_MIME) {
            body.close()
            throw ReleaseChangedException()
        }
        val part = File(dir, dest.name + ".part")
        try {
            body.byteStream().use { input ->
                FileOutputStream(part).use { out ->
                    val buffer = ByteArray(256 * 1024)
                    var copied = 0L
                    var lastPercent = -1
                    while (true) {
                        currentCoroutineContext().ensureActive()
                        val n = input.read(buffer)
                        if (n < 0) break
                        copied += n
                        if (copied > release.size) throw ChecksumException()
                        out.write(buffer, 0, n)
                        val percent = (copied * 100 / release.size).toInt()
                        if (percent != lastPercent) {
                            lastPercent = percent
                            withContext(Dispatchers.Main) { onProgress(percent) }
                        }
                    }
                }
            }
            if (!verifyFile(part, release.size, release.sha256)) throw ChecksumException()
            if (!part.renameTo(dest)) throw ApiException(-1, "Could not save ${dest.name}")
        } finally {
            part.delete()
        }
        dest
    }

    /**
     * True when [file] is a Fylvo APK with exactly the advertised version, newer than the installed
     * one, and signed with the same certificates as the installed app.
     */
    @Suppress("DEPRECATION") // The Int flag overloads are the only ones below Android 13.
    private fun isValidPackage(file: File, release: AndroidRelease): Boolean {
        val pm = context.packageManager
        val archive = pm.getPackageArchiveInfo(file.path, PackageManager.GET_SIGNING_CERTIFICATES) ?: return false
        val installed = pm.getPackageInfo(context.packageName, PackageManager.GET_SIGNING_CERTIFICATES)
        val archiveSigners = archive.signingInfo?.apkContentsSigners ?: return false
        val installedSigners = installed.signingInfo?.apkContentsSigners ?: return false
        return archive.packageName == context.packageName &&
            archive.longVersionCode == release.versionCode.toLong() &&
            archive.longVersionCode > installed.longVersionCode &&
            sameSigners(archiveSigners, installedSigners)
    }

    /**
     * Opens the system installer for [file]; returns null on success or a short error text.
     * When Fylvo may not install apps yet, opens the "install unknown apps" setting instead;
     * the user then taps Install again and the verified file is reused.
     */
    private fun install(file: File): Int? {
        if (!context.packageManager.canRequestPackageInstalls()) {
            val settings = Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, "package:${context.packageName}".toUri())
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            runCatching { context.startActivity(settings) }
            return R.string.update_allow_install
        }
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, APK_MIME)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        return try {
            context.startActivity(intent)
            installerOpened = true
            null
        } catch (_: ActivityNotFoundException) {
            R.string.update_download_failed
        }
    }

    private fun isInForeground(): Boolean {
        val info = ActivityManager.RunningAppProcessInfo()
        ActivityManager.getMyMemoryState(info)
        return info.importance == ActivityManager.RunningAppProcessInfo.IMPORTANCE_FOREGROUND
    }

    /** Removes update files that are not an update any more (installed already) or unknown. */
    private fun deleteStaleFiles() {
        val installed = installedVersionCode
        updatesDir.listFiles()?.forEach { file ->
            val code = versionCodeOf(file.name)
            if (code == null || code <= installed) file.delete()
        }
    }

    class ChecksumException : Exception("Size or checksum of the update does not match")
    class ReleaseChangedException : Exception("The release changed on the server")

    companion object {
        const val APK_MIME = "application/vnd.android.package-archive"
        private val SHA256_HEX = Regex("^[0-9a-f]{64}$")
        private val FILE_NAME = Regex("^fylvo-(\\d+)\\.apk$")

        fun fileName(versionCode: Long) = "fylvo-$versionCode.apk"

        /** Version code in an update file name, null for any other file (e.g. a `.part` leftover). */
        fun versionCodeOf(name: String): Long? = FILE_NAME.matchEntire(name)?.groupValues?.get(1)?.toLongOrNull()

        /** The release in a `latest` envelope; null for code 404 (nothing published). */
        fun releaseFrom(envelope: ApiResponse<AndroidRelease>): AndroidRelease? = when (envelope.code) {
            0 -> envelope.data
            404 -> null
            else -> throw ApiException(envelope.code, envelope.msg.ifEmpty { "Update check failed" })
        }

        /** Newer than [installedCode] and complete enough to verify after the download. */
        fun isUpdate(release: AndroidRelease, installedCode: Long): Boolean =
            release.versionCode > installedCode && release.size > 0 && SHA256_HEX.matches(release.sha256)

        /** True when [file] has exactly [size] bytes and the SHA-256 [sha256] (hex, any case). */
        fun verifyFile(file: File, size: Long, sha256: String): Boolean {
            if (!file.isFile || file.length() != size) return false
            val digest = MessageDigest.getInstance("SHA-256")
            file.inputStream().use { input ->
                val buffer = ByteArray(256 * 1024)
                while (true) {
                    val n = input.read(buffer)
                    if (n < 0) break
                    digest.update(buffer, 0, n)
                }
            }
            return digest.digest().toHex() == sha256.lowercase()
        }

        /** Same set of signing certificates, compared by the SHA-256 of their bytes. */
        fun sameSigners(a: Array<Signature>, b: Array<Signature>): Boolean {
            fun hashes(s: Array<Signature>) = s.map { MessageDigest.getInstance("SHA-256").digest(it.toByteArray()).toHex() }.toSet()
            return a.isNotEmpty() && hashes(a) == hashes(b)
        }

        private fun ByteArray.toHex() = joinToString("") { "%02x".format(it) }
    }
}
