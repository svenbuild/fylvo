package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.ShareCreateRequest
import io.github.svenbuild.fylvo.android.api.ShareInfo
import io.github.svenbuild.fylvo.android.api.ShareMemberDto
import io.github.svenbuild.fylvo.android.util.CrUri
import java.time.Instant
import java.time.OffsetDateTime

/** Fylvo: who may upload through a folder link. */
enum class UploadMode { OFF, VIEW_AND_UPLOAD, UPLOAD_ONLY }

/**
 * Fylvo: the settings of a link as the share sheet edits them (design/android-parity-plan.md, 2).
 * [expireSeconds] null = never; [downloads] null = no limit. [expireToChoose] and
 * [downloadsToChoose] mark values of an existing link that cannot be sent again as they are
 * (expired, no downloads left): the sheet asks for a new choice first.
 */
data class LinkOptions(
    val password: String = "",
    val expireSeconds: Long? = null,
    val downloads: Int? = null,
    val upload: UploadMode = UploadMode.OFF,
    val uploadLimit: Long = 0,
    val shareView: Boolean = false,
    val showReadme: Boolean = false,
    val expireToChoose: Boolean = false,
    val downloadsToChoose: Boolean = false,
    /** The unchanged end of an existing link; sent as the seconds left at the moment of sending. */
    val keepEnd: Instant? = null,
) {
    val complete: Boolean get() = !expireToChoose && !downloadsToChoose

    /**
     * The request for [uri]. On an edit the server replaces every setting except the password, so
     * all are sent; members never (a link share has none, and an empty list would remove them).
     */
    fun toRequest(uri: String, isFolder: Boolean, editing: Boolean, now: Instant = Instant.now()): ShareCreateRequest = ShareCreateRequest(
        uri = uri,
        isPrivate = !editing && password.isNotEmpty(),
        password = if (editing) "" else password,
        expire = (keepEnd?.let { remainingSeconds(it, now) } ?: expireSeconds)?.coerceIn(1, MAX_EXPIRE_SECONDS)?.toInt() ?: 0,
        downloads = if (isFolder) 0 else downloads?.coerceAtLeast(1) ?: 0,
        shareView = isFolder && shareView,
        showReadme = isFolder && showReadme,
        allowUpload = isFolder && upload != UploadMode.OFF,
        hideContent = isFolder && upload == UploadMode.UPLOAD_ONLY,
        uploadLimit = if (isFolder && upload != UploadMode.OFF) uploadLimit.coerceAtLeast(0) else 0,
    )

    companion object {
        /** The server's longest link lifetime (10 years, service/share/manage.go). */
        const val MAX_EXPIRE_SECONDS = 315_360_000L

        /** Only letters and digits, at most 32 (the server's rule). */
        fun passwordValid(password: String): Boolean = password.length in 1..32 && password.all { it in 'a'..'z' || it in 'A'..'Z' || it in '0'..'9' }

        /** The settings of an existing link at [now]: remaining time rounded up, never 0. */
        fun of(share: ShareInfo, now: Instant = Instant.now()): LinkOptions {
            val end = share.expires?.let { runCatching { OffsetDateTime.parse(it).toInstant() }.getOrNull() }
            val remaining = end?.let { remainingSeconds(it, now) }
            return LinkOptions(
                expireSeconds = remaining?.takeIf { it > 0 },
                keepEnd = end?.takeIf { remaining != null && remaining > 0 },
                expireToChoose = share.expires != null && (remaining == null || remaining <= 0),
                downloads = share.remainDownloads?.takeIf { it > 0 && !share.isFolder },
                // A folder link has no download limit in the app (old links may carry one).
                downloadsToChoose = !share.isFolder && share.remainDownloads != null && share.remainDownloads <= 0,
                upload = when {
                    !share.allowUpload -> UploadMode.OFF
                    share.hideContent -> UploadMode.UPLOAD_ONLY
                    else -> UploadMode.VIEW_AND_UPLOAD
                },
                uploadLimit = share.uploadLimit,
                shareView = share.shareView,
                showReadme = share.showReadme,
            )
        }

        /** Whole seconds from [now] to [end], rounded up (0 or less = passed). */
        fun remainingSeconds(end: Instant, now: Instant): Long {
            val millis = end.toEpochMilli() - now.toEpochMilli()
            return if (millis <= 0) 0 else (millis + 999) / 1000
        }
    }
}

