package io.github.svenbuild.fylvo.android.data

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.LocalDate

/** Fylvo 1.6: the text editor reads and writes exactly UTF-8 (design/android-parity-plan.md, 4). */
class TextRepositoryTest {
    @Test fun byteOrderMarkIsKeptButNotShown() {
        val withBom = byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + "Grüezi".toByteArray()
        val (text, bom) = TextRepository.decode(withBom)
        assertEquals("Grüezi", text)
        assertTrue(bom)
        assertArrayEquals(withBom, TextRepository.encode(text, bom))
        val (plain, noBom) = TextRepository.decode("a\r\nb".toByteArray())
        assertEquals("a\r\nb", plain)
        assertFalse(noBom)
    }

    @Test(expected = TextRepository.NotText::class) fun brokenUtf8IsNoText() {
        TextRepository.decode(byteArrayOf(0x61, 0xC3.toByte(), 0x28))
    }

    @Test(expected = TextRepository.NotText::class) fun binaryIsNoText() {
        TextRepository.decode(byteArrayOf(0x50, 0x4B, 0x00, 0x03))
    }

    @Test fun copyNamesKeepTheEnding() {
        val names = TextRepository.copyNames("notiz.md", LocalDate.of(2026, 11, 23))
        assertEquals("notiz (copy 2026-11-23).md", names[0])
        assertEquals("notiz (copy 2026-11-23 2).md", names[1])
        assertEquals(10, names.size)
        assertEquals("README (copy 2026-11-23)", TextRepository.copyNames("README", LocalDate.of(2026, 11, 23))[0])
        assertEquals(".env (copy 2026-11-23)", TextRepository.copyNames(".env", LocalDate.of(2026, 11, 23))[0])
    }
}
