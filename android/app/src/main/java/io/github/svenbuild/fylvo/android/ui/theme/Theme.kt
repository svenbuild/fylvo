package io.github.svenbuild.fylvo.android.ui.theme

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.material3.ColorScheme
import io.github.svenbuild.fylvo.android.data.prefs.Accent
import io.github.svenbuild.fylvo.android.data.prefs.Accents
import io.github.svenbuild.fylvo.android.data.prefs.ThemeMode

// Every overridden surface sets its matching `on*` colour. Overriding `surface` alone
// leaves Material's defaults for text, which were tuned for a different surface and
// render as low-contrast grey on our near-black; typed text in OutlinedTextField uses
// onSurface and labels use onSurfaceVariant, so both must be set together.
// Fylvo palette from design/style-guide.md: neutral near-black surfaces, grays carry the
// interface, the indigo accent #7484F4 marks the primary action, focus, progress and links.
// Selection fills (secondaryContainer) stay neutral gray, not accent.
internal val LightColors = lightColorScheme(
    primary = Color(0xFF5562D9),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFE3E6FD),
    onPrimaryContainer = Color(0xFF151C55),
    secondary = Color(0xFF5562D9),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFE6E6EA),
    onSecondaryContainer = Color(0xFF18181B),
    tertiary = Color(0xFF5562D9),
    tertiaryContainer = Color(0xFFE3E6FD),
    onTertiaryContainer = Color(0xFF151C55),
    background = Color(0xFFFAFAFA),
    onBackground = Color(0xFF18181B),
    surface = Color(0xFFFAFAFA),
    onSurface = Color(0xFF18181B),
    surfaceVariant = Color(0xFFEDEDF0),
    onSurfaceVariant = Color(0xFF5F5F68),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF4F4F5),
    surfaceContainer = Color(0xFFF0F0F2),
    surfaceContainerHigh = Color(0xFFEAEAED),
    surfaceContainerHighest = Color(0xFFE4E4E7),
    outline = Color(0xFFC8C8CE),
    outlineVariant = Color(0xFFE4E4E7),
    inverseSurface = Color(0xFF27272A),
    inverseOnSurface = Color(0xFFF4F4F5),
    inversePrimary = Color(0xFF7484F4),
)

internal val DarkColors = darkColorScheme(
    primary = Color(0xFF7484F4),
    onPrimary = Color(0xFF0A0A0A),
    primaryContainer = Color(0xFF2A3070),
    onPrimaryContainer = Color(0xFFDCE0FD),
    secondary = Color(0xFF7484F4),
    onSecondary = Color(0xFF0A0A0A),
    secondaryContainer = Color(0xFF1E1E22),
    onSecondaryContainer = Color(0xFFF4F4F5),
    tertiary = Color(0xFF7484F4),
    onTertiary = Color(0xFF0A0A0A),
    tertiaryContainer = Color(0xFF2A3070),
    onTertiaryContainer = Color(0xFFDCE0FD),
    background = Color(0xFF0A0A0A),
    onBackground = Color(0xFFF4F4F5),
    surface = Color(0xFF0A0A0A),
    onSurface = Color(0xFFF4F4F5),
    surfaceVariant = Color(0xFF1E1E22),
    onSurfaceVariant = Color(0xFFA9A9B0),
    surfaceContainerLowest = Color(0xFF000000),
    surfaceContainerLow = Color(0xFF101012),
    surfaceContainer = Color(0xFF141416),
    surfaceContainerHigh = Color(0xFF1A1A1D),
    surfaceContainerHighest = Color(0xFF1E1E22),
    outline = Color(0xFF3A3A40),
    outlineVariant = Color(0xFF242428),
    inverseSurface = Color(0xFFF4F4F5),
    inverseOnSurface = Color(0xFF18181B),
    inversePrimary = Color(0xFF5562D9),
)

/**
 * Fylvo: the scheme for an accent other than the default. The surfaces stay the Fylvo ones; the
 * accent colours and their text colours are derived (text on them by contrast).
 */
internal fun accentScheme(base: ColorScheme, accent: Accent, dark: Boolean): ColorScheme {
    val main = if (dark) accent.dark else accent.light
    val surface = base.surface.toArgb()
    val container = Accents.mix(surface, main, if (dark) 0.30f else 0.16f)
    val onContainer = if (dark) Accents.mix(main, 0xFFFFFFFF.toInt(), 0.75f) else Accents.mix(main, 0xFF000000.toInt(), 0.65f)
    val c = Color(main)
    val on = Color(Accents.onColor(main))
    return base.copy(
        primary = c, onPrimary = on,
        secondary = c, onSecondary = on,
        tertiary = c, onTertiary = on,
        primaryContainer = Color(container), onPrimaryContainer = Color(onContainer),
        tertiaryContainer = Color(container), onTertiaryContainer = Color(onContainer),
        inversePrimary = Color(if (dark) accent.light else accent.dark),
    )
}

/** Fylvo: the colour scheme for [dark] and [accent] (null or the default key: today's colours). */
fun fylvoColorScheme(dark: Boolean, accent: Accent?): ColorScheme {
    val base = if (dark) DarkColors else LightColors
    return if (accent == null || Accents.isDefault(accent.key)) base else accentScheme(base, accent, dark)
}

/**
 * Always the Fylvo palette, never Material You wallpaper colors. Fylvo: light or dark as the
 * account chose ([mode]), otherwise like the device; [accent] one of the site's six colours.
 */
@Composable
fun FylvoTheme(mode: ThemeMode = ThemeMode.SYSTEM, accent: Accent? = null, content: @Composable () -> Unit) {
    val dark = when (mode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    val scheme = fylvoColorScheme(dark, accent)
    // Edge-to-edge draws under the status bar, so its icons must follow our
    // surface, not the wallpaper: dark icons on the light theme and vice versa.
    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            (view.context as? Activity)?.window?.let { window ->
                val controller = WindowCompat.getInsetsController(window, view)
                controller.isAppearanceLightStatusBars = !dark
                controller.isAppearanceLightNavigationBars = !dark
            }
        }
    }
    MaterialTheme(colorScheme = scheme, typography = FylvoTypography, content = content)
}

/** Fylvo: the account's list density, for rows and tiles (compact = the look before customisation). */
val LocalListDensity = androidx.compose.runtime.staticCompositionLocalOf { io.github.svenbuild.fylvo.android.data.prefs.Density.COMPACT }

/** Fylvo: extra vertical space of a list row per density. */
val io.github.svenbuild.fylvo.android.data.prefs.Density.rowExtra: androidx.compose.ui.unit.Dp
    get() = when (this) {
        io.github.svenbuild.fylvo.android.data.prefs.Density.COMPACT -> androidx.compose.ui.unit.Dp(0f)
        io.github.svenbuild.fylvo.android.data.prefs.Density.NORMAL -> androidx.compose.ui.unit.Dp(4f)
        io.github.svenbuild.fylvo.android.data.prefs.Density.COMFORTABLE -> androidx.compose.ui.unit.Dp(8f)
    }
