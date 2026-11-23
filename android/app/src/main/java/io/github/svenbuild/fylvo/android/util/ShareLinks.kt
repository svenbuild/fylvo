package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.ShareInfo
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Fylvo 1.6: share links (`https://<server>/s/<id>[/<password>]`) received from other apps or
 * pasted into the search (design/android-parity-plan.md, 3). Only a link of the signed-in server
 * is taken; any other link leads to no request at all.
 */
object ShareLinks {
    /** A share link of the signed-in server; [password] is empty when the link has none. */
    data class Ref(val id: String, val password: String)

    private val ID = Regex("[A-Za-z0-9]{1,64}")
    private val PASSWORD = Regex("[A-Za-z0-9]{1,32}")

    /** The first http(s) address in [text] (a shared text may carry more words around it). */
    fun findUrl(text: String): String? =
        Regex("""https?://\S+""").find(text)?.value?.trimEnd('.', ',', ')', ']', '>', '"', '\'')

    /** True when [text] looks like a link (no file search is started for it). */
    fun looksLikeLink(text: String): Boolean {
        val t = text.trim()
        return t.startsWith("http://", ignoreCase = true) || t.startsWith("https://", ignoreCase = true)
    }

    /**
     * The share of [url] when it belongs to [serverBase] (same scheme, host and port, default
     * ports filled in) and has exactly the share path; null for anything else.
     */
    fun parse(url: String, serverBase: String): Ref? {
        val link = url.trim().toHttpUrlOrNull() ?: return null
        val server = serverBase.trim().toHttpUrlOrNull() ?: return null
        if (link.username.isNotEmpty() || link.password.isNotEmpty()) return null
        if (io.github.svenbuild.fylvo.android.api.ApiClient.originOf(link) != io.github.svenbuild.fylvo.android.api.ApiClient.originOf(server)) return null
        // The server may live below a path ("https://host/cloud"); the share path follows it.
        val base = server.pathSegments.filter { it.isNotEmpty() }
        val segments = link.pathSegments.filter { it.isNotEmpty() }
        if (segments.size < base.size + 2 || segments.take(base.size) != base) return null
        val rest = segments.drop(base.size)
        if (rest[0] != "s" || rest.size > 3) return null
        val id = rest[1].takeIf { ID.matches(it) } ?: return null
        val password = rest.getOrNull(2)?.let { if (PASSWORD.matches(it)) it else return null }.orEmpty()
        return Ref(id, password)
    }

    /** What opening a share leads to, from its `share/info` answer. */
    sealed interface Decision {
        /** Open [root] (with the password the listing needs), titled [name]. */
        data class Open(val root: String, val name: String) : Decision
        /** A password is needed; [wrong] when one was given and did not fit. */
        data class NeedPassword(val name: String, val wrong: Boolean) : Decision
        /** Someone else's share, and the account may not open shares. */
        data object NotAllowed : Decision
    }

    /**
     * [info] answered for [typed] (the link's or the typed password). The owner gets the password
     * in the answer and needs no group right; everyone else needs group right 7 ([mayOpen]).
     */
    fun decide(id: String, info: ShareInfo, typed: String, myId: String?, mayOpen: Boolean?): Decision {
        val own = myId != null && info.owner?.id == myId
        if (!own && mayOpen != true) return Decision.NotAllowed
        if (info.passwordProtected && !info.unlocked) return Decision.NeedPassword(info.name, wrong = typed.isNotEmpty())
        val password = if (own) info.password else typed
        return Decision.Open(CrUri.shareRoot(id, password), info.name)
    }
}
