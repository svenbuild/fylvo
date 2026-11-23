package io.github.svenbuild.fylvo.android.data

import android.content.Context
import coil.imageLoader
import coil.intercept.Interceptor
import coil.memory.MemoryCache
import coil.network.HttpException
import coil.request.ErrorResult
import coil.request.ImageResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Fylvo 1.6: previews of files in connected storages. Coil keeps them under a key made of the
 * storage, the file and its version ([key]): the address the server hands out changes daily, the
 * picture of a version does not. [Pictures] loads them: each key is noted for its storage (a small
 * file per storage) before the picture is fetched, and a disconnected storage loads nothing more;
 * disconnecting waits for its loads under way and then takes all its pictures out of the caches.
 * Sign-out empties the whole cache and these notes.
 */
object ConnectedThumbs {
    private const val DIR = "connected_thumbs"
    /** The part of a picture's address on the server (service/explorer/fylvo_connected_thumb.go). */
    private const val PATH = "/fylvo/connected/thumb/"

    private val io = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    /** Keys already noted, per storage (read from its file on first use). */
    private val noted = mutableMapOf<String, MutableSet<String>>()
    /** Storages disconnected in this run: nothing is loaded for them any more. */
    private val gone = mutableSetOf<String>()
    /** Loads under way, per storage. */
    private val active = mutableMapOf<String, Int>()

    fun key(storage: String, fileId: String, version: String): String = "cthumb:$storage:$fileId:$version"

    /** The storage a [key] belongs to, or null for other keys. */
    fun storageOf(key: String): String? =
        key.takeIf { it.startsWith("cthumb:") }?.removePrefix("cthumb:")?.substringBefore(':')?.takeIf { it.isNotEmpty() }

    /** True for the address of a picture of a connected storage. */
    fun isPicture(url: String): Boolean = PATH in url

    /** A load of [key] starts: noted first (on disk); false when its storage was disconnected. */
    private fun begin(context: Context, storage: String, key: String): Boolean = synchronized(this) {
        if (storage in gone) return false
        val keys = notes(context, storage)
        if (keys.add(key)) runCatching { fileOf(context, storage).apply { parentFile?.mkdirs() }.appendText(key + "\n") }
        active[storage] = (active[storage] ?: 0) + 1
        true
    }

    private fun end(storage: String) = synchronized(this) {
        val n = (active[storage] ?: 1) - 1
        if (n > 0) active[storage] = n else active.remove(storage)
    }

    /**
     * The storage [storage] was disconnected: no new load starts for it; once its loads under way
     * are done (at most [DRAIN_MS]), all its pictures leave the memory and disk cache.
     */
    fun forget(context: Context, storage: String) {
        val app = context.applicationContext
        synchronized(this) { gone += storage }
        io.launch {
            var waited = 0L
            while (synchronized(this@ConnectedThumbs) { (active[storage] ?: 0) > 0 } && waited < DRAIN_MS) {
                delay(200)
                waited += 200
            }
            evict(app, storage)
        }
    }

    private fun evict(context: Context, storage: String) {
        val keys = synchronized(this) {
            notes(context, storage).toSet().also {
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

    private fun notes(context: Context, storage: String): MutableSet<String> =
        noted.getOrPut(storage) {
            runCatching { fileOf(context, storage).readLines().filter { it.isNotBlank() }.toMutableSet() }.getOrDefault(mutableSetOf())
        }

    private fun fileOf(context: Context, storage: String) =
        File(File(context.filesDir, DIR), storage.filter { it.isLetterOrDigit() || it == '-' || it == '_' })

    /** The longest a load can take: the server waits up to 10 s, then three waits of 30 s at most. */
    private const val DRAIN_MS = 120_000L

    /** The wait a Retry-After header names, within 1 to 30 seconds (5 when it names none). */
    fun retryAfterMs(header: String?): Long = (header?.trim()?.toLongOrNull() ?: 5L).coerceIn(1, 30) * 1000

    /**
     * Coil's step for pictures of connected storages: noted before loading, refused for a
     * disconnected storage, two network loads at a time (the server fetches each from the provider
     * then; the slot is held until the picture is decoded and stored), a busy answer (503) tried
     * again after Retry-After, at most three times. Waiting holds no thread and ends with the row.
     */
    object Pictures : Interceptor {
        private val slots = Semaphore(2)

        override suspend fun intercept(chain: Interceptor.Chain): ImageResult {
            val request = chain.request
            val url = request.data as? String ?: return chain.proceed(request)
            val key = request.diskCacheKey
            val storage = key?.let(::storageOf)
            if (!isPicture(url) || storage == null) return chain.proceed(request)
            val context = request.context.applicationContext
            if (!withContext(Dispatchers.IO) { begin(context, storage, key) }) {
                return ErrorResult(null, request, IllegalStateException("Storage disconnected"))
            }
            try {
                // Already here (memory or disk): no slot needed.
                val loader = context.imageLoader
                val cached = loader.memoryCache?.get(MemoryCache.Key(key)) != null ||
                    withContext(Dispatchers.IO) { runCatching { loader.diskCache?.openSnapshot(key)?.use { true } }.getOrNull() == true }
                if (cached) return chain.proceed(request)
                var attempt = 0
                while (true) {
                    val result = slots.withPermit { chain.proceed(request) }
                    val busy = ((result as? ErrorResult)?.throwable as? HttpException)?.response?.takeIf { it.code == 503 }
                    if (busy == null || attempt >= 3) return result
                    attempt++
                    delay(retryAfterMs(busy.header("Retry-After")))
                }
            } finally {
                end(storage)
            }
        }
    }

    /**
     * The key already names the version, so a picture stays valid as long as Coil keeps it: its
     * answer is kept for 30 days instead of the address's day (a new day loads nothing again).
     */
    val keepLonger = okhttp3.Interceptor { chain ->
        val response = chain.proceed(chain.request())
        if (response.code == 200 && isPicture(chain.request().url.encodedPath)) {
            response.newBuilder().header("Cache-Control", "private, max-age=$KEEP_SECONDS").build()
        } else response
    }

    private const val KEEP_SECONDS = 30L * 24 * 60 * 60
}
