package io.github.svenbuild.fylvo.android.data.prefs

import android.content.Intent
import android.net.Uri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Fylvo: what the app accepts from shortcuts, the widget and other apps. */
@RunWith(RobolectricTestRunner::class)
@org.robolectric.annotation.Config(sdk = [34])
class IncomingTest {
    private val hash = FylvoActions.accountHash("https://cloud.example.com|u1")

    private fun open(uri: String, account: String?) = Intent(FylvoActions.OPEN).setData(Uri.parse(uri)).apply {
        if (account != null) putExtra(FylvoActions.EXTRA_ACCOUNT, account)
    }

    @Test
    fun navigationActions() {
        assertEquals(Incoming.Search, FylvoActions.parse(Intent(FylvoActions.SEARCH), hash))
        assertEquals(Incoming.Upload, FylvoActions.parse(Intent(FylvoActions.UPLOAD), hash))
        assertEquals(Incoming.Favorites, FylvoActions.parse(Intent(FylvoActions.FAVORITES), hash))
        assertNull(FylvoActions.parse(Intent(Intent.ACTION_VIEW), hash))
        assertNull(FylvoActions.parse(null, hash))
    }

    @Test
    fun openOnlyOwnFoldersOfThisAccount() {
        assertEquals(Incoming.Open("cloudreve://my/Projects", true), FylvoActions.parse(open("cloudreve://my/Projects", hash), hash))
        // Another account, no account, signed out: nothing.
        assertNull(FylvoActions.parse(open("cloudreve://my/Projects", "deadbeef"), hash))
        assertNull(FylvoActions.parse(open("cloudreve://my/Projects", null), hash))
        assertNull(FylvoActions.parse(open("cloudreve://my/Projects", ""), ""))
        // Other roots, searches, the root itself, climbing up: nothing.
        listOf("cloudreve://trash/x", "cloudreve://abc@share/x", "cloudreve://my", "cloudreve://my?category=image", "cloudreve://my/a/../b", "https://example.com")
            .forEach { assertNull(it, FylvoActions.parse(open(it, hash), hash)) }
    }

    @Test
    fun accountHashIsShortAndStable() {
        assertEquals(16, hash.length)
        assertEquals(hash, FylvoActions.accountHash("https://cloud.example.com|u1"))
        assertFalse(hash == FylvoActions.accountHash("https://cloud.example.com|u2"))
        assertEquals("", FylvoActions.accountHash(""))
        assertTrue(FylvoActions.openAllowed("cloudreve://my/A B"))
    }
}
