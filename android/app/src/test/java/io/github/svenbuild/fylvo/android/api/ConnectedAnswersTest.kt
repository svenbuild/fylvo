package io.github.svenbuild.fylvo.android.api

import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.OverviewResult
import io.github.svenbuild.fylvo.android.data.overviewOf
import io.github.svenbuild.fylvo.android.data.settingsUrl
import io.github.svenbuild.fylvo.android.ui.files.SelectionAction
import io.github.svenbuild.fylvo.android.ui.files.SelectionContext
import io.github.svenbuild.fylvo.android.ui.files.selectionActions
import io.github.svenbuild.fylvo.android.util.ErrorText
import io.github.svenbuild.fylvo.android.util.UiText
import kotlinx.serialization.json.Json
import kotlinx.serialization.serializer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import retrofit2.Response

/** Fylvo: the example answers of connected storages through the app's real decoder. */
class ConnectedAnswersTest {
    private val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }

    private fun text(name: String) = javaClass.classLoader!!.getResource("connected/$name.json")!!.readText()
    private inline fun <reified T> example(name: String): ApiResponse<T> = decodeApiResponse(json, serializer<T>(), text(name))

    @Test
    fun overviewWithStoragesAndStates() {
        val o = example<ConnectedOverview>("storages").data!!
        assertTrue(o.available && o.allowed)
        assertEquals(2, o.providers.size)
        assertTrue(o.storages[0].ok)
        assertTrue(o.storages[1].needsReconnect)
        assertEquals("cloudreve://cdd255pa64jyicxm@connected", o.storages[0].uri)
        val empty = example<ConnectedOverview>("storages-not-set-up").data!!
        assertTrue(empty.storages.isEmpty())
        assertFalse(empty.providers.any { it.configured })
    }

    @Test
    fun listingPagesWithACursor() {
        val root = example<ListFileResponse>("list-root").data!!
        assertTrue(root.pagination.isCursor)
        assertNull(root.pagination.nextToken)
        assertEquals(1, root.connected?.omitted)
        assertTrue(root.connected!!.readOnly)
        assertFalse(root.connected!!.thumbnails)

        val first = example<ListFileResponse>("list-page-1").data!!
        assertEquals("56e9c232edf2149f2afc6c3e.2", first.pagination.nextToken)
        assertEquals(0, first.connected?.omitted)
        val second = example<ListFileResponse>("list-page-2").data!!
        assertEquals(listOf("c.pdf", "d.pdf"), second.files.map { it.name })
        assertEquals("56e9c232edf2149f2afc6c3e.4", second.pagination.nextToken)
        // Every row of a connected storage has thumbnails off: the app never asks for one.
        assertTrue((root.files + first.files + second.files).filter { !it.isFolder }.none { it.mayHaveThumbnail })
    }

    @Test
    fun infoOfTheSpecialRows() {
        val doc = example<FileObject>("info-native-document").data!!
        assertEquals("xlsx", doc.metadata?.get("fylvo:export"))
        val link = example<FileObject>("info-link").data!!
        assertEquals("1", link.metadata?.get("fylvo:link"))
    }

    @Test
    fun errorsKeepTheirReason() {
        val ambiguous = example<FileObject>("error-item-ambiguous")
        assertEquals(40925, ambiguous.code)
        assertEquals("ambiguous", ambiguous.reason)
        val reconnect = example<ListFileResponse>("error-reconnect")
        assertEquals(40921, reconnect.code)
        assertNull(reconnect.reason)
        assertEquals(40926, example<ListFileResponse>("error-listing-gone").code)
        // POST /file/url answers one failure as a batch; the reason of the entry is kept.
        val batch = example<FileUrlResponse>("error-url-batch")
        assertEquals(40081, batch.code)
        assertEquals(listOf(40925), batch.innerCodes)
        assertEquals("ambiguous", batch.reason)
    }

    private fun idOf(e: ApiException) = (ErrorText.of(e) as UiText.Res).id

    @Test
    fun textsOfTheNewCodes() {
        assertEquals(R.string.error_connected_unsupported, idOf(ApiException(40920, "x")))
        assertEquals(R.string.error_connected_reconnect, idOf(ApiException(40921, "x")))
        assertEquals(R.string.error_connected_unavailable, idOf(ApiException(40922, "x")))
        assertEquals(R.string.error_connected_busy, idOf(ApiException(40923, "x")))
        assertEquals(R.string.error_connected_listing_expired, idOf(ApiException(40926, "x")))
        assertEquals(R.string.error_connected_item, idOf(ApiException(40925, "x")))
        mapOf(
            "ambiguous" to R.string.error_connected_ambiguous,
            "changed" to R.string.error_connected_changed,
            "forbidden" to R.string.error_connected_forbidden,
            "flagged" to R.string.error_connected_flagged,
            "export_too_large" to R.string.error_connected_export_too_large,
            "daily_limit" to R.string.error_connected_daily_limit,
            "too_large" to R.string.error_connected_too_large,
            "something_new" to R.string.error_connected_item,
        ).forEach { (reason, id) -> assertEquals(reason, id, idOf(ApiException(40925, "x", reason = reason))) }
        // Inside a batch answer the single failure and its reason decide.
        assertEquals(R.string.error_connected_ambiguous, idOf(ApiException(40081, "x", listOf(40925), reason = "ambiguous")))
        assertEquals(R.string.error_connected_reconnect, idOf(ApiException(40081, "x", listOf(40921))))
    }

    private fun envelope(status: Int, body: String): Response<ApiResponse<ConnectedOverview>> {
        val media = "application/json".toMediaType()
        return if (status in 200..299) Response.success(status, decodeApiResponse(json, serializer<ConnectedOverview>(), body))
        else Response.error(status, body.toResponseBody(media))
    }

    @Test
    fun serverWithoutTheFeatureShowsNothing() {
        assertEquals(OverviewResult.Missing, overviewOf(envelope(404, "404 page not found")))
        assertEquals(OverviewResult.Missing, overviewOf(envelope(200, """{"code":404,"msg":"Not found"}""")))
        assertEquals(OverviewResult.Failed, overviewOf(envelope(502, "Bad gateway")))
        assertEquals(OverviewResult.Failed, overviewOf(envelope(200, """{"code":50001,"msg":"x"}""")))
        val found = overviewOf(envelope(200, text("storages")))
        assertTrue(found is OverviewResult.Found && found.overview.storages.size == 2)
        try {
            overviewOf(envelope(200, """{"code":401,"msg":"Login required"}"""))
            fail("expected login required")
        } catch (e: ApiException) {
            assertEquals(ApiException.CODE_LOGIN_REQUIRED, e.code)
        }
    }

    @Test
    fun webAppAddress() {
        assertEquals("https://cloud.example.com/settings?tab=connected", settingsUrl("https://cloud.example.com/", null))
        assertEquals("https://cloud.example.com/settings?tab=connected&reconnect=abc", settingsUrl("https://cloud.example.com", "abc"))
    }

    @Test
    fun selectionOffersDownloadAndDetailsOnly() {
        val root = example<ListFileResponse>("list-root").data!!
        val ctx = SelectionContext(connected = true)
        val notes = root.files.single { it.name == "notes.txt" }
        val one = selectionActions(listOf(notes), ctx)
        assertEquals(listOf(SelectionAction.DOWNLOAD), one.quick)
        assertEquals(listOf(SelectionAction.SELECT_ALL, SelectionAction.DETAILS), one.menu)
        // A folder or a duplicate in the selection: nothing to download.
        val mixed = selectionActions(root.files, ctx)
        assertTrue(mixed.quick.isEmpty())
        assertEquals(listOf(SelectionAction.SELECT_ALL), mixed.menu)
    }
}
