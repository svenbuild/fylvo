package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.graphics.vector.group
import androidx.compose.ui.unit.dp

/**
 * Fylvo 1.6.2: the logos of the storage providers, the same drawings as the web app
 * (frontend/src/component/Icons/GoogleDrive.tsx and OneDrive.tsx), in their own colours and
 * proportions. A logo stands for a storage at that provider and for nothing else; it is never
 * tinted or redrawn. Other providers keep the cloud.
 */
object ProviderLogos {
    /** The logo of [provider] ("drive", "onedrive"), or null when there is none. */
    fun of(provider: String?): ImageVector? = when (provider) {
        "drive" -> googleDrive
        "onedrive" -> oneDrive
        else -> null
    }

    private fun ImageVector.Builder.fill(color: Long, d: String) {
        addPath(pathData = addPathNodes(d), fill = SolidColor(Color(color)))
    }

    // Both drawings sit centred in a square, so a logo takes the room of the cloud (24 dp).
    private val googleDrive: ImageVector by lazy {
        ImageVector.Builder("GoogleDrive", 24.dp, 24.dp, 87.3f, 87.3f).apply {
            group(translationY = (87.3f - 78f) / 2) {
            fill(0xFF0066DA, "m6.6 66.85 3.85 6.65c.8 1.4 1.95 2.5 3.3 3.3l13.75-23.8h-27.5c0 1.55.4 3.1 1.2 4.5z")
            fill(0xFF00AC47, "m43.65 25-13.75-23.8c-1.35.8-2.5 1.9-3.3 3.3l-25.4 44a9.06 9.06 0 0 0 -1.2 4.5h27.5z")
            fill(0xFFEA4335, "m73.55 76.8c1.35-.8 2.5-1.9 3.3-3.3l1.6-2.75 7.65-13.25c.8-1.4 1.2-2.95 1.2-4.5h-27.502l5.852 11.5z")
            fill(0xFF00832D, "m43.65 25 13.75-23.8c-1.35-.8-2.9-1.2-4.5-1.2h-18.5c-1.6 0-3.15.45-4.5 1.2z")
            fill(0xFF2684FC, "m59.8 53h-32.3l-13.75 23.8c1.35.8 2.9 1.2 4.5 1.2h50.8c1.6 0 3.15-.45 4.5-1.2z")
            fill(0xFFFFBA00, "m73.4 26.5-12.7-22c-.8-1.4-1.95-2.5-3.3-3.3l-13.75 23.8 16.15 28h27.45c0-1.55-.4-3.1-1.2-4.5z")
            }
        }.build()
    }

    private val oneDrive: ImageVector by lazy {
        // The web's viewBox is 0 5.5 32 20.5: moved up by 5.5, then centred in 32 x 32.
        ImageVector.Builder("OneDrive", 24.dp, 24.dp, 32f, 32f).apply {
            group(translationY = -5.5f + (32f - 20.5f) / 2) {
                fill(0xFF0364B8, "M12.20245,11.19292l.00031-.0011,6.71765,4.02379,4.00293-1.68451.00018.00068A6.4768,6.4768,0,0,1,25.5,13c.14764,0,.29358.0067.43878.01639a10.00075,10.00075,0,0,0-18.041-3.01381C7.932,10.00215,7.9657,10,8,10A7.96073,7.96073,0,0,1,12.20245,11.19292Z")
                fill(0xFF0078D4, "M12.20276,11.19182l-.00031.0011A7.96073,7.96073,0,0,0,8,10c-.0343,0-.06805.00215-.10223.00258A7.99676,7.99676,0,0,0,1.43732,22.57277l5.924-2.49292,2.63342-1.10819,5.86353-2.46746,3.06213-1.28859Z")
                fill(0xFF1490DF, "M25.93878,13.01639C25.79358,13.0067,25.64764,13,25.5,13a6.4768,6.4768,0,0,0-2.57648.53178l-.00018-.00068-4.00293,1.68451,1.16077.69528L23.88611,18.19l1.66009.99438,5.67633,3.40007a6.5002,6.5002,0,0,0-5.28375-9.56805Z")
                fill(0xFF28A8EA, "M25.5462,19.18437,23.88611,18.19l-3.80493-2.2791-1.16077-.69528L15.85828,16.5042,9.99475,18.97166,7.36133,20.07985l-5.924,2.49292A7.98889,7.98889,0,0,0,8,26H25.5a6.49837,6.49837,0,0,0,5.72253-3.41556Z")
            }
        }.build()
    }
}

/**
 * Fylvo 1.6.2: the picture of a connected storage: its provider's logo (untinted) or, without
 * one, the cloud [fallback] in [tint].
 */
@Composable
fun StorageIcon(provider: String?, fallback: ImageVector, modifier: Modifier = Modifier, tint: Color = LocalContentColor.current) {
    val logo = ProviderLogos.of(provider)
    if (logo != null) Icon(logo, contentDescription = null, modifier = modifier, tint = Color.Unspecified)
    else Icon(fallback, contentDescription = null, modifier = modifier, tint = tint)
}
