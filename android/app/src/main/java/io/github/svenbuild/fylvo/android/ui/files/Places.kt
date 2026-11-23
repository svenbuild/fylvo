package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Label
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Cloud
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.FolderShared
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.outlined.AccountCircle
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderShared
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.MusicNote
import androidx.compose.material.icons.outlined.OfflinePin
import androidx.compose.material.icons.outlined.StarOutline
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.filled.Link
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.prefs.Place
import io.github.svenbuild.fylvo.android.util.CrUri

/** Fylvo: the name of a place (tab, chip). */
@Composable
fun placeLabel(place: Place): String = when (place) {
    Place.Home -> stringResource(R.string.tab_home)
    Place.Files -> stringResource(R.string.tab_files)
    Place.Shared -> stringResource(R.string.tab_shared)
    Place.Favorites -> stringResource(R.string.favorites)
    Place.Tags -> stringResource(R.string.tags)
    Place.Recent -> stringResource(R.string.category_recent)
    Place.Offline -> stringResource(R.string.tab_offline)
    Place.Trash -> stringResource(R.string.trash)
    Place.Account -> stringResource(R.string.tab_account)
    is Place.Category -> stringResource(categoryOf(place).label)
    is Place.Link -> place.name.ifEmpty { CrUri.displayName(place.uri) }
    is Place.Storage -> CrUri.rootLabel(storageUri(place))
}

/** Fylvo: outline and filled icon of a place (filled when it is the active tab). */
fun placeIcons(place: Place): Pair<ImageVector, ImageVector> = when (place) {
    Place.Home -> Icons.Outlined.Home to Icons.Filled.Home
    Place.Files -> Icons.Outlined.Folder to Icons.Filled.Folder
    Place.Shared -> Icons.Outlined.FolderShared to Icons.Filled.FolderShared
    Place.Favorites -> Icons.Outlined.StarOutline to Icons.Filled.Star
    Place.Tags -> Icons.AutoMirrored.Outlined.Label to Icons.AutoMirrored.Outlined.Label
    Place.Recent -> Icons.Outlined.History to Icons.Filled.History
    Place.Offline -> Icons.Outlined.OfflinePin to Icons.Filled.OfflinePin
    Place.Trash -> Icons.Outlined.Delete to Icons.Filled.Delete
    Place.Account -> Icons.Outlined.AccountCircle to Icons.Filled.AccountCircle
    is Place.Category -> when (place.category) {
        "image" -> Icons.Outlined.Image to Icons.Filled.Image
        "video" -> Icons.Outlined.Videocam to Icons.Filled.Videocam
        "audio" -> Icons.Outlined.MusicNote to Icons.Filled.MusicNote
        else -> Icons.Outlined.Description to Icons.Filled.Description
    }
    is Place.Link -> if (CrUri.isSearch(place.uri)) Icons.Outlined.Link to Icons.Filled.Link else Icons.Outlined.Folder to Icons.Filled.Folder
    is Place.Storage -> Icons.Outlined.Cloud to Icons.Filled.Cloud
}

fun categoryOf(place: Place.Category): Category = when (place.category) {
    "image" -> Category.IMAGES
    "video" -> Category.VIDEOS
    "audio" -> Category.AUDIO
    else -> Category.DOCUMENTS
}

fun storageUri(place: Place.Storage): String = "cloudreve://${place.id}@connected"

/** Fylvo: the places of the sidebar as a row of chips above "My files" (scrolls sideways). */
@Composable
fun PlaceChips(places: List<Place>, onOpen: (Place) -> Unit) {
    if (places.isEmpty()) return
    Row(
        Modifier
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        places.forEach { place ->
            Surface(
                onClick = { onOpen(place) },
                shape = RoundedCornerShape(16.dp),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
                color = Color.Transparent,
            ) {
                Row(Modifier.padding(horizontal = 12.dp, vertical = 7.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(
                        placeIcons(place).first, contentDescription = null,
                        tint = if (place == Place.Favorites) StarColor else MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(16.dp),
                    )
                    Text(placeLabel(place), style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 7.dp))
                }
            }
        }
    }
}
