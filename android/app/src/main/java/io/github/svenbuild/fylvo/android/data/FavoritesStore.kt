package io.github.svenbuild.fylvo.android.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.first
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

private val Context.favoritesStore by preferencesDataStore(name = "favorites")

@Serializable
data class FavoriteEntry(
    /** cloudreve:// URI of the remote file. */
    val path: String,
    val name: String,
    val size: Long,
    /** Absolute local path under filesDir/offline. */
    val localFile: String,
    val savedAt: Long,
    /** Remote updated_at at download time — staleness signal for refresh. */
    val remoteUpdatedAt: String = "",
)

/**
 * Single offline files of app versions before 1.2 (JSON list in DataStore). Fylvo 1.2 keeps
 * offline files and folders in data/offline; this list is only read once to take them over
 * (see OfflineManager.migrateLegacy) and then cleared.
 */
class FavoritesStore(private val context: Context) {

    suspend fun clear() {
        context.favoritesStore.edit { it.clear() }
    }

    private object Keys {
        val entriesJson = stringPreferencesKey("entries_json")
    }

    private val json = Json { ignoreUnknownKeys = true }

    suspend fun listNow(): List<FavoriteEntry> = decode(context.favoritesStore.data.first()[Keys.entriesJson])

    private fun decode(raw: String?): List<FavoriteEntry> =
        raw?.let {
            runCatching { json.decodeFromString<List<FavoriteEntry>>(it) }.getOrNull()
        } ?: emptyList()
}
