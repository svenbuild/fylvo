package io.github.svenbuild.fylvo.android.ui.files

import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class FileIconsTest {

    @Test fun defaultsMatchTheServerTable() {
        assertEquals(24, FileIcons.DEFAULTS.size)
        val pdf = FileIcons.find("Report.PDF", FileIcons.DEFAULTS)!!
        assertEquals("pdf", pdf.icon)
        assertEquals(0xFFB53E3B.toInt(), FileIcons.argb(pdf, dark = false))
        assertEquals(0xFFF27B75.toInt(), FileIcons.argb(pdf, dark = true))
    }

    @Test fun darkFallsBackToTheLightColour() {
        val excel = FileIcons.find("a.xlsx", FileIcons.DEFAULTS)!!
        assertEquals(0xFF4CAF50.toInt(), FileIcons.argb(excel, dark = true))
    }

    @Test fun unknownTypesAndEntriesWithoutColourHaveNone() {
        assertNull(FileIcons.find("notes", FileIcons.DEFAULTS))
        assertNull(FileIcons.find("a.unknown", FileIcons.DEFAULTS))
        assertNull(FileIcons.argb(FileIcons.find("a.excalidraw", FileIcons.DEFAULTS)!!, dark = false))
    }

    @Test fun serverTableAsStringOrList() {
        val list = """[{"exts":["pdf"],"icon":"pdf","color":"#123456","color_dark":"#abc","img":"x"}]"""
        val fromString = FileIcons.parse(JsonPrimitive(list))!!
        assertEquals(listOf(FileTypeIcon(listOf("pdf"), "pdf", "#123456", "#abc")), fromString)
        assertEquals(fromString, FileIcons.parse(Json.parseToJsonElement(list)))
        assertNull(FileIcons.parse(JsonPrimitive("not json")))
        assertNull(FileIcons.parse(null))
    }

    @Test fun hexColours() {
        assertEquals(0xFF000000.toInt(), FileIcons.parseHexColor("#000"))
        assertEquals(0xFFAABBCC.toInt(), FileIcons.parseHexColor("#abc"))
        assertEquals(0x80112233.toInt(), FileIcons.parseHexColor("#80112233"))
        assertNull(FileIcons.parseHexColor("red"))
        assertNull(FileIcons.parseHexColor(""))
    }

    @Test fun veryDarkColoursAreLightenedInTheDarkLookOnly() {
        val exe = FileIcons.find("setup.exe", FileIcons.DEFAULTS)!!
        assertEquals(0xFF1A237E.toInt(), FileIcons.argb(exe, dark = false))
        val dark = FileIcons.argb(exe, dark = true)!!
        assertTrue(FileIcons.luminance(dark) >= FileIcons.MIN_DARK_LUMINANCE)
        assertTrue(FileIcons.luminance(dark) < 0.6)
        // Own dark colours and light enough colours stay as they are.
        assertEquals(0xFFFFFFFF.toInt(), FileIcons.argb(FileIcons.find("main.rs", FileIcons.DEFAULTS)!!, dark = true))
        assertEquals(0xFF4CAF50.toInt(), FileIcons.lightenForDark(0xFF4CAF50.toInt()))
    }
}
