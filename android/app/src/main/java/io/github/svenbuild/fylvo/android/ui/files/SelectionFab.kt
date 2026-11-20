package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterExitState
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.DriveFileMove
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.DeleteForever
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.filled.FileCopy
import androidx.compose.material.icons.filled.FolderOpen
import androidx.compose.material.icons.filled.MoreHoriz
import androidx.compose.material.icons.filled.OfflinePin
import androidx.compose.material.icons.filled.RestoreFromTrash
import androidx.compose.material.icons.filled.SelectAll
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.OfflinePin
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import kotlin.math.roundToInt

/** Gap between the quick buttons, and between them and the selection button. */
private val QuickGap = 10.dp
private val QuickShape = RoundedCornerShape(14.dp)
/** The mockup's "pop" curve: a little past the target, then back. */
private val Overshoot = CubicBezierEasing(0.2f, 1.4f, 0.4f, 1f)

/** Fylvo: the state of "Available offline" for the one selected item, as in the file sheet. */
data class OfflineSwitch(val on: Boolean, val covered: Boolean, val folder: Boolean)

/**
 * Fylvo: the button at the bottom right. Normally "+" (upload, new folder); while selecting it
 * turns into the selection button ("⋯" with the count) and the most important actions slide out
 * to its left. [onAnchor] reports where the button is, so the menu card can grow out of it.
 */
@Composable
internal fun FilesFab(
    visible: Boolean,
    count: Int,
    quick: List<SelectionAction>,
    menuOpen: Boolean,
    enabled: (SelectionAction) -> Boolean,
    onQuick: (SelectionAction) -> Unit,
    onClick: () -> Unit,
    onAnchor: (Rect) -> Unit,
) {
    val selecting = count > 0
    // Kept while the buttons slide back in after the selection ended.
    var shownQuick by remember { mutableStateOf(quick) }
    if (selecting) shownQuick = quick
    val gap = with(LocalDensity.current) { QuickGap.roundToPx() }
    Row(horizontalArrangement = Arrangement.spacedBy(QuickGap), verticalAlignment = Alignment.CenterVertically) {
        // The first action sits next to the button; each one further out comes a little later.
        shownQuick.asReversed().forEachIndexed { i, action ->
            val fromFab = shownQuick.size - 1 - i
            key(action) {
                val state = remember { MutableTransitionState(false) }
                state.targetState = selecting && !menuOpen
                val inDelay = fromFab * 50
                val outDelay = (shownQuick.size - 1 - fromFab) * 40
                val distance = { width: Int -> (fromFab + 1) * (width + gap) }
                AnimatedVisibility(
                    visibleState = state,
                    enter = slideInHorizontally(tween(340, inDelay, Overshoot), distance) +
                        scaleIn(tween(340, inDelay, Overshoot), initialScale = 0.3f) +
                        fadeIn(tween(160, inDelay)),
                    exit = slideOutHorizontally(tween(200, outDelay), distance) +
                        scaleOut(tween(200, outDelay), targetScale = 0.3f) +
                        fadeOut(tween(160, outDelay)),
                ) {
                    QuickButton(action, enabled(action)) { onQuick(action) }
                }
            }
        }
        AnimatedVisibility(visible = visible, enter = scaleIn() + fadeIn(), exit = scaleOut() + fadeOut()) {
            Box(
                Modifier
                    .onGloballyPositioned { onAnchor(it.boundsInWindow()) }
                    // While the card is open its own X stands here; this one keeps its place.
                    .alpha(if (menuOpen) 0f else 1f),
            ) {
                val description = if (selecting) {
                    pluralStringResource(R.plurals.actions_for_selected, count, count)
                } else {
                    stringResource(R.string.add)
                }
                // Brand primary, not M3's default tertiaryContainer purple:
                // matches the web app's primary action colour.
                FloatingActionButton(
                    onClick = onClick,
                    containerColor = MaterialTheme.colorScheme.primary,
                    contentColor = MaterialTheme.colorScheme.onPrimary,
                    modifier = Modifier.semantics { contentDescription = description },
                ) {
                    AnimatedContent(
                        targetState = selecting,
                        transitionSpec = {
                            (fadeIn(tween(180)) + scaleIn(tween(260, easing = Overshoot), initialScale = 0.6f)) togetherWith
                                (fadeOut(tween(120)) + scaleOut(tween(120), targetScale = 0.6f))
                        },
                        label = "fabIcon",
                    ) { more ->
                        val turn by transition.animateFloat(label = "fabIconTurn") { if (it == EnterExitState.Visible) 0f else -90f }
                        Icon(
                            if (more) Icons.Default.MoreHoriz else Icons.Default.Add,
                            contentDescription = null,
                            modifier = Modifier.graphicsLayer { rotationZ = turn },
                        )
                    }
                }
                if (selecting) CountBadge(count, Modifier.align(Alignment.TopStart).offset(x = (-6).dp, y = (-6).dp))
            }
        }
    }
}

