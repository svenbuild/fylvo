package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ArchiveEntry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo 1.6: archives (design/android-parity-plan.md, 11). */
class ArchiveTest {
    @Test fun whatTheServerCanOpen() {
        listOf("a.zip", "A.ZIP", "b.rar", "c.7z", "d.tar", "e.tar.gz", "f.tgz", "g.tar.xz", "h.tar.bz2").forEach { assertTrue(it, ArchiveRepository.extractable(it)) }
        listOf("a.gz", "b.xz", "c.bz2", "d.txt", "zip").forEach { assertFalse(it, ArchiveRepository.extractable(it)) }
        assertTrue(ArchiveRepository.viewable("a.zip"))
        assertTrue(ArchiveRepository.viewable("a.7z"))
        assertFalse(ArchiveRepository.viewable("a.rar"))
    }

    @Test fun zipNames() {
        assertEquals("Fotos.zip", ArchiveRepository.zipName(listOf("Fotos"), "Archiv"))
        assertEquals("plan.zip", ArchiveRepository.zipName(listOf("plan.txt"), "Archiv"))
        assertEquals("Archiv.zip", ArchiveRepository.zipName(listOf("a", "b"), "Archiv"))
    }

    @Test fun browsingAnArchive() {
        val entries = listOf(
            ArchiveEntry("Projekte/plan.txt", 11),
            ArchiveEntry("Projekte/Sub/x.md", 3),
            ArchiveEntry("readme.txt", 5),
            ArchiveEntry("Leer/", 0, isDirectory = true),
        )
        assertEquals(listOf("Leer", "Projekte", "readme.txt"), ArchiveRepository.childrenOf(entries, "").map { it.name })
        assertTrue(ArchiveRepository.childrenOf(entries, "").first { it.name == "Projekte" }.isDirectory)
        assertEquals(listOf("Projekte/Sub", "Projekte/plan.txt"), ArchiveRepository.childrenOf(entries, "Projekte").map { it.name })
        assertEquals(emptyList<String>(), ArchiveRepository.childrenOf(entries, "Leer").map { it.name })
    }
}
