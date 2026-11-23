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
import coil.compose.SubcomposeAsyncImage
import coil.request.ImageRequest
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.ui.files.FilesViewModel
import io.github.svenbuild.fylvo.android.ui.files.rememberThumbUrl
import io.github.svenbuild.fylvo.android.ui.files.thumbCacheKey
import io.github.svenbuild.fylvo.android.util.asString
import kotlinx.coroutines.launch

/**
 * Full-resolution image with pinch zoom, drag when zoomed, double-tap to
 * toggle 1x/2.5x. The thumbnail (usually already cached by the list) is
 * shown while the original downloads, so the viewer never opens blank.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ImagePreview(file: FileObject, viewModel: FilesViewModel, onTap: () -> Unit, onDismiss: () -> Unit = {}) {
    val thumb = rememberThumbUrl(viewModel, file)
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    var fullFailed by remember(file.id) { mutableStateOf(false) }
    // Fylvo: inside a connected storage the reason is shown (provider busy, file changed).
    var fullError by remember(file.id) { mutableStateOf<io.github.svenbuild.fylvo.android.util.UiText?>(null) }
    val fullUrl by produceState<String?>(initialValue = null, file.id) {
        value = runCatching { viewModel.previewUrl(file) }.onFailure { e ->
            if (io.github.svenbuild.fylvo.android.util.Connected.isConnected(file.path)) {
                fullError = io.github.svenbuild.fylvo.android.util.userError(e)
            }
        }.getOrNull()
        fullFailed = value == null
    }

    var scale by remember(file.id) { mutableFloatStateOf(1f) }
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
        val model = fullUrl ?: thumb
        if (model != null) {
            SubcomposeAsyncImage(
                model = ImageRequest.Builder(LocalContext.current)
                    .data(model)
                    // Fylvo: the preview is kept under its own key (thumbCacheKey), also while it
                    // stands in for the full image.
                    .placeholderMemoryCacheKey(thumbCacheKey(thumb) ?: thumb)
                    .apply {
                        if (model == thumb) {
                            memoryCacheKey(thumbCacheKey(thumb))
                            diskCacheKey(thumbCacheKey(thumb))
                        }
                    }
                    .crossfade(true)
                    .build(),
                contentDescription = file.name,
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer {
                        val shrink = 1f - (kotlin.math.abs(dragY) / (dismissThresholdPx * 6f)).coerceIn(0f, 0.25f)
                        scaleX = scale * shrink
                        scaleY = scale * shrink
                        translationX = offset.x
                        translationY = offset.y + dragY
                        alpha = 1f - (kotlin.math.abs(dragY) / (dismissThresholdPx * 3f)).coerceIn(0f, 0.6f)
                    },
                loading = { LoadingOverlay(null) },
                onError = { state ->
                    val status = (state.result.throwable as? coil.network.HttpException)?.response?.code
                    // Only the full image counts; a failed thumbnail says nothing about the file.
                    if (status != null && fullUrl != null && state.result.request.data == fullUrl) {
                        scope.launch { fullError = viewModel.contentFailed(file, status) }
                    }
                },
                error = { FullWidthError(fullError?.asString() ?: stringResource(R.string.image_load_failed)) },
            )
        } else if (fullFailed) {
            FullWidthError(fullError?.asString() ?: stringResource(R.string.image_load_failed))
        } else {
            LoadingOverlay(null)
        }
    }
}
