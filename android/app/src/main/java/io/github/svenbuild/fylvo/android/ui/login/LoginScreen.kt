package io.github.svenbuild.fylvo.android.ui.login

import androidx.compose.ui.res.stringResource
import io.github.svenbuild.fylvo.android.util.asString

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material.icons.outlined.Email
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Pin
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.R

/**
 * Sign-in in the same visual language as the file browser: soft filled
 * fields with no outlines, rounded containers, dynamic colour. The 2FA
 * step slides in over the password form instead of replacing it abruptly.
 */
@Composable
fun LoginScreen(viewModel: LoginViewModel, onLoggedIn: () -> Unit) {
    val state by viewModel.state.collectAsState()

    LaunchedEffect(state.loggedIn) { if (state.loggedIn) onLoggedIn() }

    Surface(Modifier.fillMaxSize()) {
        Column(
            Modifier
                .fillMaxSize()
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp, vertical = 32.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Image(
                painter = painterResource(R.drawable.ic_launcher_foreground),
                contentDescription = null,
                // Fylvo: on the dark tile of the app icon, so the white part of the mark is seen in
                // the light theme too.
                modifier = Modifier
                    .size(96.dp)
                    .clip(RoundedCornerShape(28))
                    .background(androidx.compose.ui.res.colorResource(R.color.ic_launcher_background)),
            )
            Text(stringResource(R.string.app_name), style = MaterialTheme.typography.headlineMedium, modifier = Modifier.padding(top = 8.dp))

            AnimatedContent(
                targetState = state.step(),
                transitionSpec = {
                    if (targetState.ordinal > initialState.ordinal) {
                        (slideInHorizontally(tween(260)) { it / 3 } + fadeIn(tween(200))) togetherWith
                            (slideOutHorizontally(tween(200)) { -it / 3 } + fadeOut(tween(140)))
                    } else {
                        (slideInHorizontally(tween(260)) { -it / 3 } + fadeIn(tween(200))) togetherWith
                            (slideOutHorizontally(tween(200)) { it / 3 } + fadeOut(tween(140)))
                    }
                },
                label = "loginStep",
                modifier = Modifier.widthIn(max = 440.dp),
            ) { step ->
                when (step) {
                    LoginStep.Password -> PasswordStep(state, viewModel)
                    LoginStep.TwoFactor -> TwoFactorStep(state, viewModel)
                    LoginStep.NewPassword -> NewPasswordStep(state, viewModel)
                }
            }
        }
    }
}

@Composable
private fun PasswordStep(state: LoginUiState, viewModel: LoginViewModel) {
    val context = LocalContext.current
    val focus = LocalFocusManager.current
    var showPassword by remember { mutableStateOf(false) }

    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            stringResource(R.string.sign_in_to_server),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 4.dp, bottom = 28.dp),
        )

        FormCard {
            SoftField(
                value = state.serverUrl,
                onValueChange = viewModel::onServerUrlChange,
                label = stringResource(R.string.server),
                placeholder = "drive.example.com",
                icon = Icons.Outlined.Cloud,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next),
            )
            SoftField(
                value = state.email,
                onValueChange = viewModel::onEmailChange,
                label = stringResource(R.string.email),
                icon = Icons.Outlined.Email,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next),
            )
            SoftField(
                value = state.password,
                onValueChange = viewModel::onPasswordChange,
                label = stringResource(R.string.password),
                icon = Icons.Outlined.Lock,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { focus.clearFocus(); viewModel.login() }),
                visualTransformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation(),
                trailing = {
                    IconButton(onClick = { showPassword = !showPassword }) {
                        Icon(
                            if (showPassword) Icons.Outlined.VisibilityOff else Icons.Outlined.Visibility,
                            contentDescription = stringResource(if (showPassword) R.string.hide_password else R.string.show_password),
                        )
                    }
                },
            )
        }

        ErrorNote(state.error?.asString())

        Spacer(Modifier.height(20.dp))
        PrimaryButton(text = stringResource(R.string.sign_in), loading = state.loading, onClick = { focus.clearFocus(); viewModel.login() })
    }
}

@Composable
private fun TwoFactorStep(state: LoginUiState, viewModel: LoginViewModel) {
    val focusRequester = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focusRequester.requestFocus() } }

    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            stringResource(R.string.two_step_verification),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(top = 4.dp),
        )
        Text(
            stringResource(R.string.enter_code),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 4.dp, bottom = 28.dp),
        )

        FormCard {
            SoftField(
                value = state.otp,
                onValueChange = viewModel::onOtpChange,
                label = stringResource(R.string.verification_code),
                icon = Icons.Outlined.Pin,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword, imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { viewModel.verify2FA() }),
                modifier = Modifier.focusRequester(focusRequester),
            )
        }

        ErrorNote(state.error?.asString())

        Spacer(Modifier.height(20.dp))
        PrimaryButton(text = stringResource(R.string.verify), loading = state.loading, enabled = state.otp.length == 6, onClick = viewModel::verify2FA)
        Spacer(Modifier.height(4.dp))
        TextButton(onClick = viewModel::cancelTwoFactor, enabled = !state.loading) { Text(stringResource(R.string.use_different_account)) }
    }
}

