package io.github.svenbuild.fylvo.android.util

import android.content.Context
import android.content.res.Configuration
import android.os.LocaleList
import androidx.appcompat.app.AppCompatDelegate
import androidx.core.os.LocaleListCompat

/**
 * Fylvo: the app language, set on the Account screen. Uses the AndroidX per-app language API,
 * which stores the choice itself (AppLocalesMetadataHolderService in the manifest) and on
 * Android 13+ also shows it in the system settings. No choice means the device language;
 * a device language without Fylvo texts falls back to English (values/).
 */
enum class AppLanguage(val tag: String) {
    DEVICE(""),
    ENGLISH("en"),
    GERMAN("de");

    fun apply() = AppCompatDelegate.setApplicationLocales(LocaleListCompat.forLanguageTags(tag))

    companion object {
        fun current(): AppLanguage {
            val locales = AppCompatDelegate.getApplicationLocales()
            if (locales.isEmpty) return DEVICE
            return when (locales[0]?.language) {
                "de" -> GERMAN
                "en" -> ENGLISH
                else -> DEVICE
            }
        }
    }
}

/**
 * Context in the app language for texts built outside an activity (notifications, the
 * documents provider). Activities get the language from AppCompat on their own.
 */
fun Context.localized(): Context {
    val locales = AppCompatDelegate.getApplicationLocales()
    if (locales.isEmpty) return this
    val config = Configuration(resources.configuration)
    config.setLocales(LocaleList.forLanguageTags(locales.toLanguageTags()))
    return createConfigurationContext(config)
}
