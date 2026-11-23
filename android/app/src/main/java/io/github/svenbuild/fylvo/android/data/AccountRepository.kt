package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.Me
import io.github.svenbuild.fylvo.android.api.SessionTag
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Fylvo: the signed-in account (`/user/me`) with its group rights, and the server's explorer
 * switches (map, full-text search). Held in memory for one session only; until the answers are
 * there, everything that depends on them is not offered.
 */
class AccountRepository(private val api: ApiClient, private val session: SessionManager) {

    data class State(
        val me: Me? = null,
        /** Null until known: the server's map provider is set (not empty, not "none"). */
        val mapEnabled: Boolean? = null,
        val fullTextSearch: Boolean = false,
    ) {
        /** The account's group right [bit], or null while unknown. */
        fun group(bit: Int): Boolean? = me?.group?.let { io.github.svenbuild.fylvo.android.util.Permissions.enabled(it.permission, bit) }
    }

    private val _state = MutableStateFlow(State())
    val state: StateFlow<State> = _state.asStateFlow()

    /** Loads both answers; a failure leaves the old state (asked again on the next call). */
    suspend fun refresh() {
        val tag = session.sessionTag()
        val me = runCatching { api.service().me(tag).unwrap() }.getOrNull()
        val explorer = runCatching { api.service().explorerConfig().unwrap() }.getOrNull()
        if (!session.isCurrent(tag)) return
        _state.value = _state.value.let { old ->
            old.copy(
                me = me ?: old.me,
                mapEnabled = explorer?.let { mapOn(it.mapProvider) } ?: old.mapEnabled,
                fullTextSearch = explorer?.fullTextSearch ?: old.fullTextSearch,
            )
        }
    }

    /** Loads once per session; later calls do nothing while the state is complete. */
    suspend fun ensureLoaded() {
        val s = _state.value
        if (s.me == null || s.mapEnabled == null) refresh()
    }

    /** The answer of a call made for [tag], kept only while that session is the current one. */
    fun update(tag: SessionTag, change: (State) -> State) {
        if (session.isCurrent(tag)) _state.value = change(_state.value)
    }

    fun clear() {
        _state.value = State()
    }

    companion object {
        fun mapOn(provider: String): Boolean = provider.isNotBlank() && !provider.equals("none", ignoreCase = true)
    }
}
