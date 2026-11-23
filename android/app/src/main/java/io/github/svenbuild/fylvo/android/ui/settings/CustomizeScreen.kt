package io.github.svenbuild.fylvo.android.ui.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import io.github.svenbuild.fylvo.android.FylvoApp
import io.github.svenbuild.fylvo.android.R
import io.github.svenbuild.fylvo.android.data.prefs.Accents
import io.github.svenbuild.fylvo.android.data.prefs.Density
import io.github.svenbuild.fylvo.android.data.prefs.MobilePrefs
import io.github.svenbuild.fylvo.android.data.prefs.ThemeMode
import kotlinx.coroutines.launch

/**
 * Fylvo: Account › Customise. Everything here belongs to the account and also applies on the web
 * (design/customize-plan.md); on an older server only a hint is shown.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CustomizeScreen(
    onBack: () -> Unit,
    bottomBar: @Composable () -> Unit,
    /** Ids of the connected storages (they can become tabs). */
    storages: List<String> = emptyList(),
) {
    val context = LocalContext.current
    val app = context.applicationContext as FylvoApp
    val repo = app.accountPrefs
    val state by repo.state.collectAsState()
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    BackHandler(onBack = onBack)

    val saveFailed = stringResource(R.string.customize_save_failed)
    /** Null: saved. Otherwise the server's message, or a general one. */
    val showMessage: (String?) -> Unit = { message ->
        if (message != null) scope.launch { snackbar.showSnackbar(message.ifEmpty { saveFailed }) }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.customize)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.up))
                    }
                },
            )
        },
        bottomBar = bottomBar,
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(bottom = 24.dp),
        ) {
            val prefs = state.prefs
            if (state.supported == false || prefs == null) {
                Text(
                    stringResource(if (state.supported == false) R.string.customize_needs_server else R.string.customize_loading),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(16.dp),
                )
                return@Column
            }
            Text(
                stringResource(R.string.customize_account_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 4.dp),
            )

            TabsSection(prefs, repo, storages, showMessage)
            SwipeSection(prefs, repo, showMessage)
            StartSection(prefs, repo, showMessage)

            CustomizeLabel(stringResource(R.string.customize_appearance))
            val mode = MobilePrefs.mode(prefs)
            Segmented(
                options = listOf(
                    ThemeMode.SYSTEM to stringResource(R.string.theme_system),
                    ThemeMode.LIGHT to stringResource(R.string.theme_light),
                    ThemeMode.DARK to stringResource(R.string.theme_dark),
                ),
                selected = mode,
                onSelect = { m ->
                    scope.launch {
                        showMessage(repo.patchOptimistic({ p -> p.copy(appearance = p.appearance.copy(mode = m.key), customized = (p.customized + "mode").distinct()) }, repo.modePatch(m)))
                    }
                },
            )
            if (state.accents.size > 1) {
                val current = state.accents.firstOrNull { it.key.equals(prefs.appearance.accent, ignoreCase = true) }?.key
                    ?: Accents.DEFAULT_KEY
                val accentLabel = stringResource(R.string.customize_accent)
                Row(
                    Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
                    horizontalArrangement = Arrangement.spacedBy(14.dp),
                ) {
                    state.accents.forEach { accent ->
                        val on = accent.key.equals(current, ignoreCase = true)
                        val dark = MaterialTheme.colorScheme.surface.luminanceIsDark()
                        Box(
                            Modifier
                                .size(32.dp)
                                .then(if (on) Modifier.border(2.dp, MaterialTheme.colorScheme.onSurface, CircleShape) else Modifier)
                                .padding(4.dp)
                                .background(Color(if (dark) accent.dark else accent.light), CircleShape)
                                .semantics {
                                    contentDescription = "$accentLabel ${accent.key}"
                                    selected = on
                                }
                                .clickable(role = Role.RadioButton) {
                                    scope.launch {
                                        showMessage(repo.patchOptimistic({ p -> p.copy(appearance = p.appearance.copy(accent = accent.key)) }, repo.accentPatch(accent.key)))
                                    }
                                },
                            contentAlignment = Alignment.Center,
                        ) {
                            if (on) Icon(Icons.Default.Check, contentDescription = null, tint = Color(Accents.onColor(if (dark) accent.dark else accent.light)), modifier = Modifier.size(14.dp))
                        }
                    }
                }
            }

            CustomizeLabel(stringResource(R.string.customize_density))
            Segmented(
                options = listOf(
                    Density.COMPACT to stringResource(R.string.density_compact),
                    Density.NORMAL to stringResource(R.string.density_normal),
                    Density.COMFORTABLE to stringResource(R.string.density_comfortable),
                ),
                selected = MobilePrefs.density(prefs),
                onSelect = { d ->
                    scope.launch {
                        showMessage(repo.patchOptimistic({ p -> p.copy(view = p.view.copy(density = d.key)) }, repo.densityPatch(d)))
                    }
                },
            )
            ViewSection(prefs, state.disableViewSync, repo, showMessage)
            ResetSection(repo, showMessage)
        }
    }
}

private fun Color.luminanceIsDark(): Boolean = (0.299 * red + 0.587 * green + 0.114 * blue) < 0.5

@Composable
fun CustomizeLabel(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 20.dp, bottom = 6.dp),
    )
}

/** Fylvo: a calm segmented choice (hairline frame, selected part filled neutral). */
@Composable
fun <T> Segmented(options: List<Pair<T, String>>, selected: T, onSelect: (T) -> Unit) {
    val colors = MaterialTheme.colorScheme
    Surface(
        shape = RoundedCornerShape(10.dp),
        border = BorderStroke(1.dp, colors.outlineVariant),
        color = Color.Transparent,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp)
            .height(40.dp),
    ) {
        Row {
            options.forEach { (value, label) ->
                val on = value == selected
                Box(
                    Modifier
                        .weight(1f)
                        .fillMaxSize()
                        .background(if (on) colors.secondaryContainer else Color.Transparent)
                        .semantics { this.selected = on }
                        .clickable(role = Role.RadioButton) { if (!on) onSelect(value) },
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        label,
                        style = MaterialTheme.typography.labelLarge,
                        color = if (on) colors.onSurface else colors.onSurfaceVariant,
                    )
                }
            }
        }
    }
}
