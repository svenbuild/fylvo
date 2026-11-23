package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ActivityEntry
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/**
 * Fylvo 1.6: entries of the activity log as sentences, the same as the web app
 * (`Common/Activity/activityText.ts`): a file's history and the account's sign-ins.
 */
object ActivityText {
    const val LOGIN = 30
    const val LOGIN_FAILED = 31
    const val LOGIN_LOCKED = 32

    fun failedSignIn(entry: ActivityEntry): Boolean = entry.type == LOGIN_FAILED || entry.type == LOGIN_LOCKED

    /** The device or way of an entry from its user agent, as far as it can be told; "" when unknown. */
    fun device(entry: ActivityEntry, windowsApp: String, androidApp: String, on: String): String {
        if (entry.via == "webdav" || entry.content?.method == "webdav") return "WebDAV"
        if (entry.via == "office") return "Office"
        val ua = entry.userAgent
        if (ua.isEmpty()) return ""
        if (ua.startsWith("fylvo-desktop/")) return windowsApp
        if (ua.startsWith("Fylvo-Android/") || ua.startsWith("okhttp/") || "FylvoAndroid" in ua) return androidApp
        val browser = when {
            "Edg/" in ua -> "Edge"
            "Firefox/" in ua -> "Firefox"
            "Chrome/" in ua -> "Chrome"
            "Safari/" in ua -> "Safari"
            else -> ""
        }
        val system = when {
            "Android" in ua -> "Android"
            "iPhone" in ua || "iPad" in ua -> "iOS"
            "Windows" in ua -> "Windows"
            "Mac OS" in ua -> "macOS"
            "Linux" in ua -> "Linux"
            else -> ""
        }
        return listOf(browser, system).filter { it.isNotEmpty() }.joinToString(on).ifEmpty { ua.take(60) }
    }

    fun time(iso: String): String = runCatching {
        OffsetDateTime.parse(iso).atZoneSameInstant(ZoneId.systemDefault())
            .format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT))
    }.getOrDefault(iso)
}

/** The sentence of [entry]. */
@Composable
fun activitySentence(entry: ActivityEntry): String {
    val c = entry.content
    val actor = when {
        entry.system -> stringResource(R.string.activity_system)
        entry.guest -> stringResource(R.string.activity_guest)
        entry.actor != null -> entry.actor.nickname.ifEmpty { stringResource(R.string.activity_deleted_account) }
        else -> stringResource(R.string.activity_someone)
    }
    val name = c?.name.orEmpty()
    val to = c?.toFolder?.ifEmpty { null } ?: stringResource(R.string.my_files)
    val kind = when (c?.target) {
        "link" -> stringResource(R.string.activity_share_link)
        "upload" -> stringResource(R.string.activity_share_upload)
        "members" -> stringResource(R.string.activity_share_members)
        else -> c?.target.orEmpty()
    }
    val method = when (c?.method) {
        "password" -> stringResource(R.string.activity_method_password)
        "2fa" -> stringResource(R.string.activity_method_2fa)
        "passkey" -> stringResource(R.string.activity_method_passkey)
        "app" -> stringResource(R.string.activity_method_app)
        "webdav" -> "WebDAV"
        else -> c?.method.orEmpty()
    }
    return when (entry.type) {
        1 -> stringResource(R.string.activity_1, actor, name)
        2 -> stringResource(R.string.activity_2, actor, name)
        3 -> stringResource(R.string.activity_3, actor, name)
        4 -> stringResource(R.string.activity_4, actor, c?.oldName.orEmpty(), name)
        5 -> stringResource(R.string.activity_5, actor, name, to)
        6 -> stringResource(R.string.activity_6, actor, name, to)
        7 -> stringResource(R.string.activity_7, actor, name)
        8 -> stringResource(R.string.activity_8, actor, name)
        9 -> stringResource(R.string.activity_9, actor, name)
        10 -> stringResource(R.string.activity_10, actor, name)
        11 -> stringResource(R.string.activity_11, actor, name)
        12 -> stringResource(R.string.activity_12, actor, name, kind)
        13 -> stringResource(R.string.activity_13, actor, name, kind)
        14 -> stringResource(R.string.activity_14, actor, name, kind)
        15 -> stringResource(R.string.activity_15, actor, name)
        ActivityText.LOGIN -> if (c?.method == "app" && c.target.isNotEmpty()) stringResource(R.string.activity_30_app, appName(c.target))
            else stringResource(R.string.activity_30, method)
        ActivityText.LOGIN_FAILED -> stringResource(R.string.activity_31, method)
        ActivityText.LOGIN_LOCKED -> stringResource(R.string.activity_32, method)
        33 -> when (c?.method) {
            "admin" -> stringResource(R.string.activity_33_admin)
            "reset" -> stringResource(R.string.activity_33_reset)
            else -> stringResource(R.string.activity_33)
        }
        34 -> stringResource(R.string.activity_34)
        35 -> stringResource(R.string.activity_35)
        36 -> stringResource(R.string.activity_36, appName(c?.target.orEmpty()))
        else -> stringResource(R.string.activity_unknown)
    }
}

/** An app of a sign-in or a revoked access; the server names Fylvo's own apps by a web text key. */
@Composable
private fun appName(target: String): String = when {
    target.endsWith("fylvoDesktop") -> stringResource(R.string.activity_windows_app)
    target.startsWith("application:") -> target.substringAfterLast('.')
    else -> target
}

/** One quiet row of a history: the sentence, below it when (and how, when known). */
@Composable
fun ActivityRow(entry: ActivityEntry, withDevice: Boolean = false) {
    val device = if (withDevice) ActivityText.device(
        entry,
        windowsApp = stringResource(R.string.activity_windows_app),
        androidApp = stringResource(R.string.activity_android_app),
        on = stringResource(R.string.activity_on),
    ) else ""
    val failed = ActivityText.failedSignIn(entry)
    Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp)) {
        Text(
            activitySentence(entry),
            style = MaterialTheme.typography.bodyMedium,
            color = if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
        )
        Text(
            listOf(ActivityText.time(entry.createdAt), device, entry.ip).filter { it.isNotEmpty() }.joinToString(" · "),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