/** Fylvo: the account's own shares (list, edit, delete) and sharing with people. */
class ShareRepository(private val api: ApiClient, private val session: SessionManager) {

    companion object {
        /** App-side code: the last downloads were used up while the link was being edited. */
        const val CODE_NO_DOWNLOADS_LEFT = -40

        /**
         * Members of a share with people; [members] replaces the list. [expireSeconds] is the time
         * left (see [LinkOptions.of]; an expired one must be chosen again by the caller), the other
         * settings are the share's own, because the server replaces them all.
         */
        fun memberRequest(uri: String, share: ShareInfo?, members: List<ShareMemberDto>, expireSeconds: Long?): ShareCreateRequest =
            ShareCreateRequest(
                uri = uri,
                memberOnly = true,
                members = members,
                expire = expireSeconds?.coerceIn(1, LinkOptions.MAX_EXPIRE_SECONDS)?.toInt() ?: 0,
                shareView = share?.shareView ?: false,
                showReadme = share?.showReadme ?: false,
            )
    }

    data class Page(val shares: List<ShareInfo>, val next: String?)

    suspend fun list(next: String? = null): Page {
        val res = api.service().listShares(nextPageToken = next).unwrap()
        return Page(res.shares, res.pagination?.nextToken?.takeIf { it.isNotEmpty() })
    }

    /** Creates a link for [file]; the answer is the complete link. */
    suspend fun create(file: FileObject, options: LinkOptions): String =
        // A password in the body: no redirects.
        api.authService().createShare(options.toRequest(file.path, file.isFolder, editing = false), session.sessionTag()).unwrap()

    /**
     * Saves [options] for [share] of the file at [uri]. A download limit left as it was is read
     * again just before, so downloads made while the sheet was open are not given back.
     */
    suspend fun edit(share: ShareInfo, uri: String, options: LinkOptions): String {
        val tag = session.sessionTag()
        // An expired link has no info any more (404): its own list entry is the latest there is.
        val untouched = !share.expired && options.downloads != null && options.downloads == LinkOptions.of(share).downloads
        val send = if (untouched) {
            // Expired meanwhile: the server has no info any more; the limit as it was is sent.
            val left = try {
                details(share.id).remainDownloads
            } catch (e: ApiException) {
                if (e.code == 404) options.downloads else throw e
            }
            if (left != null && left <= 0) throw ApiException(CODE_NO_DOWNLOADS_LEFT, "No downloads left")
            options.copy(downloads = left)
        } else options
        return api.service().editShare(share.id, send.toRequest(uri, share.isFolder, editing = true), tag).unwrap()
    }

    suspend fun delete(id: String) = api.service().deleteShare(id, session.sessionTag()).unwrapEmpty()

    /**
     * Fylvo: a share as its visitor sees it, unlocked with [password] when it fits (a wrong one
     * answers `unlocked: false`). Without redirects: the password is in the address.
     */
    suspend fun visit(id: String, password: String): ShareInfo =
        api.authService().shareDetails(id, password = password.ifEmpty { null }).unwrap()

    /** The current state of own share [id] (the owner is always unlocked). */
    suspend fun details(id: String): ShareInfo = api.service().shareDetails(id).unwrap()

    /**
     * The address of the shared file or folder, as the web finds it (`share.ts`): the owner's
     * `source_uri` (opened with the link's password), plus the name for a single file. Fails for
     * an expired share (the server gives no `source_uri` then).
     */
    suspend fun sourceOf(share: ShareInfo): String {
        val info = api.authService().shareDetails(share.id, password = share.password.ifEmpty { null }, ownerExtended = true).unwrap()
        val folder = info.sourceUri.ifEmpty { throw ApiException(404, "No source") }
        return if (share.isFolder) folder else CrUri.join(folder, share.name)
    }

    /** What "show in folder" opens: a shared folder itself, a shared file's folder. */
    fun folderOf(share: ShareInfo, source: String): String = if (share.isFolder) source else CrUri.parent(source)

    /** The owner's shares of the file at [uri] (`file/info?extended=true`). */
    suspend fun sharesOf(uri: String): List<ShareInfo> =
        api.service().fileInfoExtended(uri).unwrap().extendedInfo?.shares.orEmpty()
}