/** The number of selected items on the selection button; read with the button, not alone. */
@Composable
private fun CountBadge(count: Int, modifier: Modifier) {
    Box(
        modifier
            .heightIn(min = 22.dp)
            .clip(RoundedCornerShape(11.dp))
            .background(MaterialTheme.colorScheme.inverseSurface)
            .padding(horizontal = 6.dp)
            .clearAndSetSemantics {},
        contentAlignment = Alignment.Center,
    ) {
        Text(
            count.toString(),
            color = MaterialTheme.colorScheme.inverseOnSurface,
            style = MaterialTheme.typography.labelMedium,
        )
    }
}

/** A small rounded square left of the selection button. */
@Composable
private fun QuickButton(action: SelectionAction, enabled: Boolean, onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val danger = action.danger
    val base = colors.surfaceContainerHighest
    Box(
        Modifier
            .size(48.dp)
            .clip(QuickShape)
            .background(if (danger) lerp(base, colors.error, 0.15f) else base)
            .border(1.dp, if (danger) lerp(colors.outline, colors.error, 0.3f) else colors.outline, QuickShape)
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            action.icon,
            contentDescription = action.label(quick = true),
            tint = (if (danger) colors.error else colors.onSurface).copy(alpha = if (enabled) 1f else 0.38f),
            modifier = Modifier.size(20.dp),
        )
    }
}

/**
 * Fylvo: the rest of the selection's actions as a card that grows out of the selection button
 * ([anchor], in window coordinates), over a scrim. X, the scrim and back close it; the selection
 * stays. Behind it nothing is reachable, also not for TalkBack.
 */
@Composable
internal fun SelectionMenu(
    open: Boolean,
    anchor: Rect?,
    items: List<SelectionAction>,
    offline: OfflineSwitch?,
    enabled: (SelectionAction) -> Boolean,
    onAction: (SelectionAction) -> Unit,
    onClose: () -> Unit,
) {
    val shown = remember { MutableTransitionState(false) }
    shown.targetState = open
    if (!shown.currentState && !shown.targetState) return
    // Kept while the card closes, so it does not change its rows on the way out.
    var shownItems by remember { mutableStateOf(items) }
    if (open) shownItems = items
    var box by remember { mutableStateOf<Rect?>(null) }
    val closeLabel = stringResource(R.string.close)
    Box(
        Modifier
            .fillMaxSize()
            .onGloballyPositioned { box = it.boundsInWindow() },
    ) {
        AnimatedVisibility(visibleState = shown, enter = fadeIn(tween(180)), exit = fadeOut(tween(160))) {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(Color.Black.copy(alpha = 0.5f))
                    .pointerInput(Unit) { detectTapGestures { onClose() } }
                    .clearAndSetSemantics {
                        contentDescription = closeLabel
                        onClick(label = closeLabel) { onClose(); true }
                    },
            )
        }
        val outer = box ?: return@Box
        val fab = anchor?.translate(-outer.left, -outer.top) ?: return@Box
        val density = LocalDensity.current
        val end = with(density) { (outer.width - fab.right).coerceAtLeast(0f).toDp() }
        val bottom = with(density) { (outer.height - fab.top).coerceAtLeast(0f).toDp() } + 12.dp
        val origin = TransformOrigin(1f, 1f)
        AnimatedVisibility(
            visibleState = shown,
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .statusBarsPadding()
                .padding(start = 24.dp, top = 16.dp, end = end, bottom = bottom),
            enter = scaleIn(spring(dampingRatio = 0.72f, stiffness = Spring.StiffnessMediumLow), initialScale = 0.25f, transformOrigin = origin) +
                fadeIn(tween(160)),
            exit = scaleOut(tween(160), targetScale = 0.25f, transformOrigin = origin) + fadeOut(tween(140)),
        ) {
            Surface(
                shape = RoundedCornerShape(20.dp),
                color = MaterialTheme.colorScheme.surfaceContainerHigh,
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                shadowElevation = 8.dp,
                modifier = Modifier.width(256.dp),
            ) {
                // Scrolls when it does not fit (landscape, large text).
                Column(
                    Modifier
                        .verticalScroll(rememberScrollState())
                        .padding(6.dp),
                ) {
                    shownItems.forEach { action ->
                        if (action == SelectionAction.OFFLINE && offline != null) {
                            OfflineMenuRow(offline, enabled(action)) { onAction(action) }
                        } else {
                            MenuRow(action.icon, action.label(quick = false), enabled(action), danger = action.danger) { onAction(action) }
                        }
                    }
                }
            }
        }
        // The X in the button's place; it turns in as the card opens.
        if (open) {
            val turn = remember { Animatable(-90f) }
            LaunchedEffect(Unit) { turn.animateTo(0f, spring(dampingRatio = 0.6f, stiffness = Spring.StiffnessMediumLow)) }
            with(density) {
                FloatingActionButton(
                    onClick = onClose,
                    containerColor = MaterialTheme.colorScheme.primary,
                    contentColor = MaterialTheme.colorScheme.onPrimary,
                    modifier = Modifier
                        .offset { IntOffset(fab.left.roundToInt(), fab.top.roundToInt()) }
                        .size(fab.width.toDp(), fab.height.toDp()),
                ) {
                    Icon(Icons.Default.Close, contentDescription = closeLabel, modifier = Modifier.graphicsLayer { rotationZ = turn.value })
                }
            }
        }
    }
}

