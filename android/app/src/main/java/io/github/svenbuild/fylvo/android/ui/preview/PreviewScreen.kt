package io.github.svenbuild.fylvo.android.ui.preview

import android.app.Activity
import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.ui.files.FileKind
import io.github.svenbuild.fylvo.android.ui.files.fileIconOf
import io.github.svenbuild.fylvo.android.ui.files.fileIconTint
import io.github.svenbuild.fylvo.android.ui.files.FilesViewModel
import io.github.svenbuild.fylvo.android.ui.files.subtitleFor
import io.github.svenbuild.fylvo.android.util.userError
import java.io.File

/** Which viewer to use; anything else is handed to the system. */
enum class PreviewKind { IMAGE, VIDEO, AUDIO, PDF, TEXT }

fun previewKindOf(file: FileObject): PreviewKind? = when (FileKind.of(file)) {
    FileKind.IMAGE -> if (file.name.substringAfterLast('.', "").lowercase() in RASTER) PreviewKind.IMAGE else null
    FileKind.VIDEO -> PreviewKind.VIDEO
    FileKind.AUDIO -> PreviewKind.AUDIO
    FileKind.PDF -> PreviewKind.PDF
    FileKind.DOCUMENT, FileKind.CODE, FileKind.SPREADSHEET ->
        if (file.name.substringAfterLast('.', "").lowercase() in PLAIN_TEXT && file.size <= TEXT_LIMIT) PreviewKind.TEXT else null
    else -> null
}

private val RASTER = setOf("jpg", "jpeg", "png", "gif", "webp", "bmp", "heic", "heif", "avif")
private val PLAIN_TEXT = setOf(
    "txt", "md", "markdown", "csv", "tsv", "json", "xml", "yaml", "yml", "toml", "ini", "cfg", "conf", "log",
    "kt", "kts", "java", "rs", "go", "py", "js", "ts", "tsx", "jsx", "c", "h", "cpp", "hpp", "cs", "swift", "rb",
    "php", "sh", "zsh", "fish", "html", "htm", "css", "scss", "sql", "gradle", "lock", "tex",
)
private const val TEXT_LIMIT = 2L * 1024 * 1024

/**
 * Full-screen viewer over the files of the current folder that share the
 * tapped file's media group. Swipe between them; tap to toggle the chrome.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PreviewScreen(
    files: List<FileObject>,
    startIndex: Int,
    viewModel: FilesViewModel,
    onClose: () -> Unit,
    onShare: (FileObject) -> Unit,
    onDownload: (FileObject) -> Unit,
    onOpenWith: (FileObject) -> Unit,
    onDelete: (FileObject) -> Unit,
    /** Fylvo: hide actions the user may not take on this file (see util/Permissions). */
    canShare: (FileObject) -> Boolean,
    canDelete: (FileObject) -> Boolean,
    /** Fylvo 1.6: the text editor may open this file (see TextEditorScreen). */
    canEdit: (FileObject) -> Boolean = { false },
    onEdit: (FileObject) -> Unit = {},
) {
    val pager = rememberPagerState(initialPage = startIndex.coerceIn(0, files.lastIndex)) { files.size }
    var chromeVisible by remember { mutableStateOf(true) }
    var menuOpen by remember { mutableStateOf(false) }
    val current = files[pager.currentPage]
    // Fylvo: shown from the copy on this phone because the server failed.
    val staleCopies = viewModel.state.collectAsState().value.staleCopies
    val dark = previewKindOf(current).let { it == PreviewKind.IMAGE || it == PreviewKind.VIDEO }

    BackHandler(onBack = onClose)

    // Light system bar icons over the black image and video viewer, also in the light theme.
    val view = LocalView.current
    val themeLight = MaterialTheme.colorScheme.surface.luminance() > 0.5f
    val controller = (view.context as? Activity)?.window?.let { WindowCompat.getInsetsController(it, view) }
    // After every recomposition, so it wins over the theme setting the bars again.
    SideEffect {
        controller?.isAppearanceLightStatusBars = themeLight && !dark
        controller?.isAppearanceLightNavigationBars = themeLight && !dark
    }
    DisposableEffect(themeLight) {
        onDispose {
            controller?.isAppearanceLightStatusBars = themeLight
            controller?.isAppearanceLightNavigationBars = themeLight
        }
    }

    // Surface (not Box) so LocalContentColor matches the background; the
    // viewer sits outside the Scaffold and would otherwise inherit black text.
    Surface(
        modifier = Modifier.fillMaxSize(),
        color = if (dark) Color.Black else MaterialTheme.colorScheme.surface,
        contentColor = if (dark) Color.White else MaterialTheme.colorScheme.onSurface,
    ) {
      Box(Modifier.fillMaxSize()) {
        HorizontalPager(state = pager, beyondViewportPageCount = 1, modifier = Modifier.fillMaxSize()) { page ->
            PreviewPage(
                file = files[page],
                viewModel = viewModel,
                active = page == pager.currentPage,
                chromeVisible = chromeVisible,
                onChromeChange = { chromeVisible = it },
                onTap = { chromeVisible = !chromeVisible },
                onOpenWith = { onOpenWith(files[page]) },
                onDismiss = onClose,
            )
        }

        AnimatedVisibility(visible = chromeVisible, enter = fadeIn(), exit = fadeOut()) {
            TopAppBar(
                title = {
                    Column {
                        Text(current.name, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(
                            buildString {
                                append(io.github.svenbuild.fylvo.android.ui.files.fileSubtitle(current))
                                if (files.size > 1) append(" · " + stringResource(R.string.position_of, pager.currentPage + 1, files.size))
                            },
                            style = MaterialTheme.typography.labelMedium,
                            color = if (dark) Color.White.copy(alpha = 0.7f) else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        if (current.path in staleCopies) {
                            Text(
                                stringResource(R.string.offline_copy_may_be_outdated),
                                style = MaterialTheme.typography.labelSmall,
                                color = if (dark) Color.White.copy(alpha = 0.7f) else MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onClose) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
                },
                actions = {
                    if (previewKindOf(current) == PreviewKind.TEXT && canEdit(current)) {
                        IconButton(onClick = { onEdit(current) }) { Icon(Icons.Outlined.Edit, contentDescription = stringResource(R.string.edit)) }
                    }
                    if (canShare(current)) {
                        IconButton(onClick = { onShare(current) }) { Icon(Icons.Default.Link, contentDescription = stringResource(R.string.copy_share_link)) }
                    }
                    Box {
                        IconButton(onClick = { menuOpen = true }) { Icon(Icons.Default.MoreVert, contentDescription = stringResource(R.string.more)) }
                        DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.open_with_another_app)) },
                                leadingIcon = { Icon(Icons.AutoMirrored.Filled.OpenInNew, null) },
                                onClick = { menuOpen = false; onOpenWith(current) },
                            )
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.download)) },
                                leadingIcon = { Icon(Icons.Default.Download, null) },
                                onClick = { menuOpen = false; onDownload(current) },
                            )
                            if (canDelete(current)) {
                                DropdownMenuItem(
                                    text = { Text(stringResource(R.string.delete)) },
                                    leadingIcon = { Icon(Icons.Default.Delete, null) },
                                    onClick = { menuOpen = false; onDelete(current) },
                                )
                            }
                        }
                    }
                },
                colors = if (dark) TopAppBarDefaults.topAppBarColors(
                    containerColor = Color.Black.copy(alpha = 0.55f),
                    titleContentColor = Color.White,
                    navigationIconContentColor = Color.White,
                    actionIconContentColor = Color.White,
                ) else TopAppBarDefaults.topAppBarColors(),
            )
        }
      }
    }
}

