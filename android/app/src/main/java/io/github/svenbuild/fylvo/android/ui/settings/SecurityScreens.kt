package io.github.svenbuild.fylvo.android.ui.settings

import android.app.Activity
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Apps
import androidx.compose.material.icons.outlined.Badge
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.Key
import androidx.compose.material.icons.outlined.Password
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.api.ActivityEntry
import io.github.svenbuild.fylvo.android.api.UserSettings
import io.github.svenbuild.fylvo.android.data.SecurityRepository
import io.github.svenbuild.fylvo.android.ui.files.ActivityRow
import io.github.svenbuild.fylvo.android.ui.files.ActivityText
import io.github.svenbuild.fylvo.android.ui.files.NameDialog
import io.github.svenbuild.fylvo.android.ui.files.PlacePage
import io.github.svenbuild.fylvo.android.util.userError
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream

/**
 * Fylvo 1.6: the account's picture. Only a picture uploaded to the server is loaded ("file");
 * otherwise the initial, so the app never asks Gravatar (design/android-parity-plan.md, 9).
 */
@Composable
fun AccountAvatar(size: androidx.compose.ui.unit.Dp, version: Int = 0) {
    val app = LocalContext.current.applicationContext as FylvoApp
    val me = app.account.state.collectAsState().value.me
    val server by app.sessionManager.serverUrl.collectAsState(initial = "")
    val modifier = Modifier.size(size).clip(CircleShape)
    if (me != null && me.avatar == "file" && server.isNotEmpty()) {
        // Its own loader that follows no redirect: a changed picture setting never leads to Gravatar.
        val loader = remember {
            coil.ImageLoader.Builder(app)
                .okHttpClient {
                    okhttp3.OkHttpClient.Builder()
                        .followRedirects(false)
                        .addInterceptor(io.github.svenbuild.fylvo.android.api.ApiClient.userAgent)
                        .build()
                }
                .build()
        }
        AsyncImage(
            imageLoader = loader,
            model = "${server.trimEnd('/')}/api/v4/user/avatar/${me.id}?nocache=true&v=$version",
            contentDescription = null,
            contentScale = ContentScale.Crop,
            modifier = modifier,
        )
    } else {
        Box(modifier.background(MaterialTheme.colorScheme.primaryContainer), contentAlignment = Alignment.Center) {
            Text(
                (me?.nickname?.firstOrNull() ?: me?.email?.firstOrNull() ?: '?').uppercase(),
                style = if (size > 48.dp) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleMedium,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
            )
        }
    }
}

/** Fylvo 1.6: Account › profile: name and picture. */
@Composable
fun ProfilePage(onBack: () -> Unit, bottomBar: @Composable () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val me = app.account.state.collectAsState().value.me
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var renaming by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var version by remember { mutableStateOf(0) }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        if (uri == null) return@rememberLauncherForActivityResult
        busy = true
        scope.launch {
            runCatching {
                val jpeg = withContext(Dispatchers.IO) { squareJpeg(context, uri) } ?: throw IllegalArgumentException("No image")
                app.security.uploadAvatar(jpeg)
            }.onSuccess {
                version++
                snackbar.showSnackbar(context.getString(R.string.profile_picture_saved))
            }.onFailure { snackbar.showSnackbar(userError(it, fallback = R.string.profile_picture_failed).resolve(context)) }
            busy = false
        }
    }
    LaunchedEffect(Unit) { runCatching { app.account.refresh() } }

    PlacePage(stringResource(R.string.profile), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                Column(Modifier.fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    AccountAvatar(96.dp, version)
                    Text(me?.nickname.orEmpty(), style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(top = 12.dp))
                    Text(me?.email.orEmpty(), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                ListItem(
                    modifier = Modifier.clickable(enabled = me != null) { renaming = true },
                    headlineContent = { Text(stringResource(R.string.profile_name)) },
                    supportingContent = { Text(me?.nickname.orEmpty()) },
                    leadingContent = { Icon(Icons.Outlined.Badge, contentDescription = null) },
                )
                ListItem(
                    modifier = Modifier.clickable(enabled = !busy) {
                        pick.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                    },
                    headlineContent = { Text(stringResource(R.string.profile_picture_change)) },
                    supportingContent = { Text(stringResource(R.string.profile_picture_hint)) },
                    leadingContent = { Icon(Icons.Outlined.Image, contentDescription = null) },
                    trailingContent = { if (busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) },
                )
            }
            SnackbarHost(snackbar, Modifier.align(Alignment.BottomCenter).padding(bottom = 96.dp))
        }
    }

    if (renaming) {
        NameDialog(
            title = stringResource(R.string.profile_name),
            label = stringResource(R.string.profile_name),
            confirm = stringResource(R.string.save),
            initial = me?.nickname.orEmpty(),
            onConfirm = { name ->
                renaming = false
                val trimmed = name.trim()
                if (trimmed.isNotEmpty()) scope.launch {
                    runCatching { app.security.setName(trimmed.take(255)) }
                        .onFailure { snackbar.showSnackbar(userError(it).resolve(context)) }
                }
            },
            onDismiss = { renaming = false },
        )
    }
}