/** Fylvo: the account must set its own password before it gets in. */
@Composable
private fun NewPasswordStep(state: LoginUiState, viewModel: LoginViewModel) {
    val focus = LocalFocusManager.current
    val focusRequester = remember { FocusRequester() }
    var showPassword by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { runCatching { focusRequester.requestFocus() } }
    val transformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation()

    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            stringResource(R.string.new_password_title),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(top = 4.dp),
        )
        Text(
            stringResource(R.string.new_password_hint),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 4.dp, bottom = 28.dp),
        )

        FormCard {
            SoftField(
                value = state.newPassword,
                onValueChange = viewModel::onNewPasswordChange,
                label = stringResource(R.string.new_password),
                icon = Icons.Outlined.Lock,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Next),
                visualTransformation = transformation,
                modifier = Modifier.focusRequester(focusRequester),
                trailing = {
                    IconButton(onClick = { showPassword = !showPassword }) {
                        Icon(
                            if (showPassword) Icons.Outlined.VisibilityOff else Icons.Outlined.Visibility,
                            contentDescription = stringResource(if (showPassword) R.string.hide_password else R.string.show_password),
                        )
                    }
                },
            )
            SoftField(
                value = state.newPasswordRepeat,
                onValueChange = viewModel::onNewPasswordRepeatChange,
                label = stringResource(R.string.new_password_repeat),
                icon = Icons.Outlined.Lock,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { focus.clearFocus(); viewModel.setNewPassword() }),
                visualTransformation = transformation,
            )
        }

        ErrorNote(state.error?.asString())

        Spacer(Modifier.height(20.dp))
        PrimaryButton(
            text = stringResource(R.string.new_password_save),
            loading = state.loading,
            enabled = state.newPassword.isNotEmpty() && state.newPasswordRepeat.isNotEmpty(),
            onClick = { focus.clearFocus(); viewModel.setNewPassword() },
        )
        Spacer(Modifier.height(4.dp))
        TextButton(onClick = viewModel::cancelPasswordChange, enabled = !state.loading) { Text(stringResource(R.string.use_different_account)) }
    }
}

// ---- building blocks ----------------------------------------------------

/** Rounded container that groups the fields, like a settings card. */
@Composable
private fun FormCard(content: @Composable () -> Unit) {
    Surface(
        shape = RoundedCornerShape(12.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { content() }
    }
}

/** Filled text field with no indicator line: a soft shape, not an outline. */
@Composable
private fun SoftField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    icon: ImageVector,
    keyboardOptions: KeyboardOptions,
    modifier: Modifier = Modifier,
    placeholder: String? = null,
    keyboardActions: KeyboardActions = KeyboardActions.Default,
    visualTransformation: VisualTransformation = VisualTransformation.None,
    trailing: (@Composable () -> Unit)? = null,
) {
    TextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label) },
        placeholder = placeholder?.let { { Text(it) } },
        leadingIcon = { Icon(icon, contentDescription = null) },
        trailingIcon = trailing,
        singleLine = true,
        keyboardOptions = keyboardOptions,
        keyboardActions = keyboardActions,
        visualTransformation = visualTransformation,
        shape = RoundedCornerShape(12.dp),
        colors = TextFieldDefaults.colors(
            // Unfocused fields sit flush with the card (quieter, like the web's
            // filled inputs on paper); focus lifts them to the highest tier.
            focusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHighest,
            unfocusedContainerColor = Color.Transparent,
            focusedIndicatorColor = Color.Transparent,
            unfocusedIndicatorColor = Color.Transparent,
            disabledIndicatorColor = Color.Transparent,
            errorIndicatorColor = Color.Transparent,
        ),
        modifier = modifier.fillMaxWidth(),
    )
}

@Composable
private fun PrimaryButton(text: String, loading: Boolean, onClick: () -> Unit, enabled: Boolean = true) {
    Button(
        onClick = onClick,
        enabled = enabled && !loading,
        shape = RoundedCornerShape(12.dp),
        modifier = Modifier
            .fillMaxWidth()
            .height(52.dp),
    ) {
        if (loading) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) else Text(text)
    }
}

/** Error in a soft error-coloured chip rather than bare red text. */
@Composable
private fun ErrorNote(message: String?) {
    AnimatedVisibility(visible = message != null, enter = fadeIn(tween(180)), exit = fadeOut(tween(120))) {
        Surface(
            shape = RoundedCornerShape(12.dp),
            color = MaterialTheme.colorScheme.errorContainer,
            modifier = Modifier
                .fillMaxWidth()
                .padding(top = 12.dp),
        ) {
            Row(Modifier.padding(horizontal = 14.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.ErrorOutline, contentDescription = null, tint = MaterialTheme.colorScheme.onErrorContainer, modifier = Modifier.size(18.dp))
                Text(
                    message.orEmpty(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onErrorContainer,
                    modifier = Modifier.padding(start = 10.dp),
                )
            }
        }
    }
}
