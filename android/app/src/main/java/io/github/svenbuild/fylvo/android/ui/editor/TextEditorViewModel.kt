package io.github.svenbuild.fylvo.android.ui.editor

import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.api.SessionTag
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.TextRepository
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.UiText
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Fylvo 1.6: what the editor opens: an existing file, or a new one that exists only after saving. */
sealed interface EditorTarget {
    val name: String
    data class Existing(val file: FileObject) : EditorTarget { override val name get() = file.name }
    data class New(val parentUri: String, override val name: String) : EditorTarget
}

/**
 * Fylvo 1.6: the text editor's state (design/android-parity-plan.md, 4). The text lives here, so it
 * survives turning the phone, and never in saved screen state (it can be large).
 *
 * Every opening is one [Edit]: loads and saves carry it, and an answer for an edit that is no
 * longer the open one (closed, another file opened) changes nothing. All requests belong to the
 * session the edit began in.
 */
class TextEditorViewModel(
    private val texts: TextRepository,
    private val session: SessionManager,
    /** A working copy of the offline editing is open for this file (then the app does not save). */
    private val hasWorkingCopy: (FileObject) -> Boolean,
) : ViewModel() {

    /** A question after a failed save. */
    enum class Conflict { CHANGED, GONE, NAME_TAKEN }

    data class State(
        val target: EditorTarget? = null,
        val loading: Boolean = true,
        /** Loading failed for good (no text, too large, gone): the editor shows only this. */
        val loadError: UiText? = null,
        val saving: Boolean = false,
        /** Text of the last successful load or save; changes are compared against it. */
        val saved: String = "",
        /** Nothing is on the server yet (a new file before its first save). */
        val unsavedNew: Boolean = false,
        val conflict: Conflict? = null,
        val message: UiText? = null,
        /** True once something was saved (the list behind is refreshed on close). */
        val wroteSomething: Boolean = false,
    )

    /** One opened file: what was loaded or saved last, and the session it belongs to. */
    private class Edit(val tag: SessionTag) {
        var uri = ""
        var fileId = ""
        var entity = ""
        var bom = false
        var file: FileObject? = null
    }

    val text = TextFieldState()
    private val _state = MutableStateFlow(State(loading = false))
    val state: StateFlow<State> = _state.asStateFlow()

    private var edit: Edit? = null
    /** What the screen asked to open (the editor may move on to a copy it saved). */
    private var opened: EditorTarget? = null
    private var job: Job? = null

    val dirty: Boolean get() = text.text.toString() != _state.value.saved || _state.value.unsavedNew

    fun open(target: EditorTarget) {
        if (opened == target) return
        job?.cancel()
        opened = target
        val e = Edit(session.sessionTag())
        edit = e
        text.setTextAndPlaceCursorAtEnd("")
        when (target) {
            is EditorTarget.New -> _state.value = State(target = target, loading = false, unsavedNew = true)
            is EditorTarget.Existing -> {
                _state.value = State(target = target, loading = true)
                job = viewModelScope.launch {
                    try {
                        val loaded = texts.load(target.file, e.tag)
                        if (edit !== e) return@launch
                        e.file = target.file
                        e.uri = target.file.path
                        e.fileId = loaded.fileId
                        e.entity = loaded.entity
                        e.bom = loaded.bom
                        text.setTextAndPlaceCursorAtEnd(loaded.text)
                        _state.update { it.copy(loading = false, saved = loaded.text) }
                    } catch (ex: kotlinx.coroutines.CancellationException) {
                        throw ex
                    } catch (ex: Exception) {
                        if (edit !== e) return@launch
                        val error = when (ex) {
                            is TextRepository.NotText -> UiText.Res(R.string.editor_not_text)
                            is TextRepository.TooLarge -> UiText.Res(R.string.editor_too_large)
                            else -> userError(ex)
                        }
                        _state.update { it.copy(loading = false, loadError = error) }
                    }
                }
            }
        }
    }

    fun save() = run { e, current, content ->
        val made = e.file
        when {
            current is EditorTarget.New && made == null -> texts.createNew(current.parentUri, current.name, content, tag = e.tag)
            made == null -> null
            hasWorkingCopy(made) -> {
                _state.update { it.copy(message = UiText.Res(R.string.editor_working_copy)) }
                null
            }
            else -> texts.save(e.uri, e.fileId, content, e.bom, e.entity, e.tag)
        }
    }

    /** After [Conflict.CHANGED]: replace the newer version (only while it is still the same file). */
    fun overwrite() = run { e, _, content -> texts.overwrite(e.uri, e.fileId, content, e.bom, e.tag) }

    /** After a conflict: the text as a new file next to the old one; the editor then edits the copy. */
    fun saveAsCopy() = run { e, current, content ->
        val parent = (current as? EditorTarget.New)?.parentUri ?: CrUri.parent(e.uri)
        texts.saveCopy(parent, current.name, content, e.bom, tag = e.tag)
    }

    /** A new file whose name was taken: try again with [name]. */
    fun rename(name: String) {
        val current = _state.value.target as? EditorTarget.New ?: return
        _state.update { it.copy(target = current.copy(name = name), conflict = null) }
        save()
    }

    /** The editor closes: nothing of the text stays, and no answer of this edit arrives any more. */
    fun close() {
        job?.cancel()
        job = null
        edit = null
        opened = null
        text.setTextAndPlaceCursorAtEnd("")
        _state.value = State(target = null, loading = false)
    }

    fun dismissConflict() = _state.update { it.copy(conflict = null) }
    fun messageShown() = _state.update { it.copy(message = null) }

    /**
     * Runs one save step of the open edit. [block] answers the saved file (null: nothing was
     * saved). An answer that arrives after the editor closed or moved on is dropped.
     */
    private fun run(block: suspend (Edit, EditorTarget, String) -> FileObject?) {
        val e = edit ?: return
        val current = _state.value.target ?: return
        if (_state.value.saving || _state.value.loading) return
        val content = text.text.toString()
        // Connected storages take no empty file.
        if (content.isEmpty() && Connected.isConnected(e.uri)) {
            _state.update { it.copy(message = UiText.Res(R.string.editor_empty_connected)) }
            return
        }
        _state.update { it.copy(saving = true, conflict = null) }
        job = viewModelScope.launch {
            try {
                if (!session.isCurrent(e.tag)) return@launch
                val saved = block(e, current, content) ?: return@launch
                if (edit !== e) return@launch
                e.file = saved
                e.uri = saved.path
                e.fileId = saved.id
                e.entity = saved.primaryEntity
                _state.update {
                    it.copy(
                        saved = content, conflict = null, wroteSomething = true, unsavedNew = false,
                        // From now on the editor edits the saved file (also a copy it just made).
                        target = EditorTarget.Existing(saved),
                        message = UiText.Res(R.string.editor_saved),
                    )
                }
            } catch (ex: kotlinx.coroutines.CancellationException) {
                throw ex
            } catch (ex: ApiException) {
                if (edit !== e) return@launch
                val conflict = when {
                    ex.code == TextRepository.CODE_STALE -> Conflict.CHANGED
                    ex.code == TextRepository.CODE_GONE -> Conflict.GONE
                    current is EditorTarget.New && e.file == null &&
                        (ex.code == ApiException.CODE_OBJECT_EXISTS || ex.code == TextRepository.CODE_NAME_CASE_TAKEN) -> Conflict.NAME_TAKEN
                    else -> null
                }
                if (conflict != null) _state.update { it.copy(conflict = conflict) }
                else _state.update { it.copy(message = if (ex.code == Connected.CODE_OUTCOME_UNKNOWN) UiText.Res(R.string.editor_outcome_unknown) else userError(ex)) }
            } catch (ex: Exception) {
                if (edit === e) _state.update { it.copy(message = userError(ex)) }
            } finally {
                if (edit === e) _state.update { it.copy(saving = false) }
            }
        }
    }
}
