package io.github.svenbuild.fylvo.android.util

import io.github.svenbuild.fylvo.android.api.ShareInfo
import io.github.svenbuild.fylvo.android.api.ShareOwner
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo 1.6: share links are only taken from the signed-in server (design/android-parity-plan.md, 3). */
class ShareLinksTest {
    private val base = "https://cloud.example.com"

    @Test fun ownServerLinksAreTaken() {
        assertEquals(ShareLinks.Ref("0wHe", ""), ShareLinks.parse("https://cloud.example.com/s/0wHe", base))
        assertEquals(ShareLinks.Ref("0wHe", "geheim1"), ShareLinks.parse("https://cloud.example.com/s/0wHe/geheim1", base))
        // Default port written out, trailing slash, extra query: the same server.
        assertEquals("0wHe", ShareLinks.parse("https://cloud.example.com:443/s/0wHe/?path=a", base)?.id)
        assertEquals("ab", ShareLinks.parse("HTTPS://Cloud.Example.com/s/ab", base)?.id)
        // A server below a path.
        assertEquals("ab", ShareLinks.parse("https://example.com/cloud/s/ab", "https://example.com/cloud")?.id)
        assertEquals("ab", ShareLinks.parse("http://10.0.2.2:5290/s/ab", "http://10.0.2.2:5290")?.id)
    }

    @Test fun everythingElseIsRefused() {
        assertNull(ShareLinks.parse("https://other.example.com/s/ab", base))
        assertNull(ShareLinks.parse("http://cloud.example.com/s/ab", base))
        assertNull(ShareLinks.parse("https://cloud.example.com:8443/s/ab", base))
        assertNull(ShareLinks.parse("https://cloud.example.com.evil.example/s/ab", base))
        assertNull(ShareLinks.parse("https://user:pw@cloud.example.com/s/ab", base))
        assertNull(ShareLinks.parse("https://cloud.example.com/x/ab", base))
        assertNull(ShareLinks.parse("https://cloud.example.com/s/", base))
        assertNull(ShareLinks.parse("https://cloud.example.com/s/ab/pw/more", base))
        assertNull(ShareLinks.parse("https://cloud.example.com/s/a-b", base))
        assertNull(ShareLinks.parse("https://cloud.example.com/s/ab/p%20w", base))
        assertNull(ShareLinks.parse("https://example.com/s/ab", "https://example.com/cloud"))
        assertNull(ShareLinks.parse("cloudreve://ab@share", base))
        assertNull(ShareLinks.parse("not a link", base))
    }

    @Test fun linksAreFoundInSharedText() {
        assertEquals("https://cloud.example.com/s/ab", ShareLinks.findUrl("Schau mal: https://cloud.example.com/s/ab."))
        assertNull(ShareLinks.findUrl("kein Link"))
        assertTrue(ShareLinks.looksLikeLink(" https://x"))
        assertFalse(ShareLinks.looksLikeLink("urlaub"))
    }

    @Test fun decisions() {
        val other = ShareOwner(id = "owner")
        // Someone else's share needs group right 7; unknown counts as no.
        assertEquals(ShareLinks.Decision.NotAllowed, ShareLinks.decide("ab", ShareInfo(owner = other, unlocked = true, name = "x"), "", "me", false))
        assertEquals(ShareLinks.Decision.NotAllowed, ShareLinks.decide("ab", ShareInfo(owner = other, unlocked = true, name = "x"), "", "me", null))
        // Locked: ask, and say "wrong" only after a password was tried.
        assertEquals(ShareLinks.Decision.NeedPassword("x", wrong = false), ShareLinks.decide("ab", ShareInfo(owner = other, passwordProtected = true, name = "x"), "", "me", true))
        assertEquals(ShareLinks.Decision.NeedPassword("x", wrong = true), ShareLinks.decide("ab", ShareInfo(owner = other, passwordProtected = true, name = "x"), "bad", "me", true))
        assertEquals(ShareLinks.Decision.Open("cloudreve://ab:pw1@share", "x"),
            ShareLinks.decide("ab", ShareInfo(owner = other, passwordProtected = true, unlocked = true, name = "x"), "pw1", "me", true))
        assertEquals(ShareLinks.Decision.Open("cloudreve://ab@share", "x"),
            ShareLinks.decide("ab", ShareInfo(owner = other, unlocked = true, name = "x"), "", "me", true))
        // The owner needs no group right and gets the password from the answer.
        assertEquals(ShareLinks.Decision.Open("cloudreve://ab:pw1@share", "x"),
            ShareLinks.decide("ab", ShareInfo(owner = ShareOwner("me"), passwordProtected = true, unlocked = true, password = "pw1", name = "x"), "", "me", false))
    }

    @Test fun secretAddressesStayOutOfPersistentPaths() {
        val f = io.github.svenbuild.fylvo.android.api.FileObject(type = 1, name = "d", path = "cloudreve://ab:pw1@share/d", capability = "QAAAAA==")
        // Offline store and uploads (the queue would keep the address).
        assertFalse(io.github.svenbuild.fylvo.android.ui.files.canKeepOffline(f))
        assertFalse(Permissions.canAddTo(f))
        // The star never asks the server with the password in the address.
        assertEquals(io.github.svenbuild.fylvo.android.data.prefs.StarState.NONE,
            io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs.starState(f.copy(id = "x1"), emptySet()) { true })
        val open = f.copy(path = "cloudreve://ab@share/d")
        assertTrue(io.github.svenbuild.fylvo.android.ui.files.canKeepOffline(open))
    }
}
