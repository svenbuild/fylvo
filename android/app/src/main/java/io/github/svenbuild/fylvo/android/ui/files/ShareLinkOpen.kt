package io.github.svenbuild.fylvo.android.ui.files

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import io.github.svenbuild.fylvo.android.R

/** Fylvo 1.6: a share link waits for its password; [wrong] after a password that did not fit. */
data class LinkPasswordAsk(val id: String, val name: String, val wrong: Boolean)

/** Fylvo 1.6: asks for a share's password; the text lives only in this dialog (never saved). */
@Composable
fun LinkPasswordDialog(ask: LinkPasswordAsk, busy: Boolean, onOpen: (String) -> Unit, onDismiss: () -> Unit) {
    var text by remember(ask) { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(ask.name.ifEmpty { stringResource(R.string.open_share_link) }) },
        text = {
            Column {
                Text(stringResource(if (ask.wrong) R.string.link_password_wrong else R.string.link_password_needed))
                OutlinedTextField(
                    value = text,
                    onValueChange = { v -> text = v.filter { it.isLetterOrDigit() && it.code < 128 }.take(32) },
                    label = { Text(stringResource(R.string.password)) },
                    singleLine = true,
                    isError = ask.wrong,
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    textStyle = MaterialTheme.typography.bodyLarge,
                )
            }
        },
        confirmButton = {
            TextButton(enabled = !busy && text.isNotEmpty(), onClick = { onOpen(text) }) { Text(stringResource(R.string.open)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}
