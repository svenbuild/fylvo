package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.prefs.Accents
import io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs

/** Fylvo: ids of the account's favourites (a star after the name); empty on an older server. */
val LocalFavoriteIds = compositionLocalOf { emptySet<String>() }

/** Fylvo: the star colour of the web app. */
val StarColor = Color(0xFFF5A524)

/** Fylvo: `#RRGGBB` as a colour, null when unreadable. */
fun colorOf(hex: String?): Color? = hex?.let(Accents::parseColor)?.let { Color(it) }

/** Fylvo: the small star after a favourite's name. */
@Composable
fun FavoriteStar(file: FileObject, size: Dp = 14.dp) {
    if (file.id.isNotEmpty() && file.id in LocalFavoriteIds.current) {
        Icon(
            Icons.Default.Star,
            contentDescription = stringResource(R.string.favorite),
            tint = StarColor,
            modifier = Modifier.padding(start = 6.dp).size(size),
        )
    }
}

/** Fylvo: up to two tags as dot and name, then "+n", after the rest of the second line. */
@Composable
fun TagMarks(file: FileObject, style: TextStyle) {
    val tags = MobilePrefs.tagsOf(file)
    if (tags.isEmpty()) return
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(" · ", style = style, color = MaterialTheme.colorScheme.onSurfaceVariant)
        tags.take(2).forEach { (name, color) ->
            Box(
                Modifier
                    .padding(end = 4.dp)
                    .size(7.dp)
                    .background(colorOf(color) ?: MaterialTheme.colorScheme.onSurfaceVariant, CircleShape),
            )
            Text(name, style = style, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, modifier = Modifier.padding(end = 6.dp))
        }
        if (tags.size > 2) {
            Text("+${tags.size - 2}", style = style, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
