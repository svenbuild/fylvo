package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Article
import androidx.compose.material.icons.automirrored.outlined.MenuBook
import androidx.compose.material.icons.outlined.AccountTree
import androidx.compose.material.icons.outlined.Draw
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import io.github.svenbuild.fylvo.android.api.FileObject

/** One entry of the server's file type icons (`site/config/explorer` -> `icons`). */
@Serializable
data class FileTypeIcon(
    val exts: List<String> = emptyList(),
    val icon: String = "",
    val color: String = "",
    @SerialName("color_dark") val colorDark: String = "",
)

/**
 * Fylvo: the colours of file type icons, the same as in the web app. The server's table is
 * fetched once per app start ([update]); until then, and without a connection, the built-in copy
 * of the server's defaults (inventory/setting.go defaultIcons) is used.
 */
object FileIcons {
    private val json = Json { ignoreUnknownKeys = true }

    val DEFAULTS: List<FileTypeIcon> = listOf(
        FileTypeIcon(listOf("mp3", "flac", "ape", "wav", "acc", "ogg", "m4a"), "audio", "#23775E", "#62BCA0"),
        FileTypeIcon(listOf("m3u8", "mp4", "flv", "avi", "wmv", "mkv", "rm", "rmvb", "mov", "ogv"), "video", "#6945AE", "#B292EF"),
        FileTypeIcon(listOf("bmp", "iff", "png", "gif", "jpg", "jpeg", "psd", "svg", "webp", "heif", "heic", "tiff", "avif"), "image", "#3265A4", "#75A9E8"),
        FileTypeIcon(
            listOf(
                "3fr", "ari", "arw", "bay", "braw", "crw", "cr2", "cr3", "cap", "dcs", "dcr", "dng", "drf", "eip", "erf", "fff", "gpr",
                "iiq", "k25", "kdc", "mdc", "mef", "mos", "mrw", "nef", "nrw", "obm", "orf", "pef", "ptx", "pxn", "r3d", "raf", "raw",
                "rwl", "rw2", "rwz", "sr2", "srf", "srw", "tif", "x3f",
            ),
            "raw", "#3265A4", "#75A9E8",
        ),
        FileTypeIcon(listOf("pdf"), "pdf", "#B53E3B", "#F27B75"),
        FileTypeIcon(listOf("doc", "docx"), "word", "#538ce5"),
        FileTypeIcon(listOf("ppt", "pptx"), "ppt", "#EF633F"),
        FileTypeIcon(listOf("xls", "xlsx", "csv"), "excel", "#4caf50"),
        FileTypeIcon(listOf("txt", "html"), "text", "#66686E", "#A9A9B0"),
        FileTypeIcon(listOf("torrent"), "torrent", "#5c6bc0"),
        FileTypeIcon(listOf("zip", "gz", "xz", "tar", "rar", "7z", "bz2", "z"), "zip", "#99651C", "#D8A55A"),
        FileTypeIcon(listOf("exe", "msi"), "exe", "#1a237e"),
        FileTypeIcon(listOf("apk"), "android", "#8bc34a"),
        FileTypeIcon(listOf("go"), "go", "#16b3da"),
        FileTypeIcon(listOf("py"), "python", "#3776ab"),
        FileTypeIcon(listOf("c"), "c", "#a4c639"),
        FileTypeIcon(listOf("cpp"), "cpp", "#f34b7d"),
        FileTypeIcon(listOf("js", "jsx"), "js", "#f4d003"),
        FileTypeIcon(listOf("epub"), "book", "#81b315"),
        FileTypeIcon(listOf("rs"), "rust", "#000", "#fff"),
        FileTypeIcon(listOf("drawio"), "flowchart", "#F08705"),
        FileTypeIcon(listOf("dwb"), "whiteboard", "#F08705"),
        FileTypeIcon(listOf("md"), "markdown", "#66686E", "#A9A9B0"),
        FileTypeIcon(listOf("excalidraw")),
    )

    /** Compose state, so lists recolour when the server's table arrives. */
    var table: List<FileTypeIcon> by mutableStateOf(DEFAULTS)
        private set

    private val fetching = java.util.concurrent.atomic.AtomicBoolean(false)

    /** True once per app start: the caller fetches the server's table. */
    fun claimFetch(): Boolean = fetching.compareAndSet(false, true)

    /** No answer (no connection): the next screen may try again. */
    fun fetchFailed() = fetching.set(false)

    /** Takes the server's `icons` value (a list, or a JSON string holding one); ignored when unreadable. */
    fun update(icons: JsonElement?) {
        parse(icons)?.takeIf { it.isNotEmpty() }?.let { table = it }
    }

    fun parse(icons: JsonElement?): List<FileTypeIcon>? = runCatching {
        val list = when (icons) {
            is JsonArray -> icons
            is JsonPrimitive -> if (icons.isString) json.parseToJsonElement(icons.content) else return null
            else -> return null
        }
        json.decodeFromJsonElement(ListSerializer(FileTypeIcon.serializer()), list)
    }.getOrNull()

    /** The entry for [fileName]'s extension in [table], or null. */
    fun find(fileName: String, table: List<FileTypeIcon> = this.table): FileTypeIcon? {
        val ext = fileName.substringAfterLast('.', "").lowercase()
        if (ext.isEmpty()) return null
        return table.firstOrNull { entry -> entry.exts.any { it.equals(ext, ignoreCase = true) } }
    }

    /** ARGB of the entry's colour for the light or dark look (dark falls back to light), or null. */
    fun argb(entry: FileTypeIcon, dark: Boolean): Int? =
        parseHexColor(entry.colorDark.takeIf { dark && it.isNotBlank() } ?: entry.color)

    /** "#rgb", "#rrggbb" or "#aarrggbb" as ARGB; null when it is not one of those. */
    fun parseHexColor(value: String): Int? {
        val hex = value.trim().removePrefix("#")
        if (hex.any { it.lowercaseChar() !in "0123456789abcdef" }) return null
        val full = when (hex.length) {
            3 -> "ff" + hex.map { "$it$it" }.joinToString("")
            6 -> "ff$hex"
            8 -> hex
            else -> return null
        }
        return full.toLong(16).toInt()
    }
}

/** Fylvo: tint of a file's type icon, the web app's colour; folders and unknown types stay neutral. */
@Composable
fun fileIconTint(file: FileObject): Color {
    val neutral = MaterialTheme.colorScheme.onSurfaceVariant
    if (file.isFolder) return neutral
    val entry = FileIcons.find(file.name) ?: return neutral
    return FileIcons.argb(entry, isSystemInDarkTheme())?.let { Color(it) } ?: neutral
}

/** Fylvo: the glyph for a file: the server's icon type where Material has a closer one, else its kind. */
fun fileIconOf(file: FileObject): ImageVector {
    val kind = FileKind.of(file)
    if (file.isFolder) return kind.icon
    return when (FileIcons.find(file.name)?.icon) {
        "text", "markdown" -> Icons.AutoMirrored.Outlined.Article
        "book" -> Icons.AutoMirrored.Outlined.MenuBook
        "exe" -> Icons.Outlined.Terminal
        "flowchart" -> Icons.Outlined.AccountTree
        "whiteboard" -> Icons.Outlined.Draw
        else -> kind.icon
    }
}
