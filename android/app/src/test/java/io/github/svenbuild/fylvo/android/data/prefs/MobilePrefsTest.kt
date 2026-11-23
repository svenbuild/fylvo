package io.github.svenbuild.fylvo.android.data.prefs

import io.github.svenbuild.fylvo.android.api.ApiResponse
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.FylvoPrefs
import io.github.svenbuild.fylvo.android.api.SiteBasic
import io.github.svenbuild.fylvo.android.api.decodeApiResponse
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.ui.theme.DarkColors
import io.github.svenbuild.fylvo.android.ui.theme.LightColors
import io.github.svenbuild.fylvo.android.ui.theme.fylvoColorScheme
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.serializer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import retrofit2.Response

/** Fylvo: the rules of the customisation (MobilePrefs, Accents, answers of the server). */
class MobilePrefsTest {
    private val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }
    private fun text(name: String) = javaClass.classLoader!!.getResource("prefs/$name.json")!!.readText()
    private fun mobile(text: String): JsonObject = json.parseToJsonElement(text) as JsonObject

    // ---- the server's answers ----------------------------------------------

    @Test
    fun prefsOfTheServerAreRead() {
        val prefs = decodeApiResponse(json, serializer<FylvoPrefs>(), text("prefs")).data!!
        assertEquals("compact", prefs.view.density)
        assertEquals("#7484F4", prefs.appearance.accent)
        assertTrue(prefs.customized.isEmpty())
        assertEquals(listOf("RaC9"), prefs.favorites)
        assertTrue(prefs.sidebar.items.first { it.type == "home" }.hidden)
        assertEquals(5, prefs.home.widgets.size)
        // Without customisation: like the device, compact, today's tabs, no swipes.
        assertEquals(ThemeMode.SYSTEM, MobilePrefs.mode(prefs))
        assertEquals(Density.COMPACT, MobilePrefs.density(prefs))
        assertEquals(MobilePrefs.DEFAULT_TABS, MobilePrefs.tabs(prefs.mobile))
        assertEquals(SwipePrefs(), MobilePrefs.swipe(prefs.mobile))
    }

    @Test
    fun accentsOfTheSite() {
        val basic = decodeApiResponse(json, serializer<SiteBasic>(), text("basic")).data!!
        val accents = Accents.parse(basic.themes)
        assertEquals(listOf("#7484F4", "#2EC4B6", "#4CC38A", "#F5A524", "#F2735B", "#A78BFA"), accents.map { it.key })
        assertEquals(0xFF7484F4.toInt(), accents[0].dark)
        assertEquals(0xFF5565E8.toInt(), accents[0].light)
        assertEquals(emptyList<Accent>(), Accents.parse("not json"))
        assertEquals(emptyList<Accent>(), Accents.parse(""))
    }

    @Test
    fun defaultAccentKeepsTodaysColours() {
        val accent = Accent("#7484F4", 0xFF7484F4.toInt(), 0xFF5565E8.toInt())
        assertEquals(DarkColors, fylvoColorScheme(true, accent))
        assertEquals(LightColors, fylvoColorScheme(false, accent))
        assertEquals(DarkColors, fylvoColorScheme(true, null))
    }

    @Test
    fun otherAccentsGetReadableText() {
        val teal = Accent("#2EC4B6", 0xFF2EC4B6.toInt(), 0xFF0F8C81.toInt())
        val dark = fylvoColorScheme(true, teal)
        assertEquals(0xFF2EC4B6.toInt(), dark.primary.value.shr(32).toInt())
        listOf(Accent("#F5A524", 0xFFF5A524.toInt(), 0xFFB07205.toInt()), teal).forEach { a ->
            listOf(a.dark, a.light).forEach { c -> assertTrue(Accents.contrast(c, Accents.onColor(c)) >= 3.0) }
        }
        // Surfaces stay the Fylvo ones.
        assertEquals(DarkColors.surface, dark.surface)
        assertEquals(DarkColors.background, dark.background)
    }

    @Test
    fun outcomes() {
        fun <T> rsp(code: Int, body: String): Response<ApiResponse<T>> {
            return if (code in 200..299) {
                @Suppress("UNCHECKED_CAST")
                Response.success(code, decodeApiResponse(json, serializer<Unit>(), body) as ApiResponse<T>)
            } else Response.error(code, body.toResponseBody("text/plain".toMediaType()))
        }
        assertEquals(Outcome.Missing, outcomeOf(rsp<Unit>(404, "404 page not found")))
        assertEquals(Outcome.Missing, outcomeOf(rsp<Unit>(200, """{"code":404,"msg":"Not found"}""")))
        assertEquals(Outcome.Failed, outcomeOf(rsp<Unit>(502, "Bad gateway")))
        assertEquals(Outcome.Refused(40001, "Invalid"), outcomeOf(rsp<Unit>(200, """{"code":40001,"msg":"Invalid"}""")))
        assertTrue(outcomeOf(rsp<Unit>(200, """{"code":0}""")) is Outcome.Found)
        try {
            outcomeOf(rsp<Unit>(200, """{"code":401,"msg":"Login required"}"""))
            fail("login required must throw")
        } catch (e: ApiException) {
            assertEquals(ApiException.CODE_LOGIN_REQUIRED, e.code)
        }
    }

    // ---- tabs ----------------------------------------------------------------

    @Test
    fun tabsFromMobile() {
        assertEquals(
            listOf(Place.Home, Place.Files, Place.Favorites, Place.Account),
            MobilePrefs.tabs(mobile("""{"v":1,"tabs":["home","files","favorites","account"]}""")),
        )
        // Files and Account are always there; Account last when missing.
        assertEquals(
            listOf(Place.Favorites, Place.Tags, Place.Files, Place.Account),
            MobilePrefs.tabs(mobile("""{"tabs":["favorites","tags"]}""")),
        )
        // Unknown, repeated, broken: skipped. Only Files + Account left: the default.
        assertEquals(MobilePrefs.DEFAULT_TABS, MobilePrefs.tabs(mobile("""{"tabs":["calendar","files","files",3,"account"]}""")))
        assertEquals(MobilePrefs.DEFAULT_TABS, MobilePrefs.tabs(mobile("""{"tabs":"home"}""")))
        assertEquals(MobilePrefs.DEFAULT_TABS, MobilePrefs.tabs(null))
        // At most five, Files and Account kept.
        assertEquals(
            listOf(Place.Home, Place.Shared, Place.Favorites, Place.Files, Place.Account),
            MobilePrefs.tabs(mobile("""{"tabs":["home","shared","favorites","tags","recent","files","account"]}""")),
        )
    }

    @Test
    fun tabsWithLinksStoragesAndCategories() {
        val m = mobile("""{"tabs":["link:cloudreve://my/Projects","connected:abc","category:image","category:books","link:https://x","files","account"]}""")
        assertEquals(
            listOf(Place.Link("cloudreve://my/Projects"), Place.Storage("abc"), Place.Category("image"), Place.Files, Place.Account),
            MobilePrefs.tabs(m),
        )
        // A shortcut or storage that is gone is skipped.
        assertEquals(
            listOf(Place.Category("image"), Place.Files, Place.Account),
            MobilePrefs.tabs(m, linkExists = { false }, storageExists = { false }),
        )
    }

    @Test
    fun writingMobileKeepsOtherFields() {
        val current = mobile("""{"v":1,"swipe":{"left":"delete","right":"none"},"desktop":{"x":1}}""")
        val written = MobilePrefs.withTabs(current, listOf(Place.Favorites, Place.Home))
        assertEquals(JsonObject(mapOf("x" to JsonPrimitive(1))), written["desktop"])
        assertEquals(current["swipe"], written["swipe"])
        assertEquals(listOf("favorites", "home", "files", "account"), written["tabs"]!!.jsonArray.map { (it as JsonPrimitive).content })
        val swiped = MobilePrefs.withSwipe(written, SwipePrefs(SwipeAction.OFFLINE, SwipeAction.FAVORITE))
        assertEquals(SwipePrefs(SwipeAction.OFFLINE, SwipeAction.FAVORITE), MobilePrefs.swipe(swiped))
        assertEquals(written["tabs"], swiped["tabs"])
        assertEquals(SwipePrefs(SwipeAction.NONE, SwipeAction.DELETE), MobilePrefs.swipe(mobile("""{"swipe":{"left":"fly","right":"delete"}}""")))
    }

    // ---- account parts ---------------------------------------------------------

    @Test
    fun modeOnlyWhenSaved() {
        val dark = FylvoPrefs(appearance = io.github.svenbuild.fylvo.android.api.Appearance(mode = "dark"))
        assertEquals(ThemeMode.SYSTEM, MobilePrefs.mode(dark))
        assertEquals(ThemeMode.DARK, MobilePrefs.mode(dark.copy(customized = listOf("mode"))))
        assertEquals(ThemeMode.SYSTEM, MobilePrefs.mode(null))
        assertEquals(Density.COMFORTABLE, MobilePrefs.density(FylvoPrefs(view = io.github.svenbuild.fylvo.android.api.PrefsView(density = "comfortable"))))
        assertEquals(Density.COMPACT, MobilePrefs.density(FylvoPrefs(view = io.github.svenbuild.fylvo.android.api.PrefsView(density = "huge"))))
    }

    @Test
    fun chipsFollowTheSidebar() {
        val prefs = decodeApiResponse(json, serializer<FylvoPrefs>(), text("prefs")).data!!
        val withLink = prefs.copy(
            sidebar = FylvoPrefs.Sidebar(
                prefs.sidebar.items + io.github.svenbuild.fylvo.android.api.SidebarItem(type = "link", uri = "cloudreve://my/Projects", name = "Projects") +
                    io.github.svenbuild.fylvo.android.api.SidebarItem(type = "heading", name = "Work") +
                    io.github.svenbuild.fylvo.android.api.SidebarItem(type = "connected", id = "d1"),
            ),
        )
        val tabs = MobilePrefs.DEFAULT_TABS
        assertEquals(
            listOf(
                Place.Favorites, Place.Category("image"), Place.Category("video"), Place.Category("audio"), Place.Category("document"),
                Place.Trash, Place.Link("cloudreve://my/Projects"), Place.Storage("d1"),
            ),
            MobilePrefs.chips(withLink, tabs, hasFavorites = true, hasTags = false),
        )
        // Without a Shared tab "Shared with me" is always offered, without Home the storages too.
        val noShared = listOf(Place.Files, Place.Favorites, Place.Account)
        val hidden = prefs.copy(sidebar = FylvoPrefs.Sidebar(prefs.sidebar.items.map { if (it.type == "shared_with_me") it.copy(hidden = true) else it }))
        val chips = MobilePrefs.chips(hidden, noShared, hasFavorites = true, hasTags = true, storages = listOf("d2"))
        assertTrue(Place.Shared in chips)
        assertTrue(Place.Storage("d2") in chips)
        assertFalse(Place.Favorites in chips) // it is a tab already
        assertEquals(emptyList<Place>(), MobilePrefs.chips(null, tabs, true, true))
    }

    // ---- star, colour and tags ---------------------------------------------------

    private fun file(path: String, id: String = "f1", owned: Boolean? = true, type: Int = 0, metadata: Map<String, String>? = null) =
        FileObject(type = type, id = id, name = path.substringAfterLast('/'), path = path, owned = owned, metadata = metadata)

    @Test
    fun starRules() {
        val none = emptySet<String>()
        val member: (String) -> Boolean? = { it == "m1" }
        assertEquals(StarState.CAN_ADD, MobilePrefs.starState(file("cloudreve://my/Projects"), none, member))
        assertEquals(StarState.CAN_REMOVE, MobilePrefs.starState(file("cloudreve://my/Projects"), setOf("f1"), member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://my"), none, member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://trash/x"), none, member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://shared_with_me/x"), none, member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://d1@connected/x"), none, member))
        assertEquals(StarState.CAN_ADD, MobilePrefs.starState(file("cloudreve://m1@share/Doc.pdf", owned = false), none, member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://p1@share/Doc.pdf", owned = false), none, member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://m1@share/Doc.pdf", owned = true), none, member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://m1@share/Doc.pdf", owned = false), none) { null })
        // Whatever is a favourite can always lose its star (even a public link or a connected row).
        assertEquals(StarState.CAN_REMOVE, MobilePrefs.starState(file("cloudreve://p1@share/Doc.pdf", owned = false), setOf("f1"), member))
        assertEquals(StarState.NONE, MobilePrefs.starState(file("cloudreve://my/x", id = ""), none, member))
    }

    @Test
    fun metadataOnlyForTheOwner() {
        assertTrue(MobilePrefs.canEditMetadata(listOf(file("cloudreve://my/a"), file("cloudreve://my/b"))))
        assertFalse(MobilePrefs.canEditMetadata(listOf(file("cloudreve://my/a"), file("cloudreve://m1@share/b", owned = false))))
        assertFalse(MobilePrefs.canEditMetadata(listOf(file("cloudreve://m1@share/b", owned = true))))
        assertFalse(MobilePrefs.canEditMetadata(listOf(file("cloudreve://my"))))
        assertFalse(MobilePrefs.canEditMetadata(emptyList()))
    }

    @Test
    fun tagsAndColourFromMetadata() {
        val f = file("cloudreve://my/a", metadata = mapOf("tag:Work" to "#2EC4B6", "tag:alpha" to "", "tag:" to "x", "customize:icon_color" to "#4CC38A"))
        assertEquals(listOf("alpha" to "", "Work" to "#2EC4B6"), MobilePrefs.tagsOf(f))
        assertEquals(null, MobilePrefs.folderColor(f))
        assertEquals("#4CC38A", MobilePrefs.folderColor(f.copy(type = 1)))
    }

    @Test
    fun tagSearchLikeTheWebApp() {
        assertEquals("cloudreve://my?meta_tag%3AWork=", MobilePrefs.tagSearchUri("Work"))
        assertEquals("cloudreve://my?meta_tag%3AA+%26+B%3D=", MobilePrefs.tagSearchUri("A & B="))
    }

    @Test
    fun unusedJsonArrayStaysAnArray() {
        assertTrue(MobilePrefs.withTabs(null, emptyList())["tabs"] is JsonArray)
    }
}

class SelectionStarTest {
    private val s = StarState.entries

    @org.junit.Test
    fun starOfASelection() {
        org.junit.Assert.assertEquals(StarState.CAN_REMOVE, io.github.svenbuild.fylvo.android.ui.files.starOfSelection(listOf(StarState.CAN_REMOVE, StarState.CAN_REMOVE)))
        org.junit.Assert.assertEquals(StarState.CAN_ADD, io.github.svenbuild.fylvo.android.ui.files.starOfSelection(listOf(StarState.CAN_REMOVE, StarState.CAN_ADD)))
        org.junit.Assert.assertEquals(StarState.NONE, io.github.svenbuild.fylvo.android.ui.files.starOfSelection(listOf(StarState.CAN_ADD, StarState.NONE)))
        org.junit.Assert.assertEquals(StarState.NONE, io.github.svenbuild.fylvo.android.ui.files.starOfSelection(emptyList()))
        org.junit.Assert.assertEquals(3, s.size)
    }

    @org.junit.Test
    fun menuOffersStarColourAndTags() {
        val folder = io.github.svenbuild.fylvo.android.api.FileObject(type = 1, id = "a", name = "A", path = "cloudreve://my/A", owned = true)
        val ctx = io.github.svenbuild.fylvo.android.ui.files.SelectionContext(star = StarState.CAN_ADD, metadata = true)
        val menu = io.github.svenbuild.fylvo.android.ui.files.selectionActions(listOf(folder), ctx).menu
        org.junit.Assert.assertTrue(io.github.svenbuild.fylvo.android.ui.files.SelectionAction.FAVORITE in menu)
        org.junit.Assert.assertTrue(io.github.svenbuild.fylvo.android.ui.files.SelectionAction.COLOR in menu)
        org.junit.Assert.assertTrue(io.github.svenbuild.fylvo.android.ui.files.SelectionAction.TAGS in menu)
        val file = folder.copy(type = 0)
        val menuFile = io.github.svenbuild.fylvo.android.ui.files.selectionActions(listOf(file), ctx.copy(star = StarState.CAN_REMOVE)).menu
        org.junit.Assert.assertTrue(io.github.svenbuild.fylvo.android.ui.files.SelectionAction.UNFAVORITE in menuFile)
        org.junit.Assert.assertFalse(io.github.svenbuild.fylvo.android.ui.files.SelectionAction.COLOR in menuFile)
        val trash = io.github.svenbuild.fylvo.android.ui.files.selectionActions(listOf(file), ctx.copy(inTrash = true)).menu
        org.junit.Assert.assertFalse(io.github.svenbuild.fylvo.android.ui.files.SelectionAction.FAVORITE in trash)
    }
}

class HomeWidgetsTest {
    @org.junit.Test
    fun homeOnlyWhenArranged() {
        val widgets = listOf(
            io.github.svenbuild.fylvo.android.api.HomeWidget("favorites"),
            io.github.svenbuild.fylvo.android.api.HomeWidget("recent"),
            io.github.svenbuild.fylvo.android.api.HomeWidget("storage", hidden = true),
            io.github.svenbuild.fylvo.android.api.HomeWidget("weather"),
        )
        val prefs = io.github.svenbuild.fylvo.android.api.FylvoPrefs(home = io.github.svenbuild.fylvo.android.api.FylvoPrefs.Home(widgets))
        org.junit.Assert.assertNull(MobilePrefs.homeWidgets(prefs))
        org.junit.Assert.assertNull(MobilePrefs.homeWidgets(null))
        org.junit.Assert.assertEquals(listOf("favorites", "recent"), MobilePrefs.homeWidgets(prefs.copy(customized = listOf("home"))))
    }
}
