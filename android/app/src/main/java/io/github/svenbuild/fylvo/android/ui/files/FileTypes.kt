package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.Android
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderZip
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.MusicNote
import androidx.compose.material.icons.outlined.PictureAsPdf
import androidx.compose.material.icons.outlined.Slideshow
import androidx.compose.material.icons.outlined.TableChart
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.ui.graphics.vector.ImageVector
import io.github.svenbuild.fylvo.android.api.FileObject

/**
 * Coarse file categories for icons and tints. Mirrors the extension groups
 * the server ships in `site/config/explorer.icons`, hardcoded so the list
 * renders before (or without) that call.
 */
// Fylvo: calm outline icons in the neutral text color, as in the web app. Color is kept
// for meaning (accent), not for file types.
enum class FileKind(val icon: ImageVector) {
    FOLDER(Icons.Outlined.Folder),
    IMAGE(Icons.Outlined.Image),
    VIDEO(Icons.Outlined.Videocam),
    AUDIO(Icons.Outlined.MusicNote),
    PDF(Icons.Outlined.PictureAsPdf),
    DOCUMENT(Icons.Outlined.Description),
    SPREADSHEET(Icons.Outlined.TableChart),
    PRESENTATION(Icons.Outlined.Slideshow),
    ARCHIVE(Icons.Outlined.FolderZip),
    CODE(Icons.Outlined.Code),
    APK(Icons.Outlined.Android),
    OTHER(Icons.AutoMirrored.Outlined.InsertDriveFile);

    companion object {
        private val byExt: Map<String, FileKind> = buildMap {
            fun put(kind: FileKind, vararg exts: String) = exts.forEach { put(it, kind) }
            put(IMAGE, "jpg", "jpeg", "png", "gif", "webp", "bmp", "heic", "heif", "avif", "svg", "tiff", "tif", "psd")
            put(IMAGE, "3fr", "arw", "cr2", "cr3", "dng", "nef", "orf", "raf", "raw", "rw2")
            put(VIDEO, "mp4", "mkv", "mov", "avi", "webm", "m4v", "wmv", "flv", "mpg", "mpeg", "3gp", "ts", "m3u8", "ogv")
            put(AUDIO, "mp3", "flac", "wav", "m4a", "aac", "ogg", "opus", "wma", "aiff", "ape")
            put(PDF, "pdf")
            put(DOCUMENT, "doc", "docx", "odt", "rtf", "txt", "md", "markdown", "pages", "epub", "tex")
            put(SPREADSHEET, "xls", "xlsx", "ods", "csv", "tsv", "numbers")
            put(PRESENTATION, "ppt", "pptx", "odp", "key")
            put(ARCHIVE, "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "iso", "dmg", "img")
            put(CODE, "kt", "kts", "java", "rs", "go", "py", "js", "ts", "tsx", "jsx", "c", "h", "cpp", "hpp", "cs", "swift", "rb", "php", "sh", "zsh", "fish")
            put(CODE, "json", "xml", "yaml", "yml", "toml", "ini", "cfg", "conf", "html", "htm", "css", "scss", "sql", "gradle", "lock")
            put(APK, "apk", "aab", "xapk")
        }

        fun of(file: FileObject): FileKind =
            if (file.isFolder) FOLDER else byExt[file.name.substringAfterLast('.', "").lowercase()] ?: OTHER
    }
}
