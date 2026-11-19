package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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

/**
 * Floating bottom bar: a rounded pill that hovers above the bottom edge with a hairline
 * border. The active tab gets a neutral pill behind its icon and the accent color.
 */
@Composable
fun BottomTabs(selected: Tab, onSelect: (Tab) -> Unit) {
    Box(
        Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(start = 20.dp, end = 20.dp, bottom = 12.dp, top = 4.dp),
    ) {
        Surface(
            shape = RoundedCornerShape(28.dp),
            color = MaterialTheme.colorScheme.surfaceContainer,
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            shadowElevation = 6.dp,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Row(
                Modifier
                    .height(64.dp)
                    .padding(horizontal = 6.dp),
                horizontalArrangement = Arrangement.SpaceEvenly,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Tab.entries.forEach { tab ->
                    TabItem(tab, tab == selected, Modifier.weight(1f)) { onSelect(tab) }
                }
            }
        }
    }
}

@Composable
private fun TabItem(tab: Tab, active: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    Column(
        modifier
            .clickable(role = Role.Tab, onClick = onClick)
            .semantics { this.selected = active }
            .padding(vertical = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Box(
            Modifier
                .size(width = 52.dp, height = 28.dp)
                .background(
                    if (active) colors.secondaryContainer else colors.surfaceContainer,
                    RoundedCornerShape(14.dp),
                ),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                if (active) tab.selectedIcon else tab.icon,
                contentDescription = null,
                tint = if (active) colors.primary else colors.onSurfaceVariant,
                modifier = Modifier.size(20.dp),
            )
        }
        Text(
            tab.label,
            style = MaterialTheme.typography.labelSmall,
            color = if (active) colors.onSurface else colors.onSurfaceVariant,
        )
    }
}
