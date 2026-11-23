package io.github.svenbuild.fylvo.android.data.prefs

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * Fylvo: one accent colour of the site (`themes` in `site/config/basic`). [key] is the colour of
 * the dark mode and the value saved as `appearance.accent`; [light] is its variant for light mode.
 * Colours are ARGB.
 */
@Serializable
data class Accent(val key: String, val dark: Int, val light: Int)

object Accents {
    /** Today's Fylvo indigo; with it the app keeps its own fixed colour schemes. */
    const val DEFAULT_KEY = "#7484F4"

    private val json = Json { ignoreUnknownKeys = true }

    /** The accents in the site's order; unreadable entries are skipped. */
    fun parse(themes: String): List<Accent> {
        if (themes.isBlank()) return emptyList()
        val root = runCatching { json.parseToJsonElement(themes).jsonObject }.getOrNull() ?: return emptyList()
        return root.mapNotNull { (key, theme) ->
            val dark = parseColor(key) ?: return@mapNotNull null
            val light = runCatching { primaryOf(theme.jsonObject, "light") }.getOrNull() ?: dark
            Accent(key.uppercase(), dark, light)
        }
    }

    private fun primaryOf(theme: JsonObject, mode: String): Int? =
        theme[mode]?.jsonObject?.get("palette")?.jsonObject?.get("primary")?.jsonObject
            ?.get("main")?.jsonPrimitive?.content?.let(::parseColor)

    /** `#RRGGBB` to ARGB; null for anything else. */
    fun parseColor(text: String): Int? {
        val hex = text.trim().removePrefix("#")
        if (hex.length != 6 || !hex.all { it.isDigit() || it.lowercaseChar() in 'a'..'f' }) return null
        return (0xFF000000.toInt()) or hex.toInt(16)
    }

    fun isDefault(key: String): Boolean = key.equals(DEFAULT_KEY, ignoreCase = true)

    /** [a] mixed with [b]; [amount] 0 = only [a], 1 = only [b]. */
    fun mix(a: Int, b: Int, amount: Float): Int {
        fun ch(c: Int, shift: Int) = (c shr shift) and 0xFF
        fun m(shift: Int) = (ch(a, shift) + (ch(b, shift) - ch(a, shift)) * amount).toInt().coerceIn(0, 255)
        return (0xFF shl 24) or (m(16) shl 16) or (m(8) shl 8) or m(0)
    }

    private fun luminance(c: Int): Double {
        fun lin(v: Int): Double {
            val s = v / 255.0
            return if (s <= 0.03928) s / 12.92 else Math.pow((s + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * lin((c shr 16) and 0xFF) + 0.7152 * lin((c shr 8) and 0xFF) + 0.0722 * lin(c and 0xFF)
    }

    fun contrast(a: Int, b: Int): Double {
        val la = luminance(a)
        val lb = luminance(b)
        return (maxOf(la, lb) + 0.05) / (minOf(la, lb) + 0.05)
    }

    /** Black or white, whichever reads better on [background]. */
    fun onColor(background: Int): Int {
        val black = 0xFF0A0A0A.toInt()
        val white = 0xFFFFFFFF.toInt()
        return if (contrast(background, black) >= contrast(background, white)) black else white
    }
}
