package io.github.svenbuild.fylvo.android.api

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.serializer
import okhttp3.ResponseBody
import retrofit2.Converter
import retrofit2.Retrofit
import java.lang.reflect.ParameterizedType
import java.lang.reflect.Type

/**
 * Fylvo: reads `ApiResponse<T>` answers so that an error never depends on the shape of `data`.
 * The server puts different things into `data` of an error (a list of locks for 40073, a session
 * id for 203). For a code other than 0 the app needs only code and msg, so `data` is decoded only
 * when it fits and is dropped otherwise. Must be added before the general kotlinx converter.
 */
class EnvelopeConverterFactory(private val json: Json) : Converter.Factory() {
    override fun responseBodyConverter(
        type: Type,
        annotations: Array<out Annotation>,
        retrofit: Retrofit,
    ): Converter<ResponseBody, *>? {
        if (getRawType(type) != ApiResponse::class.java) return null
        val dataType = (type as? ParameterizedType)?.actualTypeArguments?.firstOrNull() ?: return null
        val dataSerializer = json.serializersModule.serializer(dataType)
        return Converter<ResponseBody, ApiResponse<*>> { body ->
            body.use { decodeApiResponse(json, dataSerializer, it.string()) }
        }
    }
}

/** Decodes one envelope; see [EnvelopeConverterFactory]. Throws [SerializationException] for non-JSON. */
fun <T> decodeApiResponse(json: Json, dataSerializer: KSerializer<T>, text: String): ApiResponse<T> {
    val obj = json.parseToJsonElement(text) as? JsonObject
        ?: throw SerializationException("Answer is not a JSON object")
    val code = (obj["code"] as? JsonPrimitive)?.intOrNull ?: 0
    val msg = (obj["msg"] as? JsonPrimitive)?.contentOrNull.orEmpty()
    val dataElement = obj["data"]?.takeUnless { it is JsonNull }
    val data = when {
        dataElement == null -> null
        code == 0 -> json.decodeFromJsonElement(dataSerializer, dataElement)
        else -> runCatching { json.decodeFromJsonElement(dataSerializer, dataElement) }.getOrNull()
    }
    return ApiResponse(
        code = code, msg = msg, data = data, innerCodes = innerCodes(obj), reason = reasonOf(obj),
        failedPaths = (obj["aggregated_error"] as? JsonObject)?.keys.orEmpty(),
    )
}

/** Codes of the single failures inside `aggregated_error` (batch answer 40081). */
private fun innerCodes(obj: JsonObject): List<Int> {
    val aggregated = obj["aggregated_error"] as? JsonObject ?: return emptyList()
    return aggregated.values.mapNotNull { ((it as? JsonObject)?.get("code") as? JsonPrimitive)?.intOrNull }
}

/**
 * Fylvo: `data.reason` of an error (connected storages, 40925), or of the first single failure
 * inside `aggregated_error` that has one (a one-file `POST /file/url` answers 40081 too).
 */
private fun reasonOf(obj: JsonObject): String? {
    fun of(o: JsonObject?): String? =
        ((o?.get("data") as? JsonObject)?.get("reason") as? JsonPrimitive)?.contentOrNull?.takeIf { it.isNotEmpty() }
    of(obj)?.let { return it }
    val aggregated = obj["aggregated_error"] as? JsonObject ?: return null
    return aggregated.values.firstNotNullOfOrNull { of(it as? JsonObject) }
}
