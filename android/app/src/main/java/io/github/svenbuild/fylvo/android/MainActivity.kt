package io.github.svenbuild.fylvo.android

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.LaunchedEffect
import androidx.core.content.ContextCompat
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import kotlinx.coroutines.launch
import io.github.svenbuild.fylvo.android.ui.files.FilesScreen
import io.github.svenbuild.fylvo.android.ui.files.FilesViewModel
import io.github.svenbuild.fylvo.android.ui.login.LoginScreen
import io.github.svenbuild.fylvo.android.ui.login.LoginViewModel
import io.github.svenbuild.fylvo.android.ui.settings.SettingsScreen
import io.github.svenbuild.fylvo.android.ui.theme.FylvoTheme
import io.github.svenbuild.fylvo.android.util.CrUri
import io.github.svenbuild.fylvo.android.work.UploadWorker

class MainActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        handleShareIntent(intent)
        setContent { App() }
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
            UploadWorker.enqueue(this, accepted, CrUri.MY_PREFIX)
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
                app.favoritesStore,
                app.viewSettings,
                androidx.work.WorkManager.getInstance(app),
                onSessionExpired = { app.sessionManager.clearSession() },
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
    val nav = rememberNavController()
    val scope = rememberCoroutineScope()

    val signOut: () -> Unit = { scope.launch { app.signOut() } }

    FylvoTheme {
        // Android 13+ needs a runtime grant before sync/upload notifications
        // can post; ask once the session is known to be signed in.
        val notifPermission = rememberLauncherForActivityResult(
            ActivityResultContracts.RequestPermission()
        ) { }
        LaunchedEffect(loggedIn) {
            if (loggedIn == true && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
                ContextCompat.checkSelfPermission(
                    context, Manifest.permission.POST_NOTIFICATIONS
                ) != PackageManager.PERMISSION_GRANTED
            ) {
                notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
        when (loggedIn) {
            null -> Unit // session store still loading; keep frame empty
            false -> NavHost(navController = nav, startDestination = "login") {
                composable("login") {
                    LoginScreen(
                        viewModel = viewModel(factory = factory),
                        onLoggedIn = { nav.navigate("files") { popUpTo(0) } },
                    )
                }
            }
            true -> {
                // Activity-scoped so Settings can drive the same files view
                // (e.g. the backup card's "open folder").
                val filesVm: FilesViewModel = viewModel(factory = factory)
                NavHost(navController = nav, startDestination = "files") {
                composable("files") {
                    FilesScreen(
                        viewModel = filesVm,
                        onOpenSettings = { nav.navigate("settings") },
                    )
                }
                composable(
                    "settings",
                    enterTransition = { slideInHorizontally(tween(260)) { it / 3 } + fadeIn(tween(200)) },
                    exitTransition = { fadeOut(tween(160)) },
                    popEnterTransition = { fadeIn(tween(200)) },
                    popExitTransition = { slideOutHorizontally(tween(220)) { it / 3 } + fadeOut(tween(160)) },
                ) {
                    SettingsScreen(
                        onBack = { nav.popBackStack() },
                        onSignOut = signOut,
                    )
                }
            }
            }
        }
    }
}
