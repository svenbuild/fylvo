package io.github.svenbuild.fylvo.android.api

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.serializer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import retrofit2.Retrofit
import kotlin.reflect.javaType
import kotlin.reflect.typeOf

class ApiEnvelopeTest {
    private val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }

    private inline fun <reified T> decode(text: String): ApiResponse<T> =
        decodeApiResponse(json, serializer<T>(), text)

    // The answer from the bug report: a lock conflict whose data is a list of locks.
    @Test
    fun errorWithListDataKeepsCodeAndMessage() {
        val r = decode<ListFileResponse>(
            """{"code":40073,"data":[{"path":"cloudreve://my/photo.jpg","token":"t","owner":{},"type":0}],"msg":"Lock conflict"}"""
        )
        assertEquals(40073, r.code)
        assertEquals("Lock conflict", r.msg)
        assertNull(r.data)
    }

    @Test
    fun errorWithStringDataKeepsCodeAndMessage() {
        val r = decode<Unit>("""{"code":40004,"data":"some text","msg":"Object existed"}""")
        assertEquals(40004, r.code)
        assertEquals("Object existed", r.msg)
        assertNull(r.data)
    }

    @Test
    fun errorWithObjectDataOfAnotherShape() {
        val r = decode<FileUrlResponse>("""{"code":40016,"data":{"urls":"not a list"},"msg":"Path not exist"}""")
        assertEquals(40016, r.code)
        assertNull(r.data)
    }

    @Test
    fun errorWithoutDataOrMessage() {
        val r = decode<ListFileResponse>("""{"code":500}""")
        assertEquals(500, r.code)
        assertEquals("", r.msg)
        assertNull(r.data)
    }

    // Sign-in reads the 2FA session id from data of code 203; it must stay there.
    @Test
    fun errorDataThatFitsIsKept() {
        val r = decode<JsonElement>("""{"code":203,"data":"session-1","msg":""}""")
        assertEquals(JsonPrimitive("session-1"), r.data)
    }

    @Test
    fun batchErrorCollectsInnerCodes() {
        val r = decode<Unit>(
            """{"code":40081,"msg":"One or more operation failed","aggregated_error":{"a":{"code":40073,"msg":"x","data":[]},"b":{"code":40016,"msg":"y"}}}"""
        )
        assertEquals(40081, r.code)
        assertEquals(setOf(40073, 40016), r.innerCodes.toSet())
    }

    @Test
    fun successDecodesData() {
        val r = decode<ListFileResponse>(
            """{"code":0,"msg":"","data":{"files":[{"type":0,"id":"f1","name":"a.jpg","size":3400000,"path":"cloudreve://my/a.jpg"}],"pagination":{"next_token":"n"}}}"""
        )
        assertEquals(0, r.code)
        assertEquals("a.jpg", r.data!!.files.single().name)
        assertEquals("n", r.data!!.pagination.nextToken)
    }

    @Test
    fun successWithoutDataForUnit() {
        val r = decode<Unit>("""{"code":0,"msg":""}""")
        assertEquals(0, r.code)
        assertNull(r.data)
    }

    @Test(expected = kotlinx.serialization.SerializationException::class)
    fun nonJsonAnswerThrowsSerializationException() {
        decode<Unit>("<html>Bad gateway</html>")
    }

    @OptIn(ExperimentalStdlibApi::class)
    @Test
    fun retrofitConverterUsesTheEnvelopeReader() {
        val retrofit = Retrofit.Builder().baseUrl("http://localhost/").build()
        val converter = EnvelopeConverterFactory(json)
            .responseBodyConverter(typeOf<ApiResponse<ListFileResponse>>().javaType, emptyArray(), retrofit)!!
        val body = """{"code":40073,"data":[{"path":"cloudreve://my/photo.jpg"}],"msg":"Lock conflict"}"""
            .toResponseBody("application/json".toMediaType())
        val r = converter.convert(body) as ApiResponse<*>
        assertEquals(40073, r.code)
        assertNull(r.data)
    }

    @OptIn(ExperimentalStdlibApi::class)
    @Test
    fun otherTypesAreLeftToTheNextConverter() {
        val retrofit = Retrofit.Builder().baseUrl("http://localhost/").build()
        assertNull(EnvelopeConverterFactory(json).responseBodyConverter(typeOf<FileObject>().javaType, emptyArray(), retrofit))
    }

    @Test
    fun unfinishedUploadIsMarked() {
        val uploading = json.decodeFromString<FileObject>(
            """{"type":0,"name":"photo.jpg","size":0,"metadata":{"sys:upload_session_id":"abc"}}"""
        )
        assertTrue(uploading.isUploading)
        assertFalse(uploading.mayHaveThumbnail)
        val done = json.decodeFromString<FileObject>("""{"type":0,"name":"a.jpg","size":5,"metadata":{"thumb:disabled":""}}""")
        assertFalse(done.isUploading)
        val plain = json.decodeFromString<FileObject>("""{"type":0,"name":"b.jpg","size":5}""")
        assertFalse(plain.isUploading)
        val folder = json.decodeFromString<FileObject>("""{"type":1,"name":"d","metadata":{"sys:upload_session_id":"abc"}}""")
        assertFalse(folder.isUploading)
    }
}
