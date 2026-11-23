package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.data.ConnectFlow.Next
import io.github.svenbuild.fylvo.android.data.ConnectFlow.Return
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo 1.6: connecting a storage from the app, the server's contract for apps (plan, 14). */
class ConnectFlowTest {
    @Test fun challengeIsPkceS256() {
        assertEquals("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", ConnectFlow.challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
        val v = ConnectFlow.newVerifier()
        assertEquals(43, v.length)
        assertTrue(v.all { it.isLetterOrDigit() || it == '-' || it == '_' })
    }

    @Test fun startAddressOnlyInItsExactForm() {
        val ticket = "fRdT4OwP_rJBV0pegosRDUSAOJG-9aDoTG96_3CbXSo"
        assertTrue(ConnectFlow.validStart("https://cloud.example.com/api/v4/fylvo/connected/start/$ticket", allowHttp = false))
        // Another host than the one the app talks to is fine (the first site address).
        assertTrue(ConnectFlow.validStart("https://other.example.com/api/v4/fylvo/connected/start/$ticket", allowHttp = false))
        assertFalse(ConnectFlow.validStart("http://cloud.example.com/api/v4/fylvo/connected/start/$ticket", allowHttp = false))
        assertTrue(ConnectFlow.validStart("http://10.0.2.2:5290/api/v4/fylvo/connected/start/$ticket", allowHttp = true))
        assertFalse(ConnectFlow.validStart("https://user@cloud.example.com/api/v4/fylvo/connected/start/$ticket", allowHttp = false))
        assertFalse(ConnectFlow.validStart("https://cloud.example.com/api/v4/fylvo/connected/start/$ticket?x=1", allowHttp = false))
        assertFalse(ConnectFlow.validStart("https://cloud.example.com/api/v4/fylvo/connected/start/$ticket#f", allowHttp = false))
        assertFalse(ConnectFlow.validStart("https://cloud.example.com/api/v4/fylvo/connected/start/short", allowHttp = false))
        assertFalse(ConnectFlow.validStart("https://cloud.example.com/evil/api/v4/fylvo/connected/start/$ticket", allowHttp = false))
        assertFalse(ConnectFlow.validStart("javascript:alert(1)", allowHttp = true))
    }

    @Test fun returnLinkOnlyInItsExactForms() {
        val a = "sNvTyTD6oBj493Y0"
        val code = "A".repeat(32)
        fun p(q: String?, scheme: String = "fylvo", host: String = "connected", path: String? = "", fragment: String? = null) =
            ConnectFlow.parseReturn(scheme, host, path, q, fragment)
        assertEquals(Return.Finish(a, code), p("attempt=$a&finish=$code"))
        assertEquals(Return.Error(a, "denied"), p("attempt=$a&error=denied"))
        assertEquals(Return.Front, p(null))
        assertNull(p("attempt=$a&error=whatever"))
        assertNull(p("attempt=$a&finish=$code&error=denied"))
        assertNull(p("attempt=$a&attempt=$a&finish=$code"))
        assertNull(p("attempt=$a&finish=$code&x=1"))
        assertNull(p("attempt=short&finish=$code"))
        assertNull(p("attempt=$a&finish=short"))
        assertNull(p("attempt=$a&finish=$code", scheme = "https"))
        assertNull(p("attempt=$a&finish=$code", host = "other"))
        assertNull(p("attempt=$a&finish=$code", path = "/x"))
        assertNull(p("attempt=$a&finish=$code", fragment = "f"))
    }

    @Test fun answersOfFinish() {
        assertEquals(Next.Repeat, ConnectFlow.next(null, null, false))
        assertEquals(Next.End, ConnectFlow.next(40922, "not_allowed", false))
        assertEquals(Next.End, ConnectFlow.next(40922, "app_signin_off", true))
        assertEquals(Next.KeepWaiting, ConnectFlow.next(40001, "expired", sentBefore = false))
        assertEquals(Next.EndAndReload, ConnectFlow.next(40001, "expired", sentBefore = true))
        assertEquals(Next.KeepWaiting, ConnectFlow.next(40001, "account", false))
        assertEquals(Next.KeepWaiting, ConnectFlow.next(40001, "verifier", false))
        assertEquals(Next.EndAndReload, ConnectFlow.next(40004, "exists", false))
        assertEquals(Next.End, ConnectFlow.next(40001, "other_account", false))
        assertEquals(Next.End, ConnectFlow.next(40001, "too_many", false))
        assertEquals(Next.EndAndReload, ConnectFlow.next(40016, null, false))
        assertEquals(Next.EndAndReload, ConnectFlow.next(40001, "overtaken", false))
        assertEquals(Next.EndAndReload, ConnectFlow.next(500, null, false))
    }
}
