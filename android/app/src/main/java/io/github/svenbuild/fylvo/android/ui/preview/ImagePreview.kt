package io.github.svenbuild.fylvo.android.ui.preview

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.R

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import coil.request.ImageRequest
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.ui.files.FilesViewModel
import io.github.svenbuild.fylvo.android.ui.files.rememberThumbUrl
import io.github.svenbuild.fylvo.android.ui.files.thumbCacheKey
import io.github.svenbuild.fylvo.android.ui.files.thumbRequest
import io.github.svenbuild.fylvo.android.util.asString
import kotlinx.coroutines.launch

/**
 * Full-resolution image with pinch zoom, drag when zoomed, double-tap to
 * toggle 1x/2.5x. The thumbnail (usually already cached by the list) is
 * shown while the original downloads, so the viewer never opens blank.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ImagePreview(file: FileObject, viewModel: FilesViewModel, active: Boolean = true, onTap: () -> Unit, onDismiss: () -> Unit = {}) {
    val thumb = rememberThumbUrl(viewModel, file)
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    var fullFailed by remember(file.id) { mutableStateOf(false) }
    // Fylvo: inside a connected storage the reason is shown (provider busy, file changed).
    var fullError by remember(file.id) { mutableStateOf<io.github.svenbuild.fylvo.android.util.UiText?>(null) }
    val fullUrl by produceState<String?>(initialValue = null, file.id, active) {
        if (!active) return@produceState
        value = runCatching { viewModel.previewUrl(file) }.onFailure { e ->
            if (io.github.svenbuild.fylvo.android.util.Connected.isConnected(file.path)) {
                fullError = io.github.svenbuild.fylvo.android.util.userError(e)
            }
        }.getOrNull()
        fullFailed = value == null
    }

    var fullShown by remember(file.id) { mutableStateOf(false) }
    var fullDecodeFailed by remember(file.id) { mutableStateOf(false) }
    var scale by remember(file.id) { mutableFloatStateOf(1f) }
    // Zooming in once asks for the sharper decode (the file is on disk by then).
    var zoomed by remember(file.id) { mutableStateOf(false) }
    if (scale > 1f) zoomed = true
    var offset by remember(file.id) { mutableStateOf(Offset.Zero) }
    // Vertical drag at 1x slides the image away and closes the viewer (the Photos gesture).
    var dragY by remember(file.id) { mutableFloatStateOf(0f) }
    val dismissThresholdPx = with(LocalDensity.current) { 120.dp.toPx() }

    Box(
        Modifier
            .fillMaxSize()
            .pointerInput(file.id) {
                detectTapGestures(
                    onTap = { onTap() },
                    onDoubleTap = {
                        if (scale > 1f) {
                            scale = 1f; offset = Offset.Zero
                        } else {
                            scale = 2.5f
                        }
                    },
                )
            }
            .pointerInput(file.id, scale > 1f) {
                if (scale > 1f) return@pointerInput
                detectVerticalDragGestures(
                    onVerticalDrag = { _, dy -> dragY += dy },
                    onDragEnd = { if (kotlin.math.abs(dragY) > dismissThresholdPx) onDismiss() else dragY = 0f },
                    onDragCancel = { dragY = 0f },
                )
            }
            // Pan only while zoomed in, so at 1x a horizontal drag reaches the pager.
            .transformable(
                state = rememberTransformableState { zoom, pan, _ ->
                    val next = (scale * zoom).coerceIn(1f, 6f)
                    scale = next
                    offset = if (next == 1f) Offset.Zero else offset + pan
                },
                canPan = { scale > 1f },
            ),
        contentAlignment = Alignment.Center,
    ) {
        val context = LocalContext.current
        val layerModifier = Modifier
            .fillMaxSize()
            .graphicsLayer {
                val shrink = 1f - (kotlin.math.abs(dragY) / (dismissThresholdPx * 6f)).coerceIn(0f, 0.25f)
                scaleX = scale * shrink
                scaleY = scale * shrink
                translationX = offset.x
                translationY = offset.y + dragY
                alpha = 1f - (kotlin.math.abs(dragY) / (dismissThresholdPx * 3f)).coerceIn(0f, 0.6f)
            }
        // Fylvo: the preview (usually cached by the list) is on screen at once and stays under the
        // full picture until that one is decoded.
        if (thumb != null) {
            AsyncImage(
                model = remember(thumb, file.id, file.primaryEntity) { thumbRequest(context, file, thumb).build() },
                contentDescription = file.name,
                contentScale = ContentScale.Fit,
                modifier = layerModifier,
            )
        }
        // Fylvo: the full picture is decoded smaller (the screen; up to 4096 px once zoomed in), never
        // at its own size, and only on the page that is shown, so the neighbours do not take the line.
        if (fullUrl != null && active) {
            val screen = context.resources.displayMetrics
            val limit = if (zoomed) 4096 else maxOf(screen.widthPixels, screen.heightPixels)
            AsyncImage(
                model = remember(fullUrl, limit) {
                    ImageRequest.Builder(context).data(fullUrl).size(coil.size.Size(limit, limit)).crossfade(true).build()
                },
                contentDescription = file.name,
                contentScale = ContentScale.Fit,
                modifier = layerModifier,
                onSuccess = { fullShown = true },
                onError = { state ->
                    val status = (state.result.throwable as? coil.network.HttpException)?.response?.code
                    if (status != null) scope.launch { fullError = viewModel.contentFailed(file, status) }
                    fullDecodeFailed = true
                },
            )
        }
        if (fullDecodeFailed || (fullFailed && thumb == null)) {
            FullWidthError(fullError?.asString() ?: stringResource(R.string.image_load_failed))
        } else if (thumb == null && !fullShown) {
            LoadingOverlay(null)
        } else if (active && !fullShown && !fullFailed) {
            CircularProgressIndicator(
                modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = 32.dp).size(28.dp),
                strokeWidth = 3.dp,
            )
        }
    }
}
