package io.github.svenbuild.fylvo.android.data

import android.content.pm.Signature
import kotlinx.serialization.json.Json
import io.github.svenbuild.fylvo.android.api.AndroidRelease
import io.github.svenbuild.fylvo.android.api.ApiResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.security.MessageDigest

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35]) // Robolectric 4.14 tops out below targetSdk 36
class AppUpdaterTest {

    // Same decoder settings as ApiClient.json.
    private val json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
    }

    private fun parse(text: String): AndroidRelease? =
        AppUpdater.releaseFrom(json.decodeFromString<ApiResponse<AndroidRelease>>(text))

    private val sha = "a".repeat(64)

    @Test
    fun `latest envelope parses the snake_case fields`() {
        val release = parse(
            """{"code":0,"msg":"","data":{"version_code":3,"version_name":"1.1.0","sha256":"$sha","size":6234643,"extra":1}}"""
        )
        assertEquals(AndroidRelease(versionCode = 3, versionName = "1.1.0", sha256 = sha, size = 6234643), release)
    }

    @Test
    fun `code 404 means no release, not an error`() {
        assertNull(parse("""{"code":404,"msg":"No release published"}"""))
    }

    @Test(expected = ApiException::class)
    fun `other error codes throw`() {
        parse("""{"code":401,"msg":"Login required"}""")
    }

    @Test
    fun `only a higher, complete release is an update`() {
        val release = AndroidRelease(versionCode = 3, versionName = "1.1.0", sha256 = sha, size = 10)
        assertTrue(AppUpdater.isUpdate(release, installedCode = 2L))
        assertFalse(AppUpdater.isUpdate(release, installedCode = 3L))
        assertFalse(AppUpdater.isUpdate(release, installedCode = 4L))
        assertFalse(AppUpdater.isUpdate(release.copy(sha256 = "xyz"), installedCode = 2L))
        assertFalse(AppUpdater.isUpdate(release.copy(size = 0), installedCode = 2L))
    }

    @Test
    fun `file verification checks size and sha256`() {
        val bytes = "fylvo apk bytes".toByteArray()
        val hex = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        val file = File.createTempFile("fylvo-update", ".apk")
        try {
            file.writeBytes(bytes)
            assertTrue(AppUpdater.verifyFile(file, bytes.size.toLong(), hex))
            assertTrue(AppUpdater.verifyFile(file, bytes.size.toLong(), hex.uppercase()))
            assertFalse(AppUpdater.verifyFile(file, bytes.size + 1L, hex))
            assertFalse(AppUpdater.verifyFile(file, bytes.size.toLong(), "0".repeat(64)))
            file.delete()
            assertFalse(AppUpdater.verifyFile(file, bytes.size.toLong(), hex))
        } finally {
            file.delete()
        }
    }

    @Test
    fun `update file names carry the version code`() {
        assertEquals("fylvo-3.apk", AppUpdater.fileName(3))
        assertEquals(3L, AppUpdater.versionCodeOf("fylvo-3.apk"))
        assertNull(AppUpdater.versionCodeOf("fylvo-3.apk.part"))
        assertNull(AppUpdater.versionCodeOf("other.apk"))
    }

    @Test
    fun `signers must match by certificate bytes`() {
        val a = Signature(byteArrayOf(1, 2, 3))
        val b = Signature(byteArrayOf(4, 5, 6))
        assertTrue(AppUpdater.sameSigners(arrayOf(a), arrayOf(Signature(byteArrayOf(1, 2, 3)))))
        assertFalse(AppUpdater.sameSigners(arrayOf(a), arrayOf(b)))
        assertFalse(AppUpdater.sameSigners(arrayOf(a, b), arrayOf(a)))
        assertFalse(AppUpdater.sameSigners(emptyArray(), emptyArray()))
    }
}
