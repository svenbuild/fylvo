package io.github.svenbuild.fylvo.android.ui.files

import android.content.Context
import android.net.Uri
import io.github.svenbuild.fylvo.android.BuildConfig
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.ConnectPreview
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.ConnectAttempt
import io.github.svenbuild.fylvo.android.data.ConnectAttemptStore
import io.github.svenbuild.fylvo.android.data.ConnectFlow
import io.github.svenbuild.fylvo.android.data.ConnectRepository
import io.github.svenbuild.fylvo.android.util.UiText
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Fylvo 1.6: one sign-in at a provider started from the app (design/android-parity-plan.md, 14;
 * the server's contract for apps). One attempt at a time; a new start replaces the old one; it ends
 * when saved, declined, refused for good, cancelled or after 20 minutes. Sign-out drops it.
 */
class ConnectController(
    private val api: ApiClient,
    private val repo: ConnectRepository,
    private val store: ConnectAttemptStore,
    /** The list of storages is read again (something may have been saved). */
    private val reload: suspend () -> Unit,
    private val clock: () -> Long = System::currentTimeMillis,
) {
    sealed interface Ui {
        data object Idle : Ui
        /** The browser is in front; [provider] is being signed in to. */
        data class Waiting(val provider: String) : Ui
        /** "Connect <provider> of <account> with your Fylvo account?" */
        data class Confirm(val preview: ConnectPreview) : Ui
        data object Busy : Ui
    }

    private val _ui = MutableStateFlow<Ui>(Ui.Idle)
    val ui: StateFlow<Ui> = _ui.asStateFlow()
    private val _message = MutableStateFlow<UiText?>(null)
    val message: StateFlow<UiText?> = _message.asStateFlow()
    private val lock = Mutex()
    /** Raised by sign-out: an answer to a request sent before it is thrown away. */
    @Volatile private var generation = 0
    /** Sign-out and every stored write take it: nothing is written back after a sign-out. */
    private val gate = Any()

    /** Stores [a] when no sign-out came since [round]; false: it belongs to nobody any more. */
    private fun keep(round: Int, a: ConnectAttempt): Boolean = synchronized(gate) {
        if (round != generation) return false
        store.save(a)
        true
    }

    fun messageShown() {
        _message.value = null
    }

    /**
     * Starts a sign-in at [provider] ([reconnect]: renew that storage) and answers the address to
     * open in the browser at once (it must be opened within a minute), or null when it cannot start.
     */
    suspend fun start(provider: String, reconnect: String?): String? = lock.withLock {
        val server = api.serverBase()
        // The verifier and the token go only to a server reached over https (development builds: http too).
        if (!server.startsWith("https://") && !BuildConfig.DEBUG) {
            _message.value = UiText.Res(R.string.connect_https_only)
            return@withLock null
        }
        store.clear()
        val verifier = ConnectFlow.newVerifier()
        val round = generation
        val account = api.accountTag()
        try {
            val res = repo.authorize(provider, reconnect, ConnectFlow.challenge(verifier))
            // Signed out (or another account) while the answer was on its way: nothing is kept or opened.
            if (round != generation || api.accountTag() != account || api.serverBase() != server) return@withLock null
            if (!ConnectFlow.validAttempt(res.attempt) || !ConnectFlow.validStart(res.url, allowHttp = BuildConfig.DEBUG)) {
                _message.value = UiText.Res(R.string.connect_failed)
                return@withLock null
            }
            if (!keep(round, ConnectAttempt(server, account, res.attempt, verifier, provider, reconnect, clock()))) return@withLock null
            _ui.value = Ui.Waiting(provider)
            res.url
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            _message.value = startError(e)
            null
        }
    }

    /** A link back from the browser. */
    suspend fun onReturn(link: ConnectFlow.Return) = lock.withLock {
        val round = generation
        val a = current() ?: return@withLock
        when (link) {
            ConnectFlow.Return.Front -> Unit
            is ConnectFlow.Return.Error -> if (link.attempt == a.attempt) _message.value = UiText.Res(errorText(link.reason))
            is ConnectFlow.Return.Finish -> {
                if (link.attempt != a.attempt) return@withLock
                // A second finish link waits until the first one is decided.
                if (a.code != null && a.code != link.code) return@withLock
                // The same link again after a yes whose answer was lost: the same save again (the
                // server answers the same storage), never a new preview of a used code.
                if (a.code == link.code && a.yes) return@withLock save(a)
                if (!keep(round, a.copy(code = link.code))) return@withLock
                preview(a.copy(code = link.code))
            }
        }
    }

    /** The person's answer to the question. */
    suspend fun answer(yes: Boolean) = lock.withLock {
        val round = generation
        val a = current() ?: return@withLock run { _ui.value = Ui.Idle }
        val code = a.code ?: return@withLock run { _ui.value = Ui.Idle }
        if (yes) {
            // The yes is stored before sending: after a restart the same save is sent again.
            if (!keep(round, a.copy(yes = true))) return@withLock
            save(a.copy(yes = true))
        } else {
            _ui.value = Ui.Busy
            runCatching { repo.cancel(code, a.verifier) }
            end()
        }
    }

    /** The attempt is given up by the person. */
    fun cancelAttempt() {
        store.clear()
        _ui.value = Ui.Idle
    }

    /**
     * At app start and whenever the app comes back to the front: a save that was said yes to but
     * not answered is sent again (likewise a preview without answer). Not while a question or a
     * request is open.
     */
    suspend fun resume() = lock.withLock {
        // Busy cannot be seen here: every request runs under the lock and sets its end state
        // (also when it is cancelled), so only an open question is left alone.
        if (_ui.value is Ui.Confirm) return@withLock
        val a = current() ?: return@withLock
        when {
            a.code != null && a.yes -> save(a)
            a.code != null -> preview(a)
            else -> _ui.value = Ui.Waiting(a.provider)
        }
    }

    /** Sign-out: the attempt is dropped. */
    fun clear() = synchronized(gate) {
        generation++
        store.clear()
        _ui.value = Ui.Idle
        _message.value = null
    }

    /** The stored attempt while it belongs to this server and account and is not too old. */
    private suspend fun current(): ConnectAttempt? {
        val a = store.load() ?: return null
        if (a.server != api.serverBase() || a.account != api.accountTag() || clock() - a.startedAt > ConnectFlow.ATTEMPT_MS) {
            store.clear()
            _ui.value = Ui.Idle
            return null
        }
        return a
    }

    private suspend fun preview(a: ConnectAttempt) {
        val code = a.code ?: return
        val round = generation
        _ui.value = Ui.Busy
        try {
            val p = repo.preview(code, a.verifier)
            // Signed out meanwhile: the answer belongs to nobody any more.
            if (round != generation) return
            // A reconnect may save without asking (the server refuses another provider account).
            if (a.reconnect != null) { if (keep(round, a.copy(yes = true))) save(a.copy(yes = true)) }
            else _ui.value = Ui.Confirm(p)
        } catch (e: kotlinx.coroutines.CancellationException) {
            stopped(round, a)
            throw e
        } catch (e: Exception) {
            if (round == generation) handle(e, a, sentBefore = a.sent, round)
        }
    }

    /** A request was cancelled (the screen went): the attempt stays stored and is resumed later. */
    private fun stopped(round: Int, a: ConnectAttempt) {
        if (round == generation && _ui.value == Ui.Busy) _ui.value = Ui.Waiting(a.provider)
    }

    private suspend fun save(a: ConnectAttempt) {
        val code = a.code ?: return
        val round = generation
        _ui.value = Ui.Busy
        val sentBefore = a.sent
        if (!keep(round, a.copy(sent = true))) return
        try {
            repo.save(code, a.verifier)
            if (round != generation) return
            _message.value = UiText.Res(if (a.reconnect != null) R.string.connect_reconnected else R.string.connect_saved)
            end()
        } catch (e: kotlinx.coroutines.CancellationException) {
            stopped(round, a)
            throw e
        } catch (e: Exception) {
            if (round == generation) handle(e, a.copy(sent = true), sentBefore, round)
        }
    }

    private suspend fun handle(e: Exception, a: ConnectAttempt, sentBefore: Boolean, round: Int) {
        val api = e as? ApiException
        val code = if (e is java.io.IOException) null else api?.code ?: -1
        when (ConnectFlow.next(code, api?.reason, sentBefore)) {
            ConnectFlow.Next.KeepWaiting -> {
                if (!keep(round, a.copy(code = null, yes = false, sent = false))) return
                _ui.value = Ui.Waiting(a.provider)
            }
            ConnectFlow.Next.End -> {
                _message.value = finishError(api)
                end(reload = false)
            }
            ConnectFlow.Next.EndAndReload -> {
                _message.value = finishError(api)
                end()
            }
            ConnectFlow.Next.Repeat -> {
                // No answer: the same request can go again (the code stays stored).
                _message.value = UiText.Res(R.string.connect_no_answer)
                _ui.value = Ui.Waiting(a.provider)
            }
        }
    }

    private suspend fun end(reload: Boolean = true) {
        store.clear()
        _ui.value = Ui.Idle
        if (reload) runCatching { reload() }
    }

    /** A retry after "no answer": the stored request again. */
    suspend fun retry() = resume()

    private fun startError(e: Exception): UiText = when ((e as? ApiException)?.reason) {
        "not_allowed" -> UiText.Res(R.string.connect_not_allowed)
        "not_configured", "app_signin_off" -> UiText.Res(R.string.connect_not_offered)
        "too_many" -> UiText.Res(R.string.connect_too_many)
        else -> userError(e)
    }

    private fun finishError(e: ApiException?): UiText = when {
        e == null -> UiText.Res(R.string.connect_failed)
        e.code == 40004 -> UiText.Res(R.string.connect_exists)
        e.reason == "other_account" -> UiText.Res(R.string.connect_other_account)
        e.reason == "too_many" -> UiText.Res(R.string.connect_too_many)
        e.reason == "not_allowed" -> UiText.Res(R.string.connect_not_allowed)
        e.reason == "app_signin_off" -> UiText.Res(R.string.connect_not_offered)
        e.code == 40016 -> UiText.Res(R.string.connect_storage_gone)
        else -> UiText.Res(R.string.connect_failed)
    }

    private fun errorText(reason: String): Int = when (reason) {
        "denied" -> R.string.connect_link_denied
        "expired" -> R.string.connect_link_expired
        "not_allowed" -> R.string.connect_not_allowed
        "too_many" -> R.string.connect_too_many
        else -> R.string.connect_link_failed
    }

    companion object {
        /** Opens the start address in a Custom Tab (the system browser when there is none). */
        fun openInTab(context: Context, url: String) {
            val uri = Uri.parse(url)
            try {
                androidx.browser.customtabs.CustomTabsIntent.Builder().build().launchUrl(context, uri)
            } catch (_: Exception) {
                openInBrowser(context, url)
            }
        }
    }
}