/** A picked image as a square JPEG of at most 400 px (the server stores 200 px). */
private fun squareJpeg(context: android.content.Context, uri: Uri): ByteArray? {
    val resolver = context.contentResolver
    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, bounds) }
    if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
    var sample = 1
    while (minOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= 400) sample *= 2
    val decoded = resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample }) } ?: return null
    val side = minOf(decoded.width, decoded.height)
    val square = Bitmap.createBitmap(decoded, (decoded.width - side) / 2, (decoded.height - side) / 2, side, side)
    val scaled = if (side > 400) Bitmap.createScaledBitmap(square, 400, 400, true) else square
    return ByteArrayOutputStream().use { out ->
        scaled.compress(Bitmap.CompressFormat.JPEG, 90, out)
        out.toByteArray()
    }
}

/** Fylvo 1.6: Account › security: password, two-factor sign-in, apps, recent sign-ins. */
@Composable
fun SecurityPage(onBack: () -> Unit, bottomBar: @Composable () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var settings by remember { mutableStateOf<UserSettings?>(null) }
    var failed by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }
    val signIns = remember { mutableStateListOf<ActivityEntry>() }
    var next by remember { mutableStateOf(0) }
    var changePassword by remember { mutableStateOf(false) }
    var setup2fa by remember { mutableStateOf<String?>(null) }
    var disable2fa by remember { mutableStateOf(false) }
    var revoke by remember { mutableStateOf<io.github.svenbuild.fylvo.android.api.OauthGrant?>(null) }
    /** A 2FA secret is being fetched (the server keeps only the last one: one request at a time). */
    var fetching by remember { mutableStateOf(false) }
    val message: (String) -> Unit = { scope.launch { snackbar.showSnackbar(it) } }

    LaunchedEffect(reload) {
        runCatching { app.security.settings() }.onSuccess { settings = it; failed = false }.onFailure { failed = true }
        runCatching { app.security.signIns() }.onSuccess { signIns.clear(); signIns.addAll(it.entries); next = it.next }
    }

    PlacePage(stringResource(R.string.security), onBack, bottomBar) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            val s = settings
            if (s == null) {
                if (failed) Text(stringResource(R.string.security_load_failed), modifier = Modifier.align(Alignment.Center).padding(24.dp))
                else CircularProgressIndicator(Modifier.align(Alignment.Center))
            } else Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                if (!s.passwordless) ListItem(
                    modifier = Modifier.clickable { changePassword = true },
                    headlineContent = { Text(stringResource(R.string.password_change)) },
                    leadingContent = { Icon(Icons.Outlined.Password, contentDescription = null) },
                )
                ListItem(
                    modifier = Modifier.clickable {
                        if (s.twoFaEnabled) disable2fa = true
                        else if (!fetching) {
                            fetching = true
                            scope.launch {
                                runCatching { app.security.twoFaSecret() }
                                    .onSuccess { setup2fa = it }
                                    .onFailure { message(userError(it).resolve(context)) }
                                fetching = false
                            }
                        }
                    },
                    headlineContent = { Text(stringResource(R.string.two_fa)) },
                    supportingContent = { Text(stringResource(if (s.twoFaEnabled) R.string.two_fa_on else R.string.two_fa_off)) },
                    leadingContent = { Icon(Icons.Outlined.Key, contentDescription = null) },
                    trailingContent = { Text(stringResource(if (s.twoFaEnabled) R.string.two_fa_turn_off else R.string.two_fa_turn_on), color = MaterialTheme.colorScheme.primary) },
                )
                if (s.oauthGrants.isNotEmpty()) {
                    HorizontalDivider(Modifier.padding(vertical = 8.dp))
                    SectionTitle(stringResource(R.string.app_access))
                    s.oauthGrants.forEach { grant ->
                        ListItem(
                            headlineContent = { Text(grant.clientName.ifEmpty { grant.clientId }) },
                            supportingContent = {
                                Text(grant.lastUsedAt?.let { stringResource(R.string.last_used, ActivityText.time(it)) } ?: stringResource(R.string.never_used))
                            },
                            leadingContent = { Icon(Icons.Outlined.Apps, contentDescription = null) },
                            trailingContent = { TextButton(onClick = { revoke = grant }) { Text(stringResource(R.string.revoke), color = MaterialTheme.colorScheme.error) } },
                        )
                    }
                }
                HorizontalDivider(Modifier.padding(vertical = 8.dp))
                SectionTitle(stringResource(R.string.recent_sign_ins))
                signIns.forEach { ActivityRow(it, withDevice = true) }
                if (next > 0) TextButton(onClick = {
                    scope.launch {
                        runCatching { app.security.signIns(next) }.onSuccess { signIns.addAll(it.entries); next = it.next }
                    }
                }, modifier = Modifier.padding(horizontal = 12.dp)) { Text(stringResource(R.string.load_more)) }
                Spacer(Modifier.height(96.dp))
            }
            SnackbarHost(snackbar, Modifier.align(Alignment.BottomCenter).padding(bottom = 96.dp))
        }
    }

    if (changePassword) PasswordDialog(
        onDone = { changePassword = false; message(context.getString(R.string.password_changed)); reload++ },
        onDismiss = { changePassword = false },
    )
    setup2fa?.let { secret ->
        TwoFaSetup(
            secret = secret,
            email = app.account.state.value.me?.email.orEmpty(),
            onDone = { setup2fa = null; message(context.getString(R.string.two_fa_enabled)); reload++ },
            onCancel = { setup2fa = null },
        )
    }
    if (disable2fa) CodeDialog(
        title = stringResource(R.string.two_fa_turn_off),
        text = stringResource(R.string.two_fa_off_text),
        confirm = stringResource(R.string.two_fa_turn_off),
        send = { code -> app.security.setTwoFa(false, code) },
        onDone = { disable2fa = false; message(context.getString(R.string.two_fa_disabled)); reload++ },
        onDismiss = { disable2fa = false },
    )
    revoke?.let { grant ->
        AlertDialog(
            onDismissRequest = { revoke = null },
            title = { Text(stringResource(R.string.revoke_title, grant.clientName.ifEmpty { grant.clientId })) },
            text = { Text(stringResource(R.string.revoke_text)) },
            confirmButton = {
                TextButton(onClick = {
                    revoke = null
                    scope.launch {
                        runCatching { app.security.revokeGrant(grant.clientId) }
                            .onSuccess { reload++ }
                            .onFailure { message(userError(it).resolve(context)) }
                    }
                }) { Text(stringResource(R.string.revoke), color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { revoke = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
}

/** Current password, new one twice; the texts live only in this dialog. */
@Composable
private fun PasswordDialog(onDone: () -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    var current by remember { mutableStateOf("") }
    var new by remember { mutableStateOf("") }
    var repeat by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val ok = current.length >= 4 && new.length in 6..128 && new == repeat
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(stringResource(R.string.password_change)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                SecretField(current, { current = it }, stringResource(R.string.password_current))
                SecretField(new, { new = it }, stringResource(R.string.password_new), stringResource(R.string.password_rule))
                SecretField(repeat, { repeat = it }, stringResource(R.string.password_repeat),
                    if (repeat.isNotEmpty() && repeat != new) stringResource(R.string.password_mismatch) else null)
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
            }
        },
        confirmButton = {
            TextButton(enabled = ok && !busy, onClick = {
                busy = true
                scope.launch {
                    runCatching { app.security.changePassword(current, new) }
                        .onSuccess { onDone() }
                        .onFailure { e ->
                            error = if ((e as? io.github.svenbuild.fylvo.android.data.ApiException)?.code == 40069) context.getString(R.string.password_current_wrong)
                            else userError(e).resolve(context)
                        }
                    busy = false
                }
            }) { Text(stringResource(R.string.save)) }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun SecretField(value: String, onChange: (String) -> Unit, label: String, hint: String? = null) {
    OutlinedTextField(
        value = value,
        onValueChange = { onChange(it.take(128)) },
        label = { Text(label) },
        singleLine = true,
        isError = hint != null && hint == stringResource(R.string.password_mismatch),
        supportingText = hint?.let { { Text(it) } },
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
    )
}

/** A six-digit code; [send] does the request (wrong code: 40022). */
@Composable
private fun CodeDialog(title: String, text: String, confirm: String, send: suspend (String) -> Unit, onDone: () -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var code by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(title) },
        text = {
            Column {
                Text(text)
                CodeField(code, { code = it }, error)
            }
        },
        confirmButton = {
            TextButton(enabled = code.length == 6 && !busy, onClick = {
                busy = true
                scope.launch {
                    runCatching { send(code) }.onSuccess { onDone() }.onFailure { error = codeError(context, it) }
                    busy = false
                }
            }) { Text(confirm) }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun CodeField(code: String, onChange: (String) -> Unit, error: String?) {
    OutlinedTextField(
        value = code,
        onValueChange = { v -> onChange(v.filter(Char::isDigit).take(6)) },
        label = { Text(stringResource(R.string.two_fa_code)) },
        singleLine = true,
        isError = error != null,
        supportingText = error?.let { { Text(it) } },
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
        modifier = Modifier.padding(top = 12.dp),
    )
}

private fun codeError(context: android.content.Context, e: Throwable): String = when ((e as? io.github.svenbuild.fylvo.android.data.ApiException)?.code) {
    40022 -> context.getString(R.string.two_fa_code_wrong)
    50005 -> context.getString(R.string.two_fa_expired)
    else -> userError(e).resolve(context)
}

/**
 * Turning on two-factor sign-in: the secret for an authenticator app, then a code from it. A page
 * of its own whose window allows no screenshots while it is open; the secret cannot be copied.
 */
@Composable
private fun TwoFaSetup(secret: String, email: String, onDone: () -> Unit, onCancel: () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val scope = rememberCoroutineScope()
    var code by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    DisposableEffect(Unit) {
        val window = (context as? Activity)?.window
        window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        onDispose { window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE) }
    }
    BackHandler(onBack = onCancel)
    androidx.compose.material3.Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp)) {
            Spacer(Modifier.height(32.dp))
            Text(stringResource(R.string.two_fa_turn_on), style = MaterialTheme.typography.headlineSmall)
            Text(stringResource(R.string.two_fa_setup_text), style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 12.dp))
            Text(
                SecurityRepository.grouped(secret),
                style = MaterialTheme.typography.titleLarge,
                fontFamily = FontFamily.Monospace,
                modifier = Modifier.padding(vertical = 20.dp),
            )
            OutlinedButton(onClick = {
                runCatching {
                    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(SecurityRepository.otpAuthUri(secret, email))))
                }.onFailure { error = context.getString(R.string.two_fa_no_app) }
            }) { Text(stringResource(R.string.two_fa_open_app)) }
            Text(stringResource(R.string.two_fa_enter_code), style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 24.dp))
            CodeField(code, { code = it }, error)
            Row(Modifier.fillMaxWidth().padding(top = 16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End)) {
                TextButton(enabled = !busy, onClick = onCancel) { Text(stringResource(R.string.cancel)) }
                Button(enabled = code.length == 6 && !busy, onClick = {
                    busy = true
                    scope.launch {
                        runCatching { app.security.setTwoFa(true, code) }.onSuccess { onDone() }.onFailure { error = codeError(context, it) }
                        busy = false
                    }
                }) { Text(stringResource(R.string.two_fa_turn_on)) }
            }
        }
    }
}
