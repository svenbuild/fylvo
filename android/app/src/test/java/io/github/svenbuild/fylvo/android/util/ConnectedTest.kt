package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.ApiResponse
import io.github.svenbuild.fylvo.android.api.ConnectedOverview
import io.github.svenbuild.fylvo.android.api.ConnectedStorage
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.ListFileResponse
import io.github.svenbuild.fylvo.android.api.decodeApiResponse
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.SortBy
import kotlinx.serialization.json.Json
import kotlinx.serialization.serializer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Rows and answers from the example answers of the server (test resources `connected/`). */
class ConnectedTest {
    private val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }

    private inline fun <reified T> example(name: String): ApiResponse<T> {
        val text = javaClass.classLoader!!.getResource("connected/$name.json")!!.readText()
        return decodeApiResponse(json, serializer<T>(), text)
    }

    private val root by lazy { example<ListFileResponse>("list-root").data!! }
    private fun row(name: String) = root.files.single { it.name == name }

    @After
    fun forget() = Connected.forgetNames()

    @Test
    fun placeIsTheRootAuthority() {
        assertTrue(Connected.isConnected("cloudreve://abc@connected"))
        assertTrue(Connected.isConnected("cloudreve://abc@connected/Docs/a.pdf"))
        assertFalse(Connected.isConnected("cloudreve://my/connected"))
        assertFalse(Connected.isConnected("cloudreve://abc@share/x"))
        assertEquals("abc", Connected.storageId("cloudreve://abc@connected/Docs"))
        assertNull(Connected.storageId("cloudreve://my/Docs"))
    }

    @Test
    fun capabilityBitsOfTheExamples() {
        val folder = row("Docs") // "AAKI"
        assertTrue(Connected.canEnter(folder))
        assertTrue(Connected.canInfo(folder))
        assertFalse(Connected.canDownload(folder))
        val file = row("notes.txt") // "gAAI"
        assertTrue(Connected.canDownload(file))
        assertTrue(Connected.canInfo(file))
        assertFalse(Connected.canEnter(file))
    }

    @Test
    fun duplicateRowOnlyShowsItsSheet() {
        val twice = row("twice.txt") // "AAAI": info only
        assertTrue(Connected.isUnusable(twice))
        assertFalse(Connected.canDownload(twice))
        assertTrue(Connected.canInfo(twice))
        assertEquals(Connected.Tap.SHEET, Connected.tapAction(twice))
        // Any other value of the mark is unusable too.
        assertTrue(Connected.isUnusable(FileObject(metadata = mapOf(Connected.META_UNSUPPORTED to "something-new"))))
    }

    @Test
    fun linksAreReadOnlyAndFollowTheirBits() {
        val folderLink = row("Link to Docs")
        assertTrue(Connected.isLink(folderLink))
        assertEquals(Connected.Tap.ENTER, Connected.tapAction(folderLink))
        val fileLink = row("Link to notes.txt")
        assertTrue(Connected.isLink(fileLink))
        assertTrue(Connected.sizeUnknown(fileLink))
        assertEquals(Connected.Tap.OPEN, Connected.tapAction(fileLink))
        assertEquals("Link to notes.txt", Connected.downloadName(fileLink))
    }

    @Test
    fun googleDocumentOpensAtTheProviderAndDownloadsAsOffice() {
        val sheet = row("Budget.gsheet")
        assertEquals("xlsx", Connected.exportExt(sheet))
        assertEquals("https://drive.example.com/file/d/id005/view", Connected.externalUrl(sheet))
        assertTrue(Connected.sizeUnknown(sheet))
        assertTrue(Connected.canDownload(sheet))
        assertEquals(Connected.Tap.BROWSER, Connected.tapAction(sheet))
        assertEquals("Budget.xlsx", Connected.downloadName(sheet))
    }

    @Test
    fun shortcutToAGoogleDocumentBehavesLikeTheDocument() {
        val link = FileObject(
            name = "Shared plan.gdoc",
            capability = "gAAI",
            metadata = mapOf(
                Connected.META_LINK to "1",
                Connected.META_EXPORT to "docx",
                Connected.META_EXTERNAL_URL to "https://drive.example.com/document/d/id9/edit",
                Connected.META_SIZE_UNKNOWN to "1",
            ),
        )
        assertTrue(Connected.isLink(link))
        assertEquals(Connected.Tap.BROWSER, Connected.tapAction(link))
        assertEquals("Shared plan.docx", Connected.downloadName(link))
        assertTrue(Connected.canDownload(link))
    }

    @Test
    fun downloadNameKeepsOtherNames() {
        fun doc(name: String, export: String?) =
            FileObject(name = name, metadata = export?.let { mapOf(Connected.META_EXPORT to it) })
        assertEquals("Plan.docx", doc("Plan.gdoc", "docx").downloadName())
        assertEquals("Slides.pptx", doc("Slides.gslides", "pptx").downloadName())
        // Like the server: only the extension of this type, case as it is.
        assertEquals("Slides.GSLIDES.pptx", doc("Slides.GSLIDES", "pptx").downloadName())
        // A shortcut named like another type keeps its name.
        assertEquals("Budget.gdoc.xlsx", doc("Budget.gdoc", "xlsx").downloadName())
        assertEquals("Sketch.svg", doc("Sketch.gdraw", "svg").downloadName())
        // Without the known extension the format is added.
        assertEquals("Notes.docx", doc("Notes", "docx").downloadName())
        assertEquals("photo.jpg", doc("photo.jpg", null).downloadName())
    }

    private fun FileObject.downloadName() = Connected.downloadName(this)

    @Test
    fun externalUrlOnlyHttps() {
        val plain = FileObject(metadata = mapOf(Connected.META_EXTERNAL_URL to "http://drive.example.com/x"))
        val script = FileObject(metadata = mapOf(Connected.META_EXTERNAL_URL to "javascript:alert(1)"))
        assertNull(Connected.externalUrl(plain))
        assertNull(Connected.externalUrl(script))
    }

    @Test
    fun folderThatCannotBeEnteredButHasAnAddressOpensInTheBrowser() {
        val f = FileObject(type = 1, capability = "AAAI", metadata = mapOf(Connected.META_EXTERNAL_URL to "https://drive.example.com/f"))
        assertEquals(Connected.Tap.BROWSER, Connected.tapAction(f))
        assertEquals(Connected.Tap.SHEET, Connected.tapAction(FileObject(type = 1, capability = "AAAI")))
    }

    @Test
    fun sortingOnlyByWhatTheServerOffers() {
        assertEquals(SortBy.UPDATED, Connected.sortFor(SortBy.CREATED))
        assertEquals(SortBy.NAME, Connected.sortFor(SortBy.NAME))
        assertEquals(SortBy.SIZE, Connected.sortFor(SortBy.SIZE))
        assertEquals(SortBy.UPDATED, Connected.sortFor(SortBy.UPDATED))
    }

    @Test
    fun writingStaysOffEvenWhenOwnedAndBitsAreSet() {
        val mine = FileObject(path = "cloudreve://abc@connected/a.txt", owned = true, capability = "////////")
        val folder = FileObject(type = 1, path = "cloudreve://abc@connected/Docs", owned = true, capability = "////////")
        assertFalse(Connected.writesAllowed(mine.path))
        assertFalse(Permissions.canModifyFile(mine))
        assertFalse(Permissions.canShareLink(mine))
        assertFalse(Permissions.canAddTo(folder))
        // Own files are not affected.
        assertTrue(Permissions.canModifyFile(FileObject(path = "cloudreve://my/a.txt", owned = true)))
    }

    @Test
    fun errorsOfConnectedStorages() {
        val changed = ApiException(40081, "batch", innerCodes = listOf(40077))
        assertEquals(40077, Connected.codeOf(changed))
        assertTrue(Connected.isChanged(changed))
        assertTrue(Connected.isChanged(ApiException(409, "HTTP 409")))
        assertTrue(Connected.isChanged(ApiException(40925, "x", reason = "changed")))
        assertFalse(Connected.isChanged(ApiException(40925, "x", reason = "ambiguous")))

        assertTrue(Connected.isStorageTrouble(ApiException(40921, "x")))
        assertTrue(Connected.isStorageTrouble(ApiException(40081, "x", innerCodes = listOf(40921))))
        assertTrue(Connected.isStorageTrouble(ApiException(40016, "x")))
        assertTrue(Connected.isStorageTrouble(ApiException(404, "HTTP 404")))
        assertFalse(Connected.isStorageTrouble(ApiException(40923, "x")))

        assertEquals(40923, (Connected.normalize(ApiException(503, "HTTP 503")) as ApiException).code)
        assertEquals(40923, (Connected.normalize(ApiException(429, "HTTP 429")) as ApiException).code)
        val normalized = Connected.normalize(ApiException(409, "HTTP 409")) as ApiException
        assertEquals(40925, normalized.code)
        assertEquals("changed", normalized.reason)
        val other = ApiException(40016, "x")
        assertTrue(Connected.normalize(other) === other)
    }

    @Test
    fun rootNamesFromTheOverviewTellEqualNamesApart() {
        val overview = example<ConnectedOverview>("storages").data!!
        val (first, second) = overview.storages
        Connected.nameStorages(overview.storages)
        assertEquals("Google Drive (${first.account})", CrUri.rootLabel(first.uri + "/Docs"))
        assertEquals("Google Drive (${second.account})", CrUri.displayName(second.uri))
        // A storage that is gone loses its name.
        Connected.nameStorages(listOf(first))
        assertEquals("Google Drive", CrUri.rootLabel(first.uri))
        assertEquals("Connected storage", CrUri.rootLabel(second.uri))
    }

    @Test
    fun rootNameFromTheListingUntilTheOverviewIsThere() {
        val rootUri = root.parent!!.path
        Connected.nameFromListing(rootUri, root.parent?.name)
        assertEquals("Google Drive", CrUri.rootLabel("$rootUri/Docs"))
        // Only a root listing names its storage.
        Connected.nameFromListing("cloudreve://other@connected/Docs", "Docs")
        assertEquals("Connected storage", CrUri.rootLabel("cloudreve://other@connected"))
        // The overview wins.
        Connected.nameStorages(listOf(ConnectedStorage(id = "x", name = "Drive", uri = rootUri)))
        assertEquals("Drive", CrUri.rootLabel(rootUri))
    }

    @Test
    fun aTimeOfTheYearOneIsUnknown() {
        assertFalse(Connected.knownTime("0001-01-01T00:00:00Z"))
        assertFalse(Connected.knownTime(""))
        assertTrue(Connected.knownTime(row("notes.txt").updatedAt))
    }

    @Test
    fun noSearchInsideConnectedStorages() {
        assertFalse(CrUri.isSearchScope("cloudreve://abc@connected"))
        assertFalse(CrUri.isSearchScope("cloudreve://abc@connected/Docs"))
        assertTrue(CrUri.isSearchScope("cloudreve://my/Docs"))
    }
}
