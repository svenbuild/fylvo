package io.github.svenbuild.fylvo.android.util

import android.content.Context
import io.github.svenbuild.fylvo.android.R
import java.net.URLDecoder
import java.net.URLEncoder

/**
 * CrUri builds and manipulates `cloudreve://` URIs the same way the desktop
 * client does: segments are percent-encoded individually, "/" stays.
 *
 * Fylvo: every file system has its own root, `cloudreve://<authority>`: `my`, `trash`,
 * `shared_with_me`, and `<shareId>@share` for a shared folder. Parent and path helpers stop
 * at the root of the URI they are given instead of assuming `cloudreve://my`.
 */
object CrUri {
    private const val SCHEME = "cloudreve://"
    const val MY_PREFIX = "cloudreve://my"
    const val SHARED_WITH_ME = "cloudreve://shared_with_me"

    fun join(parent: String, name: String): String {
        val encoded = name.split('/').filter { it.isNotEmpty() }
            .joinToString("/") { URLEncoder.encode(it, "UTF-8").replace("+", "%20") }
        return if (parent.endsWith('/')) parent + encoded else "$parent/$encoded"
    }

    /** `cloudreve://<authority>` of [uri], without path or query. */
    fun root(uri: String): String {
        val rest = uri.substringBefore('?').removePrefix(SCHEME)
        return SCHEME + rest.substringBefore('/')
    }

    /** True inside a share: `cloudreve://<shareId>@share/...`. */
    fun isShare(uri: String): Boolean = root(uri).endsWith("@share")

    fun parent(uri: String): String {
        val trimmed = uri.substringBefore('?').trimEnd('/')
        val root = root(trimmed)
        val idx = trimmed.lastIndexOf('/')
        return if (idx < root.length) root else trimmed.substring(0, idx)
    }

    fun fileName(uri: String): String {
        val seg = uri.substringBefore('?').trimEnd('/').substringAfterLast('/')
        return URLDecoder.decode(seg, "UTF-8")
    }

    fun isRoot(uri: String): Boolean = uri.substringBefore('?').trimEnd('/') == root(uri)

    /**
     * One spelling for comparing URIs from different sources: the server leaves
     * `(` and `'` bare where [join] percent-encodes them. Decoded, no query part.
     */
    fun canonical(uri: String): String {
        val root = root(uri)
        val rel = uri.substringBefore('?').removePrefix(root).trim('/')
        return if (rel.isEmpty()) root else root + "/" + URLDecoder.decode(rel, "UTF-8")
    }

    /** Names of the file system roots in the app language; English until [useLabelsFrom] runs. */
    data class Labels(
        val myFiles: String = "My files",
        val sharedWithMe: String = "Shared with me",
        val trash: String = "Trash",
        val sharedFolder: String = "Shared folder",
        /** Fylvo: a connected storage whose name is not known yet. */
        val connectedStorage: String = "Connected storage",
    )

    @Volatile
    var labels = Labels()
        private set

    fun useLabelsFrom(context: Context) {
        labels = Labels(
            myFiles = context.getString(R.string.my_files),
            sharedWithMe = context.getString(R.string.shared_with_me),
            trash = context.getString(R.string.trash),
            sharedFolder = context.getString(R.string.shared_folder),
            connectedStorage = context.getString(R.string.connected_storage),
        )
    }

    /** Fylvo: names of shared folders by share root, learned from "Shared with me" listings. */
    private val shareNames = java.util.concurrent.ConcurrentHashMap<String, String>()

    fun nameShare(uri: String, name: String) {
        if (isShare(uri) && name.isNotEmpty()) shareNames[root(uri)] = name
    }

    fun forgetShareNames() = shareNames.clear()

    /** Label of a file system root; a shared folder shows its own name. */
    fun rootLabel(uri: String): String = when (val root = root(uri)) {
        MY_PREFIX -> labels.myFiles
        SHARED_WITH_ME -> labels.sharedWithMe
        "cloudreve://trash" -> labels.trash
        else -> if (Connected.isConnected(root)) Connected.rootName(root) ?: labels.connectedStorage
        else shareNames[root] ?: labels.sharedFolder
    }

    /** A folder search can be limited to: any folder below a root, or a whole shared folder. */
    fun isSearchScope(uri: String): Boolean = !Connected.isConnected(uri) && (!isRoot(uri) || isShare(uri))

    /** Display name of the folder at [uri]: its last segment, or the root's label. */
    fun displayName(uri: String): String = if (isRoot(uri)) rootLabel(uri) else fileName(uri)

    /** Display name of the folder containing [path]. */
    fun parentDisplayName(path: String): String = displayName(parent(path))

    /** "My files / Photos / 2025" for breadcrumb-style subtitles. */
    fun displayPath(uri: String): String {
        val rel = uri.substringBefore('?').removePrefix(root(uri)).trim('/')
        val label = rootLabel(uri)
        return if (rel.isEmpty()) label
        else label + " / " + URLDecoder.decode(rel, "UTF-8").replace("/", " / ")
    }
}
