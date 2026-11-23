package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.FileObject
import java.util.Base64

/**
 * Fylvo: what the signed-in user may do with a file, mirroring frontend/src/util/permission.ts.
 * Outside a share ownership decides. Inside a share (`cloudreve://<id>@share/...`) only the
 * capability bitset the server sends with the listing decides. The server checks again; this
 * only keeps the app from offering actions that would be refused.
 */
object Permissions {
    /** Bit numbers of the server's NavigatorCapability (frontend/src/api/explorer.ts). */
    const val RENAME_FILE = 1
    const val UPLOAD_FILE = 6

    /** May create folders and upload into this folder. */
    fun canAddTo(folder: FileObject?): Boolean =
        if (folder != null && Connected.isConnected(folder.path)) Connected.canCreateIn(folder) || Connected.canUploadTo(folder)
        else folder != null && writable(folder) && (shareCapability(folder, UPLOAD_FILE) ?: (folder.owned == true))

    /** May rename, move, delete or overwrite this file. */
    fun canModifyFile(file: FileObject): Boolean =
        writable(file) && (shareCapability(file, RENAME_FILE) ?: (file.owned == true))

    /** Only the owner creates share links, and never from inside someone else's share. */
    fun canShareLink(file: FileObject): Boolean = file.owned == true && !CrUri.isShare(file.path) && writable(file)

    /**
     * Fylvo: false inside a connected storage; its rows have their own sheet and selection with
     * the rules of util/Connected (bits, row ids). Only adding is read from the bits here.
     */
    private fun writable(file: FileObject): Boolean = !Connected.isConnected(file.path)

    private fun shareCapability(file: FileObject, bit: Int): Boolean? =
        if (CrUri.isShare(file.path)) enabled(file.capability, bit) else null

    /** Bit [index] of a base64 boolset: byte index/8, bit index%8, as boolset.ts reads it. */
    fun enabled(boolset: String?, index: Int): Boolean {
        if (boolset.isNullOrEmpty()) return false
        val bytes = runCatching { Base64.getDecoder().decode(boolset) }.getOrNull() ?: return false
        if (index >= bytes.size * 8) return false
        return (bytes[index / 8].toInt() and (1 shl (index % 8))) != 0
    }
}
