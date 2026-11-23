package io.github.svenbuild.fylvo.android.ui.files

import io.github.svenbuild.fylvo.android.api.ConnectedOverview
import io.github.svenbuild.fylvo.android.api.ConnectedProvider
import io.github.svenbuild.fylvo.android.api.ConnectedStorage
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fylvo 1.6.1: when Account offers the connected storages page (like the web app's tab). */
class ConnectedPageStateTest {
    private val drive = ConnectedProvider(type = "drive", name = "Google Drive", configured = true)
    private val notSetUp = drive.copy(configured = false)

    private fun state(overview: ConnectedOverview?, failed: Boolean = false) = ConnectedStorages.State(overview = overview, failed = failed)

    @Test fun noProviderAndNoStorageHidesTheEntry() {
        // The live server of the report: the feature is on, nothing set up, nothing connected.
        assertFalse(state(ConnectedOverview(available = true, allowed = true, providers = listOf(notSetUp))).pageWanted)
    }

    @Test fun aProviderOrAStorageShowsIt() {
        assertTrue(state(ConnectedOverview(available = true, allowed = true, providers = listOf(drive))).pageWanted)
        // Storages stay reachable (to disconnect) without the right or without a provider.
        val storage = ConnectedStorage(id = "s1", provider = "drive")
        assertTrue(state(ConnectedOverview(available = true, allowed = false, providers = listOf(notSetUp), storages = listOf(storage))).pageWanted)
    }

    @Test fun notAllowedWithoutStoragesHidesIt() {
        assertFalse(state(ConnectedOverview(available = true, allowed = false, providers = listOf(drive))).pageWanted)
        assertFalse(state(ConnectedOverview(available = false, allowed = true, providers = listOf(drive))).pageWanted)
    }

    @Test fun aFailedLoadShowsItForTheRetry() {
        assertTrue(state(null, failed = true).pageWanted)
        assertFalse(state(null).pageWanted)
    }

    @Test fun aFailedRefreshKeepsTheRetryReachable() {
        // An overview without anything to show was read before; the next load failed.
        val nothing = ConnectedOverview(available = true, allowed = true, providers = listOf(notSetUp))
        assertTrue(state(nothing, failed = true).pageWanted)
    }

    @Test fun aServerWithoutTheFeatureHidesIt() {
        assertFalse(ConnectedStorages.State(missing = true).pageWanted)
    }
}
