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
        // Fylvo 1.6: not into a link with a password (the upload queue would have to keep it).
        else folder != null && writable(folder) && !CrUri.hasSecret(folder.path) &&
            (shareCapability(folder, UPLOAD_FILE) ?: (folder.owned == true))

    /** May rename, move, delete or overwrite this file. */
    fun canModifyFile(file: FileObject): Boolean =
        writable(file) && (shareCapability(file, RENAME_FILE) ?: (file.owned == true))

    /** Only the owner creates share links, and never from inside someone else's share. */
    fun canShareLink(file: FileObject): Boolean = file.owned == true && !CrUri.isShare(file.path) && writable(file)

    // ---- Fylvo 1.6: one rule per action (design/android-parity-plan.md). Group bits are null
    // while the account's rights are not known yet; then nothing that needs them is offered.

    /** Group bits of the server (inventory/types, frontend/src/api/user.ts). */
    const val GROUP_SHARE = 2
    const val GROUP_ARCHIVE = 5
    const val GROUP_SHARE_DOWNLOAD = 7

    /** File bits of the server's navigator capability (frontend/src/api/explorer.ts). */
    const val CREATE_FILE = 0
    const val DOWNLOAD_FILE = 7
    const val SHARE = 18
    const val VERSION_CONTROL = 20

    /** A file bit the server sends; a file without a capability set (own listing) is not limited by it. */
    private fun bitOrOwn(file: FileObject, bit: Int): Boolean = file.capability.isNullOrEmpty() || enabled(file.capability, bit)

    /** Link sharing and sharing with people: owner, group right, file right. */
    fun canShare(file: FileObject, groupShare: Boolean?): Boolean =
        groupShare == true && canShareLink(file) && !Connected.isConnected(file.path) && bitOrOwn(file, SHARE)

    /**
     * Replacing a file's content (text editor): own files by ownership; inside shares and connected
     * storages rename (1) and upload (6) together, as `useActionDisplayOpt.ts` does. Visitors of an
     * upload link have only bit 6 and may not replace.
     */
    fun canSaveContent(file: FileObject): Boolean = when {
        file.isFolder -> false
        Connected.isConnected(file.path) -> Connected.canRename(file) && enabled(file.capability, UPLOAD_FILE) &&
            Connected.canDownload(file)
        CrUri.isShare(file.path) -> enabled(file.capability, RENAME_FILE) && enabled(file.capability, UPLOAD_FILE)
        else -> file.owned == true
    }

    /** A new text file in [folder]: the same rule as saving content, taken at the folder. */
    fun canCreateTextIn(folder: FileObject?): Boolean = folder != null && folder.isFolder &&
        !Connected.isConnected(folder.path) && when {
            CrUri.isShare(folder.path) -> enabled(folder.capability, RENAME_FILE) && enabled(folder.capability, UPLOAD_FILE)
            else -> folder.owned == true
        }

    /** Older versions: only the owner, outside shares and connected storages. */
    fun canManageVersions(file: FileObject): Boolean = !file.isFolder && file.owned == true &&
        !CrUri.isShare(file.path) && !Connected.isConnected(file.path) &&
        file.metadata?.containsKey(FileObject.METADATA_SHARED_REDIRECT) != true && bitOrOwn(file, VERSION_CONTROL)

    /** Sources of an archive task: the group right and readable files, never connected ones. */
    fun canArchiveFrom(files: List<FileObject>, groupArchive: Boolean?): Boolean =
        groupArchive == true && files.isNotEmpty() && files.none { Connected.isConnected(it.path) || CrUri.hasSecret(it.path) } &&
            files.all { bitOrOwn(it, DOWNLOAD_FILE) }

    /** A folder an archive task may write into (create and upload). */
    fun canArchiveInto(folder: FileObject?): Boolean = folder != null && folder.isFolder &&
        !Connected.isConnected(folder.path) && !CrUri.hasSecret(folder.path) && when {
            CrUri.isShare(folder.path) -> enabled(folder.capability, CREATE_FILE) && enabled(folder.capability, UPLOAD_FILE)
            else -> folder.owned == true
        }

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
