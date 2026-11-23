package io.github.svenbuild.fylvo.android.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Fylvo: `GET api/v4/fylvo/connected`, the account's connected storages (Google Drive, OneDrive).
 * [available]: the server has the feature; [allowed]: this account's group may use it.
 */
@Serializable
data class ConnectedOverview(
    val available: Boolean = false,
    val allowed: Boolean = false,
    val max: Int = 0,
    val providers: List<ConnectedProvider> = emptyList(),
    val storages: List<ConnectedStorage> = emptyList(),
)

@Serializable
data class ConnectedProvider(
    val type: String = "",
    val name: String = "",
    /** The operator has set this provider up; only then can it be connected in the web app. */
    val configured: Boolean = false,
)

@Serializable
data class ConnectedStorage(
    val id: String = "",
    val provider: String = "",
    val name: String = "",
    val account: String = "",
    /** `ok` or `reconnect`; anything else is treated as not usable right now. */
    val status: String = "",
    val message: String? = null,
    /** Root of the storage, `cloudreve://<id>@connected`. */
    val uri: String = "",
    @SerialName("created_at") val createdAt: String = "",
) {
    val ok: Boolean get() = status == STATUS_OK
    val needsReconnect: Boolean get() = status == STATUS_RECONNECT

    companion object {
        const val STATUS_OK = "ok"
        const val STATUS_RECONNECT = "reconnect"
    }
}

/** Fylvo: the `connected` block of a listing inside a connected storage. */
@Serializable
data class ConnectedListing(
    val id: String = "",
    val provider: String = "",
    @SerialName("read_only") val readOnly: Boolean = true,
    val search: Boolean = false,
    val thumbnails: Boolean = false,
    /** Entries the server leaves out because their names cannot be addressed. */
    val omitted: Int = 0,
)
