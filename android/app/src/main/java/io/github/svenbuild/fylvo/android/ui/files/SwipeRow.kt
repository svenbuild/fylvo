package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.OfflinePin
import androidx.compose.material.icons.outlined.StarOutline
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.prefs.StarState
import io.github.svenbuild.fylvo.android.data.prefs.SwipeAction

/**
 * Fylvo: what swiping one row does. [right] is the action of a swipe to the right (start to end),
 * [left] of one to the left; null: that direction does nothing for this row.
 */
data class RowSwipe(
    val right: SwipeAction?,
    val left: SwipeAction?,
    /** The star of the row, for the label and icon of [SwipeAction.FAVORITE]. */
    val star: StarState,
    val onSwipe: (SwipeAction) -> Unit,
)

/**
 * Fylvo: a row that can be swiped left or right. The action runs once when the row snaps past the
 * threshold; the row then springs back (nothing is removed from the list here).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SwipeRow(swipe: RowSwipe?, content: @Composable () -> Unit) {
    if (swipe == null || (swipe.right == null && swipe.left == null)) {
        content()
        return
    }
    val haptics = LocalHapticFeedback.current
    // The state is made once per row; the action must be the row's current one (a star set by
    // the last swipe turns "add" into "remove").
    val current by androidx.compose.runtime.rememberUpdatedState(swipe)
    val state = rememberSwipeToDismissBoxState(
        confirmValueChange = { value ->
            val now = current
            val action = when (value) {
                SwipeToDismissBoxValue.StartToEnd -> now.right
                SwipeToDismissBoxValue.EndToStart -> now.left
                SwipeToDismissBoxValue.Settled -> null
            }
            if (action != null) {
                haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                now.onSwipe(action)
            }
            // Never stay swiped away: the row comes back.
            false
        },
        positionalThreshold = { it * 0.4f },
    )
    SwipeToDismissBox(
        state = state,
        enableDismissFromStartToEnd = swipe.right != null,
        enableDismissFromEndToStart = swipe.left != null,
        backgroundContent = {
            // At rest nothing is behind the row (rows are see-through).
            if (state.dismissDirection == SwipeToDismissBoxValue.Settled) return@SwipeToDismissBox
            val toRight = state.dismissDirection == SwipeToDismissBoxValue.StartToEnd
            val action = (if (toRight) swipe.right else swipe.left) ?: return@SwipeToDismissBox
            val (icon, label, tint) = swipeLook(action, swipe.star)
            Row(
                Modifier
                    .fillMaxSize()
                    .padding(horizontal = 8.dp, vertical = 1.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .background(tint.copy(alpha = 0.18f))
                    .padding(horizontal = 22.dp),
                horizontalArrangement = if (toRight) Arrangement.Start else Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(22.dp))
                Text(label, color = tint, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 10.dp))
            }
        },
    ) { content() }
}

@Composable
private fun swipeLook(action: SwipeAction, star: StarState): Triple<ImageVector, String, Color> = when (action) {
    SwipeAction.FAVORITE -> if (star == StarState.CAN_REMOVE) {
        Triple(Icons.Outlined.StarOutline, stringResource(R.string.remove_from_favorites), StarColor)
    } else {
        Triple(Icons.Default.Star, stringResource(R.string.add_to_favorites), StarColor)
    }
    SwipeAction.SHARE -> Triple(Icons.Outlined.Link, stringResource(R.string.share_link), MaterialTheme.colorScheme.primary)
    SwipeAction.OFFLINE -> Triple(Icons.Outlined.OfflinePin, stringResource(R.string.available_offline), MaterialTheme.colorScheme.primary)
    SwipeAction.DELETE -> Triple(Icons.Outlined.Delete, stringResource(R.string.delete), MaterialTheme.colorScheme.error)
    SwipeAction.NONE -> Triple(Icons.Outlined.StarOutline, "", Color.Transparent)
}
