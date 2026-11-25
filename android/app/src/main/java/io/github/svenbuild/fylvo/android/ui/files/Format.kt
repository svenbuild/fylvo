package io.github.svenbuild.fylvo.android.ui.files

import java.text.DecimalFormat
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

fun formatSize(size: Long): String {
    if (size <= 0) return "0 B"
    val units = arrayOf("B", "kB", "MB", "GB", "TB")
    val group = (Math.log10(size.toDouble()) / Math.log10(1024.0)).toInt().coerceAtMost(units.size - 1)
    return DecimalFormat("#,##0.#").format(size / Math.pow(1024.0, group.toDouble())) + " " + units[group]
}

// Cached per locale: formatters are allocated per row bind otherwise, and a
// locale change rebuilds them on next access anyway.
private val formatters = java.util.concurrent.ConcurrentHashMap<String, DateTimeFormatter>()

private fun pattern(p: String) =
    formatters.getOrPut("${Locale.getDefault()}|$p") { DateTimeFormatter.ofPattern(p, Locale.getDefault()) }

/**
 * Short, Files-app-style timestamp: time of day for today, day and month
 * within the current year, otherwise with the year. Empty for unparsable input.
 */
fun formatDate(iso: String, now: Instant = Instant.now()): String {
    val instant = runCatching { OffsetDateTime.parse(iso).toInstant() }.getOrNull() ?: return ""
    val zone = ZoneId.systemDefault()
    val date = instant.atZone(zone).toLocalDate()
    val today = now.atZone(zone).toLocalDate()
    return when {
        date == today -> pattern("HH:mm").format(instant.atZone(zone))
        date.year == today.year -> pattern("d MMM").format(date)
        else -> pattern("d MMM yyyy").format(date)
    }
}

/** "7 Nov · 12.6 kB" for files, just the date for folders. */
fun subtitleFor(size: Long, updatedAt: String, isFolder: Boolean, now: Instant = Instant.now()): String {
    val date = formatDate(updatedAt, now)
    return when {
        isFolder -> date
        date.isEmpty() -> formatSize(size)
        else -> "$date · ${formatSize(size)}"
    }
}

/** Header label for date-grouped lists: Today, Yesterday, then "Sat, 10 Oct", with the year once it differs. */
fun dayGroupLabel(
    iso: String,
    now: Instant = Instant.now(),
    today: String = "Today",
    yesterday: String = "Yesterday",
    unknown: String = "Unknown date",
): String {
    val instant = runCatching { OffsetDateTime.parse(iso).toInstant() }.getOrNull() ?: return unknown
    val zone = ZoneId.systemDefault()
    val date = instant.atZone(zone).toLocalDate()
    val todayDate = now.atZone(zone).toLocalDate()
    return when {
        date == todayDate -> today
        date == todayDate.minusDays(1) -> yesterday
        date.year == todayDate.year -> pattern("EEE, d MMM").format(date)
        else -> pattern("d MMM yyyy").format(date)
    }
}

/** Stable key for grouping: the local calendar day. */
fun dayKey(iso: String): String =
    runCatching { OffsetDateTime.parse(iso).atZoneSameInstant(ZoneId.systemDefault()).toLocalDate().toString() }.getOrDefault("")
