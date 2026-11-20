package io.github.svenbuild.fylvo.android.util

import android.content.Context
import androidx.annotation.PluralsRes
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext

/**
 * A text a view model hands to the screen. Resource texts are resolved where they are shown,
 * so they follow the app language; [Raw] is for texts that come as they are (server messages).
 * Arguments may be [UiText] themselves.
 */
sealed interface UiText {
    data class Raw(val text: String) : UiText
    class Res(@StringRes val id: Int, vararg val args: Any) : UiText
    class Plural(@PluralsRes val id: Int, val count: Int, vararg val args: Any) : UiText

    fun resolve(context: Context): String = when (this) {
        is Raw -> text
        is Res -> context.getString(id, *resolveArgs(args, context))
        is Plural -> context.resources.getQuantityString(id, count, *resolveArgs(args, context))
    }
}

private fun resolveArgs(args: Array<out Any>, context: Context): Array<Any> =
    Array(args.size) { i -> (args[i] as? UiText)?.resolve(context) ?: args[i] }

@Composable
fun UiText.asString(): String {
    LocalConfiguration.current // re-read on a language change
    return resolve(LocalContext.current)
}
