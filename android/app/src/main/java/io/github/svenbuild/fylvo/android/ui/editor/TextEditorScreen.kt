package io.github.svenbuild.fylvo.android.ui.editor

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.ui.files.FileKind
import io.github.svenbuild.fylvo.android.api.FileObject

/** Fylvo 1.6: endings shown as Markdown (with a preview). */
private val MARKDOWN = setOf("md", "markdown")

/**
 * Fylvo 1.6: the text and Markdown editor (design/android-parity-plan.md, 4): a plain text field,
 * "Save", for Markdown a switch between editing and a preview. Leaving with changes asks first.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TextEditorScreen(viewModel: TextEditorViewModel, onClose: (wroteSomething: Boolean) -> Unit) {
    val context = LocalContext.current
    val state by viewModel.state.collectAsState()
    val shown = state.target ?: return
    val markdown = shown.name.substringAfterLast('.', "").lowercase() in MARKDOWN
    val code = (shown as? EditorTarget.Existing)?.file?.let { FileKind.of(it) == FileKind.CODE } == true
    var preview by remember { mutableStateOf(false) }
    var askLeave by remember { mutableStateOf(false) }
    /** "Save" in the leave question: close once the save went through. */
    var closeAfterSave by remember { mutableStateOf(false) }
    val snackbar = remember { SnackbarHostState() }
    val scope = androidx.compose.runtime.rememberCoroutineScope()

    LaunchedEffect(state.saving, closeAfterSave) {
        if (closeAfterSave && !state.saving) {
            closeAfterSave = false
            // Typed on while saving: ask instead of closing.
            if (!viewModel.dirty) onClose(state.wroteSomething) else if (state.conflict == null) askLeave = true
        }
    }
    fun leave() {
        when {
            // A save on its way finishes first (its answer belongs to this file).
            state.saving -> {
                closeAfterSave = true
                scope.launch { snackbar.showSnackbar(context.getString(R.string.editor_saving_wait)) }
            }
            viewModel.dirty && state.loadError == null && !state.loading -> askLeave = true
            else -> onClose(state.wroteSomething)
        }
    }
    BackHandler { leave() }

    LaunchedEffect(state.message) {
        state.message?.let {
            snackbar.showSnackbar(it.resolve(context))
            viewModel.messageShown()
        }
    }

    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(shown.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    navigationIcon = {
                        IconButton(onClick = ::leave) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
                    },
                    actions = {
                        if (markdown && state.loadError == null) {
                            IconButton(onClick = { preview = !preview }) {
                                Icon(
                                    if (preview) Icons.Outlined.Edit else Icons.Outlined.Visibility,
                                    contentDescription = stringResource(if (preview) R.string.editor_edit else R.string.editor_preview),
                                )
                            }
                        }
                        if (state.loadError == null && !state.loading) {
                            TextButton(enabled = !state.saving && viewModel.dirty, onClick = viewModel::save) {
                                if (state.saving) CircularProgressIndicator(Modifier.padding(2.dp), strokeWidth = 2.dp)
                                else Text(stringResource(R.string.save))
                            }
                        }
                    },
                )
            },
            snackbarHost = { SnackbarHost(snackbar) },
        ) { padding ->
            Box(Modifier.fillMaxSize().padding(padding).imePadding()) {
                when {
                    state.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                    state.loadError != null -> Text(
                        state.loadError!!.resolve(context),
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.align(Alignment.Center).padding(24.dp),
                    )
                    preview && markdown -> MarkdownPreview(viewModel.text.text.toString())
                    else -> BasicTextField(
                        state = viewModel.text,
                        textStyle = MaterialTheme.typography.bodyLarge.copy(
                            color = MaterialTheme.colorScheme.onSurface,
                            fontFamily = if (code) FontFamily.Monospace else MaterialTheme.typography.bodyLarge.fontFamily,
                        ),
                        cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                        modifier = Modifier.fillMaxSize().padding(horizontal = 16.dp, vertical = 8.dp),
                    )
                }
            }
        }
    }

    if (askLeave) {
        AlertDialog(
            onDismissRequest = { askLeave = false },
            title = { Text(stringResource(R.string.editor_unsaved_title)) },
            text = { Text(stringResource(R.string.editor_unsaved_text)) },
            confirmButton = {
                TextButton(onClick = { askLeave = false; closeAfterSave = true; viewModel.save() }) { Text(stringResource(R.string.save)) }
            },
            dismissButton = {
                Column(horizontalAlignment = Alignment.End) {
                    TextButton(enabled = !state.saving, onClick = { askLeave = false; onClose(state.wroteSomething) }) {
                        Text(stringResource(R.string.editor_discard), color = MaterialTheme.colorScheme.error)
                    }
                    TextButton(onClick = { askLeave = false }) { Text(stringResource(R.string.editor_keep_editing)) }
                }
            },
        )
    }

    when (state.conflict) {
        TextEditorViewModel.Conflict.CHANGED -> AlertDialog(
            onDismissRequest = viewModel::dismissConflict,
            title = { Text(stringResource(R.string.editor_changed_title)) },
            text = { Text(stringResource(R.string.editor_changed_text)) },
            confirmButton = { TextButton(onClick = viewModel::saveAsCopy) { Text(stringResource(R.string.editor_save_copy)) } },
            dismissButton = {
                Column(horizontalAlignment = Alignment.End) {
                    TextButton(onClick = viewModel::overwrite) { Text(stringResource(R.string.editor_overwrite), color = MaterialTheme.colorScheme.error) }
                    TextButton(onClick = viewModel::dismissConflict) { Text(stringResource(R.string.cancel)) }
                }
            },
        )
        TextEditorViewModel.Conflict.GONE -> AlertDialog(
            onDismissRequest = viewModel::dismissConflict,
            title = { Text(stringResource(R.string.editor_gone_title)) },
            text = { Text(stringResource(R.string.editor_gone_text)) },
            confirmButton = { TextButton(onClick = viewModel::saveAsCopy) { Text(stringResource(R.string.editor_save_copy)) } },
            dismissButton = { TextButton(onClick = viewModel::dismissConflict) { Text(stringResource(R.string.cancel)) } },
        )
        TextEditorViewModel.Conflict.NAME_TAKEN -> {
            var name by remember { mutableStateOf(shown.name) }
            AlertDialog(
                onDismissRequest = viewModel::dismissConflict,
                title = { Text(stringResource(R.string.editor_name_taken)) },
                text = {
                    OutlinedTextField(value = name, onValueChange = { name = it.take(255) }, singleLine = true, label = { Text(stringResource(R.string.name)) })
                },
                confirmButton = {
                    TextButton(enabled = name.isNotBlank() && '/' !in name && name != shown.name, onClick = { viewModel.rename(name.trim()) }) { Text(stringResource(R.string.save)) }
                },
                dismissButton = { TextButton(onClick = viewModel::dismissConflict) { Text(stringResource(R.string.cancel)) } },
            )
        }
        null -> Unit
    }
}

/** Fylvo 1.6: Markdown as it will look; only http(s) links leave the app. */
@Composable
private fun MarkdownPreview(text: String) {
    val system = LocalUriHandler.current
    val handler = remember(system) {
        object : UriHandler {
            override fun openUri(uri: String) {
                val lower = uri.trim().lowercase()
                if (lower.startsWith("https://") || lower.startsWith("http://")) runCatching { system.openUri(uri) }
            }
        }
    }
    CompositionLocalProvider(LocalUriHandler provides handler) {
        Column(
            Modifier.fillMaxSize().background(MaterialTheme.colorScheme.surface).verticalScroll(rememberScrollState()).padding(16.dp),
        ) {
            com.mikepenz.markdown.m3.Markdown(content = text)
        }
    }
}

/** True for a file the editor can open as text (the preview's text kinds). */
fun editableAsText(file: FileObject): Boolean =
    io.github.svenbuild.fylvo.android.ui.preview.previewKindOf(file) == io.github.svenbuild.fylvo.android.ui.preview.PreviewKind.TEXT &&
        file.size <= io.github.svenbuild.fylvo.android.data.TextRepository.MAX_BYTES
