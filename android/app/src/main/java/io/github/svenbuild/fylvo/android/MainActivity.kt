package io.github.svenbuild.fylvo.android

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import kotlinx.coroutines.Dispatchers
import androidx.lifecycle.lifecycleScope
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.compose.runtime.LaunchedEffect
import androidx.core.content.ContextCompat
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.launch
import io.github.svenbuild.fylvo.android.ui.files.FilesScreen
import io.github.svenbuild.fylvo.android.ui.files.FilesViewModel
import io.github.svenbuild.fylvo.android.ui.login.LoginScreen
import io.github.svenbuild.fylvo.android.ui.login.LoginViewModel
import io.github.svenbuild.fylvo.android.ui.theme.FylvoTheme
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.work.UploadWorker

// AppCompatActivity for the per-app language (util/AppLanguage.kt); the UI is all Compose.
class MainActivity : AppCompatActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The activity is recreated on a language change, so the labels follow it.
        CrUri.useLabelsFrom(this)
        enableEdgeToEdge()
        val fromHistory = intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0
        if (savedInstanceState == null && !fromHistory) handleShareIntent(intent)
        setContent { App() }
    }

    /** Fylvo: back in the app: edited files that wait get their check soon. */
    override fun onResume() {
        super.onResume()
        val app = application as FylvoApp
        lifecycleScope.launch { runCatching { app.offlineEdits.start() } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleShareIntent(intent)
    }

    // Share-sheet target: files other apps hand to Fylvo go to My files.
    private fun handleShareIntent(intent: Intent?) {
        val uris = when (intent?.action) {
            Intent.ACTION_SEND -> listOfNotNull(
                @Suppress("DEPRECATION")
                intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
            )
            Intent.ACTION_SEND_MULTIPLE ->
                @Suppress("DEPRECATION")
                intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM)
            else -> null
        }
        // Fylvo: only content:// documents of other apps, never file:// or our own providers.
        val accepted = uris.orEmpty().filter { UploadWorker.isForeignContentUri(this, it) }
        if (accepted.isNotEmpty()) {
            // Fylvo: names and the upload list off the main thread (a share can hold hundreds).
            val context = applicationContext
            lifecycleScope.launch(Dispatchers.IO) { UploadWorker.enqueueSafely(context, accepted, CrUri.MY_PREFIX) }
        }
    }
}

private class AppViewModelFactory(private val app: FylvoApp) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T = when {
        modelClass.isAssignableFrom(LoginViewModel::class.java) ->
            LoginViewModel(app.sessionManager, app.apiClient) as T
        modelClass.isAssignableFrom(FilesViewModel::class.java) ->
            FilesViewModel(
                app.fileRepository,
                app.offlineManager,
                app.offlineEdits,
                app.viewSettings,
                androidx.work.WorkManager.getInstance(app),
                onSessionExpired = { app.endSession(expired = true) },
            ) as T
        else -> throw IllegalArgumentException("Unknown ViewModel ${modelClass.name}")
    }
}

@Composable
fun App() {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val factory = remember { AppViewModelFactory(app) }
    val loggedIn by app.sessionManager.isLoggedIn.collectAsState(initial = null)

    // Fylvo: when a session ends (sign-out or expired) start the screen afresh in a new task,
    // so no listing, recent file or thumbnail URL of the old account survives in a view model.
    var wasLoggedIn by remember { mutableStateOf(false) }
    LaunchedEffect(loggedIn) {
        if (loggedIn == true) wasLoggedIn = true
        if (loggedIn == false && wasLoggedIn) {
            context.startActivity(
                Intent(context, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK),
            )
        }
    }

    FylvoTheme {
        // Android 13+ needs a runtime grant before sync/upload notifications
        // can post; ask once the session is known to be signed in.
        val notifPermission = rememberLauncherForActivityResult(
            ActivityResultContracts.RequestPermission()
        ) { }
        // Ask once per app start, not again when a language change recreates the screen.
        var askedNotifications by rememberSaveable { mutableStateOf(false) }
        // Fylvo: look for an app update once per app start, quietly.
        LaunchedEffect(loggedIn) {
            if (loggedIn == true) app.appUpdater.checkOnStart()
        }
        LaunchedEffect(loggedIn) {
            if (loggedIn == true && !askedNotifications && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
                ContextCompat.checkSelfPermission(
                    context, Manifest.permission.POST_NOTIFICATIONS
                ) != PackageManager.PERMISSION_GRANTED
            ) {
                askedNotifications = true
                notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
        when (loggedIn) {
            null -> Unit // session store still loading; keep frame empty
            // Signing in flips loggedIn, which swaps in the files screen by itself.
            false -> LoginScreen(viewModel = viewModel(factory = factory), onLoggedIn = {})
            true -> {
                FilesScreen(viewModel = viewModel(factory = factory))
            }
        }
    }
}
