package io.github.svenbuild.fylvo.android.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import io.github.svenbuild.fylvo.android.R

/**
 * Fylvo: Inter, the font of the web app (bundled, SIL Open Font License, see
 * android/FONT-LICENSE-Inter.txt). Sizes follow design/style-guide.md, a step larger for a
 * phone: calm 14 sp body text, 12 sp meta text, semibold titles with slightly tighter
 * tracking, and none of Material's wide letter spacing.
 */
private val Inter = FontFamily(
    Font(R.font.inter_regular, FontWeight.Normal),
    Font(R.font.inter_medium, FontWeight.Medium),
    Font(R.font.inter_semibold, FontWeight.SemiBold),
)

private fun style(size: Int, line: Int, weight: FontWeight, tracking: Double = 0.0) = TextStyle(
    fontFamily = Inter,
    fontSize = size.sp,
    lineHeight = line.sp,
    fontWeight = weight,
    letterSpacing = tracking.em,
)

val FylvoTypography = Typography(
    displayLarge = style(40, 48, FontWeight.SemiBold, -0.02),
    displayMedium = style(34, 42, FontWeight.SemiBold, -0.02),
    displaySmall = style(30, 38, FontWeight.SemiBold, -0.02),
    headlineLarge = style(26, 34, FontWeight.SemiBold, -0.02),
    headlineMedium = style(24, 32, FontWeight.SemiBold, -0.02),
    headlineSmall = style(20, 28, FontWeight.SemiBold, -0.015),
    titleLarge = style(19, 26, FontWeight.SemiBold, -0.015),
    titleMedium = style(15, 22, FontWeight.Medium, -0.01),
    titleSmall = style(13, 18, FontWeight.SemiBold),
    bodyLarge = style(15, 22, FontWeight.Normal, -0.005),
    bodyMedium = style(14, 20, FontWeight.Normal, -0.005),
    bodySmall = style(12, 16, FontWeight.Normal),
    labelLarge = style(14, 20, FontWeight.Medium),
    labelMedium = style(12, 16, FontWeight.Medium),
    labelSmall = style(11, 14, FontWeight.Medium, 0.01),
)
