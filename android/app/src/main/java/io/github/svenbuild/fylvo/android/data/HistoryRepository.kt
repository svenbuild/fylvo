package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ActivityList
import io.github.svenbuild.fylvo.android.api.ApiClient
import io.github.svenbuild.fylvo.android.api.EntityDto
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.VersionRequest

/** Fylvo 1.6: versions, details and history of a file, and emptying the trash (plan 6 to 8). */
class HistoryRepository(private val api: ApiClient) {

    /** The file now, with its shares, versions and the storage it uses. */
    suspend fun details(uri: String): FileObject = api.service().fileInfoExtended(uri).unwrap()

    suspend fun activity(uri: String, next: Int? = null): ActivityList = api.service().fileActivity(uri, next).unwrap()

    suspend fun makeCurrent(uri: String, version: String) = api.service().setCurrentVersion(VersionRequest(uri, version)).unwrapEmpty()

    suspend fun deleteVersion(uri: String, version: String) = api.service().deleteVersion(VersionRequest(uri, version)).unwrapEmpty()

    /** Can take long on the server: the client without transport replay and with a long read timeout. */
    suspend fun emptyTrash() = api.editService().emptyTrash().unwrapEmpty()

    companion object {
        /** The stored versions of a file (type 0), newest first. */
        fun versions(file: FileObject): List<EntityDto> =
            file.extendedInfo?.entities.orEmpty().filter { it.type == 0 }.sortedByDescending { it.createdAt }
    }
}
