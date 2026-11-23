package io.github.svenbuild.fylvo.android.ui.files

import androidx.annotation.StringRes
import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.FolderShared
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderShared
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.AccountCircle
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.LocalContext
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.data.prefs.Place
import io.github.svenbuild.fylvo.android.data.AppUpdater
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

/**
 * Fylvo: the bottom tabs. Which places and in which order comes from the account (`mobile.tabs`,
 * see MobilePrefs); without customisation Home, Files, Shared and Account.
 */
private val BarShape = RoundedCornerShape(30.dp)
private val PillInset = 5.dp

/**
 * Floating bottom bar: a rounded pill that hovers above the bottom edge with a hairline
 * border. The active tab sits on a lighter pill that fills the bar's height and glides to
 * the tapped tab with a soft spring; icon and label fade to the accent as it arrives.
 */
@Composable
fun BottomTabs(tabs: List<Place>, selected: Place, onSelect: (Place) -> Unit) {
    val colors = MaterialTheme.colorScheme
    // Fylvo: a small dot on Account while an app update waits.
    val updater = (LocalContext.current.applicationContext as FylvoApp).appUpdater
    val updateState by updater.state.collectAsState()
    val updateWaiting = updateState is AppUpdater.State.Available || updateState is AppUpdater.State.Downloading
    Box(
        Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(start = 20.dp, end = 20.dp, bottom = 12.dp, top = 4.dp),
    ) {
        Surface(
            shape = BarShape,
            color = colors.surfaceContainer,
            border = BorderStroke(1.dp, colors.outlineVariant),
            shadowElevation = 8.dp,
            modifier = Modifier
                .fillMaxWidth()
                .height(64.dp),
        ) {
            BoxWithConstraints(Modifier.fillMaxSize().padding(PillInset)) {
                val tabWidth = maxWidth / tabs.size.coerceAtLeast(1)
                val pillOffset by animateDpAsState(
                    targetValue = tabWidth * tabs.indexOf(selected).coerceAtLeast(0),
                    animationSpec = spring(dampingRatio = 0.78f, stiffness = Spring.StiffnessMediumLow),
                    label = "tabPill",
                )
                Box(
                    Modifier
                        .offset(x = pillOffset)
                        .width(tabWidth)
                        .fillMaxHeight()
                        .background(colors.secondaryContainer, RoundedCornerShape(25.dp)),
                )
                Row(Modifier.fillMaxSize()) {
                    tabs.forEach { tab ->
                        TabItem(
                            tab, tab == selected, Modifier.weight(1f).fillMaxHeight(),
                            dot = tab == Place.Account && updateWaiting,
                        ) { onSelect(tab) }
                    }
                }
            }
        }
    }
}

@Composable
private fun TabItem(tab: Place, active: Boolean, modifier: Modifier, dot: Boolean = false, onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val iconTint by animateColorAsState(if (active) colors.primary else colors.onSurfaceVariant, label = "tabIcon")
    val labelColor by animateColorAsState(if (active) colors.onSurface else colors.onSurfaceVariant, label = "tabLabel")
    Column(
        modifier
            // The gliding pill is the feedback; no ripple on top of it.
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                role = Role.Tab,
                onClick = onClick,
            )
            .semantics { this.selected = active },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(2.dp, Alignment.CenterVertically),
    ) {
        Box {
            val (icon, selectedIcon) = placeIcons(tab)
            Icon(
                if (active) selectedIcon else icon,
                contentDescription = null,
                tint = iconTint,
                modifier = Modifier.size(22.dp),
            )
            if (dot) {
                Box(
                    Modifier
                        .align(Alignment.TopEnd)
                        .offset(x = 3.dp, y = (-2).dp)
                        .size(8.dp)
                        .background(colors.primary, CircleShape),
                )
            }
        }
        Text(
            placeLabel(tab), style = MaterialTheme.typography.labelSmall, color = labelColor,
            maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
            modifier = Modifier.padding(horizontal = 2.dp),
        )
    }
}
