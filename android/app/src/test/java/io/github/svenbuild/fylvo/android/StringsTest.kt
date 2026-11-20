package io.github.svenbuild.fylvo.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/** English (values/) and German (values-de/) must hold the same texts; German keeps the style rules. */
class StringsTest {
    private val english = File("src/main/res/values/strings.xml")
    private val german = File("src/main/res/values-de/strings.xml")

    /** Names of the translatable strings and plurals in [file]. */
    private fun keys(file: File): Set<String> {
        val doc = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file)
        return listOf("string", "plurals").flatMap { tag ->
            val nodes = doc.getElementsByTagName(tag)
            (0 until nodes.length).map { nodes.item(it) as Element }
        }.filter { it.getAttribute("translatable") != "false" }
            .map { it.getAttribute("name") }
            .toSet()
    }

    @Test fun everyEnglishTextHasAGermanOne() {
        assertEquals(emptySet<String>(), keys(english) - keys(german))
    }

    @Test fun germanHasNoExtraTexts() {
        assertEquals(emptySet<String>(), keys(german) - keys(english))
    }

    @Test fun germanAvoidsSharpSAndEmDash() {
        val text = german.readText()
        assertFalse("values-de must not contain the sharp s", text.contains('ß'))
        assertFalse("values-de must not contain the em dash", text.contains('—'))
    }
}
