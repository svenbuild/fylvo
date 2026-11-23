package io.github.svenbuild.fylvo.android.data

import io.github.svenbuild.fylvo.android.api.ShareInfo
import io.github.svenbuild.fylvo.android.data.prefs.Navigation
import io.github.svenbuild.fylvo.android.data.prefs.Place
import io.github.svenbuild.fylvo.android.data.prefs.Where
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

/** Fylvo 1.6: the settings of a share link as they are sent (design/android-parity-plan.md, 2). */
class ShareOptionsTest {
    private val now = Instant.parse("2026-11-23T10:00:00Z")

    @Test fun remainingTimeIsRoundedUpAndNeverZero() {
        assertEquals(1, LinkOptions.remainingSeconds(now.plusMillis(1), now))
        assertEquals(2, LinkOptions.remainingSeconds(now.plusMillis(1001), now))
        assertEquals(0, LinkOptions.remainingSeconds(now, now))
        assertEquals(0, LinkOptions.remainingSeconds(now.minusSeconds(5), now))
        // Less than a second left: still sent as 1 (0 would clear the end).
        val almost = LinkOptions.of(ShareInfo(expires = "2026-11-23T10:00:00.400Z"), now)
        assertEquals(1L, almost.expireSeconds)
        assertEquals(1, almost.toRequest("cloudreve://my/a", isFolder = false, editing = true, now = now).expire)
    }

    @Test fun expiredLinkMustGetANewEnd() {
        val o = LinkOptions.of(ShareInfo(expires = "2026-11-22T10:00:00Z", expired = true), now)
        assertTrue(o.expireToChoose)
        assertFalse(o.complete)
        assertNull(o.expireSeconds)
        val never = LinkOptions.of(ShareInfo(expires = null), now)
        assertFalse(never.expireToChoose)
        assertNull(never.expireSeconds)
    }

    @Test fun usedUpDownloadsMustBeChosenAgain() {
        assertTrue(LinkOptions.of(ShareInfo(remainDownloads = 0), now).downloadsToChoose)
        assertEquals(3, LinkOptions.of(ShareInfo(remainDownloads = 3), now).downloads)
        assertNull(LinkOptions.of(ShareInfo(remainDownloads = null), now).downloads)
    }

    @Test fun editSendsEverySettingButNeverPasswordOrMembers() {
        val share = ShareInfo(
            id = "s1", sourceType = 1, allowUpload = true, hideContent = true, uploadLimit = 2L * 1024 * 1024 * 1024,
            shareView = true, showReadme = true, password = "abc", passwordProtected = true,
        )
        val o = LinkOptions.of(share, now)
        assertEquals(UploadMode.UPLOAD_ONLY, o.upload)
        val r = o.copy(password = "ignored").toRequest("cloudreve://my/d", isFolder = true, editing = true)
        assertEquals("", r.password)
        assertFalse(r.isPrivate)
        assertNull(r.members)
        assertTrue(r.allowUpload && r.hideContent && r.shareView && r.showReadme)
        // A limit set in the web (2 GB) is kept as it is.
        assertEquals(2L * 1024 * 1024 * 1024, r.uploadLimit)
        assertEquals(0, r.downloads)
    }

    @Test fun uploadsAndViewOnlyForFolders() {
        val o = LinkOptions(upload = UploadMode.VIEW_AND_UPLOAD, uploadLimit = 5, shareView = true, downloads = 4)
        val file = o.toRequest("cloudreve://my/a.txt", isFolder = false, editing = false)
        assertFalse(file.allowUpload)
        assertFalse(file.shareView)
        assertEquals(0L, file.uploadLimit)
        assertEquals(4, file.downloads)
        val folder = o.toRequest("cloudreve://my/d", isFolder = true, editing = false)
        assertTrue(folder.allowUpload)
        assertFalse(folder.hideContent)
        assertEquals(0, folder.downloads)
    }

    @Test fun passwordOnlyOnCreateAndOnlyLettersAndDigits() {
        val r = LinkOptions(password = "Abc123").toRequest("cloudreve://my/a", isFolder = false, editing = false)
        assertTrue(r.isPrivate)
        assertEquals("Abc123", r.password)
        assertTrue(LinkOptions.passwordValid("Abc123"))
        assertFalse(LinkOptions.passwordValid("abc-123"))
        assertFalse(LinkOptions.passwordValid(""))
        assertFalse(LinkOptions.passwordValid("a".repeat(33)))
        assertFalse(LinkOptions.passwordValid("äbc"))
    }

    @Test fun expiryIsCappedAtTheServersLimit() {
        val r = LinkOptions(expireSeconds = LinkOptions.MAX_EXPIRE_SECONDS + 100).toRequest("cloudreve://my/a", false, false)
        assertEquals(LinkOptions.MAX_EXPIRE_SECONDS.toInt(), r.expire)
    }

    @Test fun memberShareEditKeepsItsSettings() {
        val share = ShareInfo(id = "m1", memberOnly = true, shareView = true, showReadme = false)
        val r = ShareRepository.memberRequest("cloudreve://my/d", share, listOf(io.github.svenbuild.fylvo.android.api.ShareMemberDto("u1", true)), expireSeconds = 3600)
        assertTrue(r.memberOnly)
        assertEquals(3600, r.expire)
        assertTrue(r.shareView)
        assertEquals(1, r.members?.size)
    }

    @Test fun sharedByMeBelongsToSharedOrAccount() {
        val tabs = listOf(Place.Home, Place.Files, Place.Shared, Place.Account)
        assertEquals(Place.Shared, Navigation.activeTab(Where(uri = "cloudreve://shared_with_me", page = "shares"), tabs, emptyList(), Place.Shared))
        assertEquals(Place.Account, Navigation.activeTab(Where(account = true, page = "shares"), tabs, emptyList(), Place.Account))
        // On the page, back closes the page instead of leaving the tab.
        assertFalse(Navigation.atTabRoot(Where(uri = "cloudreve://shared_with_me", page = "shares"), Place.Shared, emptyList()))
        assertFalse(Navigation.atTabRoot(Where(account = true, page = "shares"), Place.Account, emptyList()))
        assertTrue(Navigation.atTabRoot(Where(account = true), Place.Account, emptyList()))
    }

    @Test fun unchangedEndIsCountedWhenSending() {
        val o = LinkOptions.of(ShareInfo(expires = "2026-11-23T12:00:00Z"), now)
        assertEquals(7200L, o.expireSeconds)
        // Sent ten minutes later: the end stays the same.
        assertEquals(6600, o.toRequest("cloudreve://my/a", false, true, now = now.plusSeconds(600)).expire)
        // A new choice replaces it.
        assertEquals(86400, o.copy(expireSeconds = 86400, keepEnd = null).toRequest("cloudreve://my/a", false, true, now = now.plusSeconds(600)).expire)
    }

    @Test fun folderLinksIgnoreOldDownloadLimits() {
        val o = LinkOptions.of(ShareInfo(sourceType = 1, remainDownloads = 0), now)
        assertFalse(o.downloadsToChoose)
        assertTrue(o.complete)
        assertNull(o.downloads)
    }
}