/** Space the floating top bar occupies: status bar inset plus the 64 dp bar, with a little air. */
@Composable
internal fun chromeTopPadding(): androidx.compose.ui.unit.Dp =
    WindowInsets.statusBars.asPaddingValues().calculateTopPadding() + 64.dp + 8.dp

@Composable
private fun PreviewPage(
    file: FileObject,
    viewModel: FilesViewModel,
    active: Boolean,
    chromeVisible: Boolean,
    onChromeChange: (Boolean) -> Unit,
    onTap: () -> Unit,
    onOpenWith: () -> Unit,
    onDismiss: () -> Unit,
) {
    when (previewKindOf(file)) {
        PreviewKind.IMAGE -> ImagePreview(file, viewModel, active, onTap, onDismiss)
        PreviewKind.VIDEO, PreviewKind.AUDIO -> MediaPreview(file, viewModel, active, chromeVisible, onChromeChange)
        PreviewKind.PDF -> PdfPreview(file, viewModel, onTap)
        PreviewKind.TEXT -> TextPreview(file, viewModel)
        null -> NoPreview(file, onOpenWith)
    }
}

@Composable
private fun NoPreview(file: FileObject, onOpenWith: () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(32.dp)) {
            Icon(fileIconOf(file), contentDescription = null, tint = fileIconTint(file), modifier = Modifier.size(72.dp))
            Text(stringResource(R.string.no_preview), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 16.dp))
            Button(onClick = onOpenWith, modifier = Modifier.padding(top = 16.dp)) { Text(stringResource(R.string.open_with_another_app)) }
        }
    }
}

/** Centered progress used by the loaders below. */
@Composable
internal fun LoadingOverlay(progress: Float?, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        if (progress != null) CircularProgressIndicator(progress = { progress }) else CircularProgressIndicator()
    }
}

/** Load a file's local copy, reporting progress; null until ready. */
@Composable
internal fun rememberLocalCopy(file: FileObject, viewModel: FilesViewModel): Triple<File?, Float?, String?> {
    val context = LocalContext.current
    var local by remember(file.id) { mutableStateOf<File?>(null) }
    var progress by remember(file.id) { mutableStateOf<Float?>(null) }
    var error by remember(file.id) { mutableStateOf<String?>(null) }
    LaunchedEffect(file.id) {
        runCatching { viewModel.localCopy(context, file) { progress = it } }
            .onSuccess { local = it }
            .onFailure { error = userError(it, fallback = R.string.download_failed).resolve(context) }
    }
    return Triple(local, progress, error)
}

@Composable
internal fun FullWidthError(message: String) {
    Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
        Text(message, color = MaterialTheme.colorScheme.error)
    }
}
