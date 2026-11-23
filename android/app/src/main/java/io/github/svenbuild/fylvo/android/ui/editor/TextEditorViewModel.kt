package io.github.svenbuild.fylvo.android.ui.editor

import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.FileObject
import io.github.svenbuild.fylvo.android.data.ApiException
import io.github.svenbuild.fylvo.android.data.SessionManager
import io.github.svenbuild.fylvo.android.data.TextRepository
import io.github.svenbuild.fylvo.android.util.Connected
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.util.UiText
import io.github.svenbuild.fylvo.android.util.userError
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
 * survives turning the phone, and never in saved screen state (it can be large). All work belongs
 * to the session it began in.
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
        val conflict: Conflict? = null,
        val message: UiText? = null,
        /** True once something was saved (the list behind is refreshed on close). */
        val wroteSomething: Boolean = false,
    )

    val text = TextFieldState()
    private val _state = MutableStateFlow(State())
    val state: StateFlow<State> = _state.asStateFlow()

    private var tag = session.sessionTag()
    /** The file being edited: address, id, version and BOM of the loaded text. */
    private var uri = ""
    private var fileId = ""
    private var entity = ""
    private var bom = false
    private var file: FileObject? = null
    /** What the screen asked to open (the editor may move on to a copy it saved). */
    private var opened: EditorTarget? = null

    val dirty: Boolean get() = text.text.toString() != _state.value.saved || _state.value.target is EditorTarget.New && file == null

    fun open(target: EditorTarget) {
        if (opened == target) return
        opened = target
        tag = session.sessionTag()
        _state.value = State(target = target)
        when (target) {
            is EditorTarget.New -> {
                text.setTextAndPlaceCursorAtEnd("")
                _state.update { it.copy(loading = false) }
            }
            is EditorTarget.Existing -> viewModelScope.launch {
                try {
                    val loaded = texts.load(target.file)
                    if (!session.isCurrent(tag)) return@launch
                    file = target.file
                    uri = target.file.path
                    fileId = loaded.fileId
                    entity = loaded.entity
                    bom = loaded.bom
                    text.setTextAndPlaceCursorAtEnd(loaded.text)
                    _state.update { it.copy(loading = false, saved = loaded.text) }
                } catch (e: TextRepository.NotText) {
                    _state.update { it.copy(loading = false, loadError = UiText.Res(R.string.editor_not_text)) }
                } catch (e: TextRepository.TooLarge) {
                    _state.update { it.copy(loading = false, loadError = UiText.Res(R.string.editor_too_large)) }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    _state.update { it.copy(loading = false, loadError = userError(e)) }
                }
            }
        }
    }

    fun save() = run { current, content ->
        when (current) {
            is EditorTarget.New -> {
                val made = file
                if (made == null) {
                    val created = texts.createNew(current.parentUri, current.name, content)
                    took(created, content)
                } else {
                    took(texts.save(made.path, fileId, content, bom, entity), content)
                }
            }
            is EditorTarget.Existing -> {
                val f = file ?: return@run
                if (hasWorkingCopy(f)) {
                    _state.update { it.copy(message = UiText.Res(R.string.editor_working_copy)) }
                    return@run
                }
                took(texts.save(uri, fileId, content, bom, entity), content)
            }
        }
    }

    /** After [Conflict.CHANGED]: replace the newer version (only while it is still the same file). */
    fun overwrite() = run { _, content -> took(texts.overwrite(uri, fileId, content, bom), content) }

    /** After a conflict: the text as a new file next to the old one; the editor then edits the copy. */
    fun saveAsCopy() = run { current, content ->
        val parent = (current as? EditorTarget.New)?.parentUri ?: CrUri.parent(uri)
        took(texts.saveCopy(parent, current.name, content, bom), content)
    }

    /** A new file whose name was taken: try again with [name]. */
    fun rename(name: String) {
        val current = _state.value.target as? EditorTarget.New ?: return
        _state.update { it.copy(target = current.copy(name = name), conflict = null) }
        save()
    }

    /** The editor closes: nothing of the text stays. */
    fun close() {
        opened = null
        file = null
        uri = ""
        fileId = ""
        entity = ""
        bom = false
        text.setTextAndPlaceCursorAtEnd("")
        _state.value = State(target = null, loading = false)
    }

    fun dismissConflict() = _state.update { it.copy(conflict = null) }
    fun messageShown() = _state.update { it.copy(message = null) }

    private fun took(saved: FileObject, content: String) {
        file = saved
        uri = saved.path
        fileId = saved.id
        entity = saved.primaryEntity
        _state.update {
            it.copy(
                saved = content, conflict = null, wroteSomething = true,
                // From now on the editor edits the saved file (also a copy it just made).
                target = EditorTarget.Existing(saved),
                message = UiText.Res(R.string.editor_saved),
            )
        }
    }

    private fun run(block: suspend (EditorTarget, String) -> Unit) {
        val current = _state.value.target ?: return
        if (_state.value.saving) return
        val content = text.text.toString()
        // Connected storages take no empty file.
        if (content.isEmpty() && Connected.isConnected((current as? EditorTarget.Existing)?.file?.path ?: "")) {
            _state.update { it.copy(message = UiText.Res(R.string.editor_empty_connected)) }
            return
        }
        _state.update { it.copy(saving = true, conflict = null) }
        viewModelScope.launch {
            try {
                if (!session.isCurrent(tag)) return@launch
                block(current, content)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: ApiException) {
                val conflict = when {
                    e.code == TextRepository.CODE_STALE -> Conflict.CHANGED
                    e.code == TextRepository.CODE_GONE -> Conflict.GONE
                    current is EditorTarget.New && file == null &&
                        (e.code == ApiException.CODE_OBJECT_EXISTS || e.code == TextRepository.CODE_NAME_CASE_TAKEN) -> Conflict.NAME_TAKEN
                    else -> null
                }
                if (conflict != null) _state.update { it.copy(conflict = conflict) }
                else _state.update { it.copy(message = if (e.code == Connected.CODE_OUTCOME_UNKNOWN) UiText.Res(R.string.editor_outcome_unknown) else userError(e)) }
            } catch (e: Exception) {
                _state.update { it.copy(message = userError(e)) }
            } finally {
                _state.update { it.copy(saving = false) }
            }
        }
    }
}
