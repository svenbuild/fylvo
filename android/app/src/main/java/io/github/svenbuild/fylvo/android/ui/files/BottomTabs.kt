package io.github.svenbuild.fylvo.android.ui.files

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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.FolderShared
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderShared
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.OfflinePin
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

/** Fylvo: the four places of the app, reachable from the floating bar at the bottom. */
enum class Tab(val label: String, val icon: ImageVector, val selectedIcon: ImageVector) {
    HOME("Home", Icons.Outlined.Home, Icons.Filled.Home),
    FILES("Files", Icons.Outlined.Folder, Icons.Filled.Folder),
    SHARED("Shared", Icons.Outlined.FolderShared, Icons.Filled.FolderShared),
    OFFLINE("Offline", Icons.Outlined.OfflinePin, Icons.Filled.OfflinePin),
}

private val BarShape = RoundedCornerShape(30.dp)
private val PillInset = 5.dp

/**
 * Floating bottom bar: a rounded pill that hovers above the bottom edge with a hairline
 * border. The active tab sits on a lighter pill that fills the bar's height and glides to
 * the tapped tab with a soft spring; icon and label fade to the accent as it arrives.
 */
@Composable
fun BottomTabs(selected: Tab, onSelect: (Tab) -> Unit) {
    val colors = MaterialTheme.colorScheme
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
                val tabWidth = maxWidth / Tab.entries.size
                val pillOffset by animateDpAsState(
                    targetValue = tabWidth * selected.ordinal,
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
                    Tab.entries.forEach { tab ->
                        TabItem(tab, tab == selected, Modifier.weight(1f).fillMaxHeight()) { onSelect(tab) }
                    }
                }
            }
        }
    }
}

@Composable
private fun TabItem(tab: Tab, active: Boolean, modifier: Modifier, onClick: () -> Unit) {
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
        Icon(
            if (active) tab.selectedIcon else tab.icon,
            contentDescription = null,
            tint = iconTint,
            modifier = Modifier.size(22.dp),
        )
        Text(tab.label, style = MaterialTheme.typography.labelSmall, color = labelColor)
    }
}
