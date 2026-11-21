package io.github.svenbuild.fylvo.android.provider

import android.net.Uri
import androidx.core.content.FileProvider

/**
 * Fylvo: the app's FileProvider (see res/xml/file_paths.xml). An editor gets read and write access
 * to a working copy, but may not delete it: it is the only copy of the changes until they are
 * uploaded.
 */
class FylvoFileProvider : FileProvider() {
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int {
        if (uri.pathSegments.firstOrNull() == EDIT_PATH) return 0
        return super.delete(uri, selection, selectionArgs)
    }

    private companion object {
        /** The name of the working copies' folder in file_paths.xml. */
        const val EDIT_PATH = "edit"
    }
}