/** A row of the selection menu card. */
@Composable
private fun MenuRow(
    icon: ImageVector,
    label: String,
    enabled: Boolean,
    danger: Boolean = false,
    onClick: () -> Unit,
) {
    MenuRowLayout(
        icon = icon,
        label = label,
        enabled = enabled,
        danger = danger,
        modifier = Modifier.clickable(enabled = enabled, role = Role.Button, onClick = onClick),
    )
}

/**
 * Fylvo: "Available offline" in the card, as in the file sheet: a switch for folders and for
 * anything inside an offline folder (on, cannot be changed there), a toggle entry for files.
 */
@Composable
private fun OfflineMenuRow(offline: OfflineSwitch, enabled: Boolean, onToggle: () -> Unit) {
    if (offline.folder || offline.covered) {
        val checked = offline.on || offline.covered
        val canToggle = enabled && !offline.covered
        MenuRowLayout(
            icon = if (checked) Icons.Default.OfflinePin else Icons.Outlined.OfflinePin,
            label = stringResource(R.string.available_offline),
            supporting = if (offline.covered) stringResource(R.string.offline_covered) else null,
            enabled = canToggle,
            modifier = Modifier.toggleable(value = checked, enabled = canToggle, role = Role.Switch, onValueChange = { onToggle() }),
        ) {
            Switch(checked = checked, onCheckedChange = null, enabled = canToggle)
        }
    } else {
        MenuRow(
            if (offline.on) Icons.Default.OfflinePin else Icons.Outlined.OfflinePin,
            stringResource(if (offline.on) R.string.remove_offline_copy else R.string.keep_offline),
            enabled,
            onClick = onToggle,
        )
    }
}

@Composable
private fun MenuRowLayout(
    icon: ImageVector,
    label: String,
    enabled: Boolean,
    modifier: Modifier,
    danger: Boolean = false,
    supporting: String? = null,
    trailing: (@Composable () -> Unit)? = null,
) {
    val colors = MaterialTheme.colorScheme
    val alpha = if (enabled) 1f else 0.38f
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 48.dp)
            .clip(RoundedCornerShape(14.dp))
            .then(modifier)
            .padding(horizontal = 12.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            icon, contentDescription = null,
            tint = (if (danger) colors.error else colors.onSurfaceVariant).copy(alpha = alpha),
            modifier = Modifier.size(20.dp),
        )
        Column(
            Modifier
                .weight(1f)
                .padding(start = 14.dp, end = 8.dp),
        ) {
            Text(label, style = MaterialTheme.typography.bodyLarge, color = (if (danger) colors.error else colors.onSurface).copy(alpha = alpha))
            if (supporting != null) {
                Text(supporting, style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant.copy(alpha = alpha))
            }
        }
        trailing?.invoke()
    }
}

private val SelectionAction.danger: Boolean
    get() = this == SelectionAction.DELETE || this == SelectionAction.DELETE_FOREVER

private val SelectionAction.icon: ImageVector
    get() = when (this) {
        SelectionAction.SELECT_ALL -> Icons.Default.SelectAll
        SelectionAction.DETAILS -> Icons.Outlined.Info
        SelectionAction.SHOW_IN_FOLDER -> Icons.Default.FolderOpen
        SelectionAction.SHARE -> Icons.Default.Share
        SelectionAction.DOWNLOAD -> Icons.Default.Download
        SelectionAction.DELETE -> Icons.Outlined.Delete
        SelectionAction.MOVE -> Icons.AutoMirrored.Filled.DriveFileMove
        SelectionAction.COPY -> Icons.Default.FileCopy
        SelectionAction.RENAME -> Icons.Default.DriveFileRenameOutline
        SelectionAction.OFFLINE -> Icons.Outlined.OfflinePin
        SelectionAction.RESTORE -> Icons.Default.RestoreFromTrash
        SelectionAction.DELETE_FOREVER -> Icons.Default.DeleteForever
    }

/** [quick]: the short label of a quick button ("Move" instead of "Move to"). */
@Composable
private fun SelectionAction.label(quick: Boolean): String = stringResource(
    when (this) {
        SelectionAction.SELECT_ALL -> R.string.select_all
        SelectionAction.DETAILS -> R.string.details
        SelectionAction.SHOW_IN_FOLDER -> R.string.show_in_folder
        SelectionAction.SHARE -> R.string.share
        SelectionAction.DOWNLOAD -> R.string.download
        SelectionAction.DELETE -> R.string.delete
        SelectionAction.MOVE -> if (quick) R.string.move else R.string.move_to
        SelectionAction.COPY -> R.string.copy_to
        SelectionAction.RENAME -> R.string.rename
        SelectionAction.OFFLINE -> R.string.available_offline
        SelectionAction.RESTORE -> R.string.restore
        SelectionAction.DELETE_FOREVER -> R.string.delete_forever
    },
)
