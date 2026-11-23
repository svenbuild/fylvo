package io.github.svenbuild.fylvo.android.data

import android.content.Context
import coil.imageLoader
import coil.memory.MemoryCache
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.Interceptor
import okhttp3.Response
import java.io.File
import java.io.IOException
import java.util.concurrent.Semaphore
import java.util.concurrent.TimeUnit

/**
 * Fylvo 1.6: previews of files in connected storages. Coil keeps them under a key made of the
 * storage, the file and its version ([key]): the address the server hands out changes daily, the
 * picture of a version does not. Each key is noted for its storage before the picture is loaded
 * (one small file per storage, written off the main thread), so disconnecting a storage takes
 * exactly its pictures out of the caches. Sign-out empties the whole cache and these notes.
 */
object ConnectedThumbs {
    private const val DIR = "connected_thumbs"
    /** The part of a picture's address on the server (service/explorer/fylvo_connected_thumb.go). */
    private const val PATH = "/fylvo/connected/thumb/"

    private val io = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    /** Keys already noted, per storage (read from its file on first use). */
    private val noted = mutableMapOf<String, MutableSet<String>>()
    /** Storages disconnected in this run: nothing is noted for them any more. */
    private val gone = mutableSetOf<String>()

    fun key(storage: String, fileId: String, version: String): String = "cthumb:$storage:$fileId:$version"

    /** The storage a [key] belongs to, or null for other keys. */
    fun storageOf(key: String): String? =
        key.takeIf { it.startsWith("cthumb:") }?.removePrefix("cthumb:")?.substringBefore(':')?.takeIf { it.isNotEmpty() }

    /** Called when a picture is about to be loaded under [key]: noted before anything is cached. */
    fun note(context: Context, key: String) {
        val storage = storageOf(key) ?: return
        val app = context.applicationContext
        io.launch {
            synchronized(this@ConnectedThumbs) {
                if (storage in gone) return@launch
                val keys = loaded(app, storage)
                if (keys.add(key)) runCatching { fileOf(app, storage).apply { parentFile?.mkdirs() }.appendText(key + "\n") }
            }
        }
    }

    /**
     * The storage [storage] was disconnected: its pictures leave the memory and disk cache, now and
     * once more a little later (a load that was under way when it was disconnected).
     */
    fun forget(context: Context, storage: String) {
        val app = context.applicationContext
        synchronized(this) { gone += storage }
        io.launch {
            evict(app, storage)
            // The server waits up to 10 s for a picture; one that was on its way is caught here.
            delay(LATE_SWEEP_MS)
            evict(app, storage)
        }
    }

    private fun evict(context: Context, storage: String) {
        val keys = synchronized(this) {
            loaded(context, storage).toSet().also {
                noted.remove(storage)
                fileOf(context, storage).delete()
            }
        }
        val loader = context.imageLoader
        loader.memoryCache?.let { cache -> cache.keys.filter { storageOf(it.key) == storage }.forEach { cache.remove(it) } }
        keys.forEach { k ->
            loader.memoryCache?.remove(MemoryCache.Key(k))
            runCatching { loader.diskCache?.remove(k) }
        }
    }

    /** Sign-out: the caches are emptied as a whole; the notes go too. */
    fun clear(context: Context) {
        synchronized(this) {
            noted.clear()
            gone.clear()
            File(context.filesDir, DIR).deleteRecursively()
        }
    }

    private fun loaded(context: Context, storage: String): MutableSet<String> =
        noted.getOrPut(storage) {
            runCatching { fileOf(context, storage).readLines().filter { it.isNotBlank() }.toMutableSet() }.getOrDefault(mutableSetOf())
        }

    private fun fileOf(context: Context, storage: String) =
        File(File(context.filesDir, DIR), storage.filter { it.isLetterOrDigit() || it == '-' || it == '_' })

    private const val LATE_SWEEP_MS = 20_000L

    // ---- loading the pictures (the image loader's HTTP client) -------------------------------

    /** True for the address of a picture of a connected storage. */
    fun isPicture(path: String): Boolean = PATH in path

    /** The server fetches at most two pictures at a time from a provider: the app loads no more at once. */
    private val slots = Semaphore(2)

    /**
     * Loads pictures of connected storages two at a time; a busy answer (503) is tried again after
     * the time the server names (at most three times, 30 s at most each). Other addresses pass.
     */
    val limiter = Interceptor { chain ->
        val request = chain.request()
        if (!isPicture(request.url.encodedPath)) return@Interceptor chain.proceed(request)
        var attempt = 0
        while (true) {
            // Waiting for a slot ends when the load is cancelled (the row went away).
            while (!slots.tryAcquire(200, TimeUnit.MILLISECONDS)) {
                if (chain.call().isCanceled()) throw IOException("Canceled")
            }
            val response: Response = try {
                chain.proceed(request)
            } finally {
                slots.release()
            }
            if (response.code != 503 || attempt >= 3) return@Interceptor response
            val wait = retryAfterMs(response.header("Retry-After"))
            response.close()
            attempt++
            var waited = 0L
            while (waited < wait) {
                if (chain.call().isCanceled()) throw IOException("Canceled")
                Thread.sleep(200)
                waited += 200
            }
        }
        @Suppress("UNREACHABLE_CODE")
        throw IllegalStateException()
    }

    /** The wait a Retry-After header names, within 1 to 30 seconds (5 when it names none). */
    fun retryAfterMs(header: String?): Long = (header?.trim()?.toLongOrNull() ?: 5L).coerceIn(1, 30) * 1000

    /**
     * The key already names the version, so a picture stays valid as long as Coil keeps it: its
     * answer is kept for 30 days instead of the address's day (a new day loads nothing again).
     */
    val keepLonger = Interceptor { chain ->
        val response = chain.proceed(chain.request())
        if (response.code == 200 && isPicture(chain.request().url.encodedPath)) {
            response.newBuilder().header("Cache-Control", "private, max-age=$KEEP_SECONDS").build()
        } else response
    }

    private const val KEEP_SECONDS = 30L * 24 * 60 * 60
}
