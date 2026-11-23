package io.github.svenbuild.fylvo.android.ui.files

import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.util.Permissions

/** Fylvo: what can be done with the selected files, from the selection button. */
enum class SelectionAction(
    /** Talks to the server; disabled while the listing on screen is still loading. */
    val server: Boolean = true,
) {
    SELECT_ALL(server = false),
    DETAILS(server = false),
    SHOW_IN_FOLDER(server = false),
    SHARE,
    DOWNLOAD,
    DELETE,
    MOVE,
    COPY,
    RENAME,
    OFFLINE,
    RESTORE,
    DELETE_FOREVER,
    /** Fylvo: edit a file of an offline root on the phone (works without a connection too). */
    EDIT(server = false),
    FINISH_EDIT(server = false),
    DISCARD_EDIT(server = false),
}

/** Where the selection is made; decides together with the selected files which actions exist. */
data class SelectionContext(
    val inTrash: Boolean = false,
    /** The folder on screen comes from the copies on this phone: no server actions. */
    val offlineMode: Boolean = false,
    /** A category view that mixes folders (offers "Show in folder"). */
    val virtual: Boolean = false,
    /** One item: "Available offline" can be switched here or is covered by an offline folder. */
    val offlineShown: Boolean = false,
    /** Fylvo: one file that can be edited on the phone (FilesUiState.canEdit). */
    val editable: Boolean = false,
    /** Fylvo: one file with a working copy in use. */
    val edited: Boolean = false,
    /** Fylvo: inside a connected storage (see connectedSelectionActions). */
    val connected: Boolean = false,
)

/** [quick]: the small buttons left of the selection button, nearest first. [menu]: the card above it. */
data class SelectionActions(val quick: List<SelectionAction>, val menu: List<SelectionAction>)

/**
 * Fylvo: the rules for the selection button's actions (one place, unit-tested). [selected]: the
 * listed rows of the selection; [count]: how many paths are selected, more than [selected] while
 * a refresh shows a placeholder listing.
 */
fun selectionActions(selected: List<FileObject>, ctx: SelectionContext, count: Int = selected.size): SelectionActions {
    val file = selected.singleOrNull()?.takeIf { count == 1 }
    val one = file != null
    return when {
        count == 0 -> SelectionActions(emptyList(), emptyList())
        // Nothing of it listed yet: only what needs no row.
        selected.isEmpty() -> SelectionActions(emptyList(), listOf(SelectionAction.SELECT_ALL))
        ctx.connected -> connectedSelectionActions(selected, count)
        ctx.inTrash -> SelectionActions(
            quick = listOf(SelectionAction.RESTORE, SelectionAction.DELETE_FOREVER),
            menu = listOf(SelectionAction.SELECT_ALL),
        )
        ctx.offlineMode -> SelectionActions(
            quick = emptyList(),
            menu = listOfNotNull(SelectionAction.SELECT_ALL, SelectionAction.DETAILS.takeIf { one }) + editActions(one, ctx),
        )
        // The server locks a file while its upload is not finished; offer only what still works.
        selected.any { it.isUploading } -> SelectionActions(
            quick = emptyList(),
            menu = listOfNotNull(
                SelectionAction.SELECT_ALL,
                SelectionAction.DETAILS.takeIf { one },
                SelectionAction.SHOW_IN_FOLDER.takeIf { one && ctx.virtual },
            ),
        )
        file != null -> {
            val canModify = Permissions.canModifyFile(file)
            SelectionActions(
                quick = listOfNotNull(
                    SelectionAction.SHARE.takeIf { Permissions.canShareLink(file) },
                    SelectionAction.DOWNLOAD,
                    SelectionAction.DELETE.takeIf { canModify },
                ),
                menu = listOfNotNull(
                    SelectionAction.SELECT_ALL,
                    SelectionAction.DETAILS,
                    SelectionAction.RENAME.takeIf { canModify },
                    SelectionAction.COPY.takeIf { canModify },
                    SelectionAction.MOVE.takeIf { canModify },
                    SelectionAction.OFFLINE.takeIf { ctx.offlineShown },
                    SelectionAction.SHOW_IN_FOLDER.takeIf { ctx.virtual },
                ) + editActions(one, ctx),
            )
        }
        else -> {
            val canModify = selected.all(Permissions::canModifyFile)
            SelectionActions(
                quick = listOfNotNull(
                    SelectionAction.MOVE.takeIf { canModify },
                    SelectionAction.DOWNLOAD,
                    SelectionAction.DELETE.takeIf { canModify },
                ),
                menu = listOfNotNull(SelectionAction.SELECT_ALL, SelectionAction.COPY.takeIf { canModify }),
            )
        }
    }
}

/** Fylvo: "Bearbeiten" for one editable file; finishing and discarding for one with a working copy. */
private fun editActions(one: Boolean, ctx: SelectionContext): List<SelectionAction> = listOfNotNull(
    SelectionAction.EDIT.takeIf { one && ctx.editable },
    SelectionAction.FINISH_EDIT.takeIf { one && ctx.edited },
    SelectionAction.DISCARD_EDIT.takeIf { one && ctx.edited },
)

/**
 * Fylvo: whether [action] can be tapped now. Server actions wait while the listing is still
 * loading ([busy]); the download of several files waits while such a download runs.
 */
fun selectionActionEnabled(action: SelectionAction, count: Int, busy: Boolean, batchSaving: Boolean): Boolean = when {
    busy && action.server -> false
    action == SelectionAction.DOWNLOAD && count > 1 && batchSaving -> false
    else -> true
}

/**
 * Fylvo: the selection while a finger drags from [anchor] to [current]: what was selected
 * [before] the drag plus every file between the two in display [order]. Unfinished uploads in
 * between are skipped (the anchor itself stays). Paths no longer in [order] fall out. Null when
 * the anchor is gone from the list, which ends the drag.
 */
fun dragSelection(order: List<FileObject>, before: Set<String>, anchor: String, current: String): Set<String>? {
    val from = order.indexOfFirst { it.path == anchor }
    if (from < 0) return null
    val to = order.indexOfFirst { it.path == current }.takeIf { it >= 0 } ?: from
    val range = order.subList(minOf(from, to), maxOf(from, to) + 1).filter { !it.isUploading }.map { it.path }
    val present = order.mapTo(HashSet()) { it.path }
    return before.filterTo(LinkedHashSet()) { it in present } + range + anchor
}
