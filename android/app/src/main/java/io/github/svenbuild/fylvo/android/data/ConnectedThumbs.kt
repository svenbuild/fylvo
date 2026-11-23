package io.github.svenbuild.fylvo.android.data

import android.content.Context
import coil.imageLoader
import coil.memory.MemoryCache

/**
 * Fylvo 1.6: previews of files in connected storages. Coil keeps them under a key made of the
 * storage, the file and its version ([key]): the address the server hands out changes daily, the
 * picture of a version does not. The keys are noted per storage, so disconnecting a storage takes
 * exactly its pictures out of the caches (sign-out empties the whole cache and this list).
 */
object ConnectedThumbs {
    private const val PREFS = "connected_thumbs"

    fun key(storage: String, fileId: String, version: String): String = "cthumb:$storage:$fileId:$version"

    /** The storage a [key] belongs to, or null for other keys. */
    fun storageOf(key: String): String? =
        key.takeIf { it.startsWith("cthumb:") }?.removePrefix("cthumb:")?.substringBefore(':')?.takeIf { it.isNotEmpty() }

    /** A picture was loaded under [key] (called when Coil has it). */
    fun loaded(context: Context, key: String) {
        val storage = storageOf(key) ?: return
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        synchronized(this) {
            val keys = p.getStringSet(storage, emptySet()).orEmpty()
            if (key !in keys) p.edit().putStringSet(storage, keys + key).apply()
        }
    }

    /** The storage [storage] was disconnected: its pictures leave the memory and disk cache. Off the main thread. */
    fun forget(context: Context, storage: String) {
        val p = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val keys = synchronized(this) {
            p.getStringSet(storage, emptySet()).orEmpty().also { p.edit().remove(storage).apply() }
        }
        val loader = context.imageLoader
        // Also pictures loaded but not noted yet (memory): every key of the storage.
        loader.memoryCache?.let { cache -> cache.keys.filter { storageOf(it.key) == storage }.forEach { cache.remove(it) } }
        keys.forEach { k ->
            loader.memoryCache?.remove(MemoryCache.Key(k))
            runCatching { loader.diskCache?.remove(k) }
        }
    }

    /** Sign-out: the caches are emptied as a whole; the list goes too. */
    fun clear(context: Context) {
        synchronized(this) { context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().clear().apply() }
    }
}
