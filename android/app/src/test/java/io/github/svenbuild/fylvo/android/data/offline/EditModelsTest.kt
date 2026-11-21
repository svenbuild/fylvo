package io.github.svenbuild.fylvo.android.data.offline

import io.github.svenbuild.fylvo.android.api.FileObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class EditModelsTest {
    private val now = 1_800_000_000_000L
    private val base = EditSnapshot(5, 1_000, "base")
    private fun entry(
        state: EditState = EditState.Editing,
        confirmed: EditSnapshot? = null,
        pending: EditMarker? = null,
        retiredAt: Long = 0,
        baseEntity: String = "e1",
        seen: EditSeen? = null,
    ) = EditEntry(
        id = "w1", fileId = "f1", uri = "cloudreve://my/Docs/a.txt", name = "a.txt", baseEntity = baseEntity, base = base,
        confirmed = confirmed, pending = pending, state = state, retiredAt = retiredAt, seen = seen,
    )
    private fun marker(at: Long = now, sessionId: String = "") =
        EditMarker(UploadKind.Version, EditSnapshot(6, 2_000, "snap"), target = "cloudreve://my/Docs/a.txt", sessionId = sessionId, entityId = "e2", at = at)

    // ---- change detection and the stable rule ----

    @Test fun changedAgainstBaseUntilAnUploadIsConfirmed() {
        assertFalse(isChanged(entry(), "base"))
        assertTrue(isChanged(entry(), "other"))
        val uploaded = entry(confirmed = EditSnapshot(6, 2_000, "up"))
        assertFalse(isChanged(uploaded, "up"))
        assertTrue(isChanged(uploaded, "base"))
    }

    @Test fun stableWhenNotModifiedForTwoMinutes() {
        assertTrue(isStable(LocalStat(5, now - EDIT_STABLE_MS), null, now))
        assertFalse(isStable(LocalStat(5, now - EDIT_STABLE_MS + 1), null, now))
    }

    @Test fun stableWhenSeenTheSameTwoMinutesAgo() {
        // A modification time in the future (other clock) still settles by what the pass saw.
        val stat = LocalStat(5, now + 60_000)
        assertFalse(isStable(stat, EditSeen(5, now + 60_000, now - 60_000), now))
        assertTrue(isStable(stat, EditSeen(5, now + 60_000, now - EDIT_STABLE_MS), now))
        // Another size: not the same file state.
        assertFalse(isStable(stat, EditSeen(4, now + 60_000, now - EDIT_STABLE_MS), now))
    }

    @Test fun seenIsKeptWhileSizeAndTimeStay() {
        val seen = EditSeen(5, 1_000, 3)
        assertEquals(seen, nextSeen(LocalStat(5, 1_000), seen, now))
        assertEquals(EditSeen(6, 1_000, now), nextSeen(LocalStat(6, 1_000), seen, now))
        assertEquals(EditSeen(5, 1_000, now), nextSeen(LocalStat(5, 1_000), null, now))
    }

    // ---- planner ----

    @Test fun plannerUploadsOnlyStableChanges() {
        val old = LocalStat(6, now - EDIT_STABLE_MS)
        val fresh = LocalStat(6, now)
        assertEquals(EditStep.Unchanged, planEdit(entry(), old, "base", now, explicit = false, retryErrors = true))
        assertEquals(EditStep.Upload, planEdit(entry(), old, "new", now, explicit = false, retryErrors = true))
        assertEquals(EditStep.Unstable, planEdit(entry(), fresh, "new", now, explicit = false, retryErrors = true))
        // Explicit actions do not wait for a quiet file.
        assertEquals(EditStep.Upload, planEdit(entry(), fresh, "new", now, explicit = true, retryErrors = true))
    }

    @Test fun plannerSkipsCreatingAndLeavesErrorsToRealRuns() {
        val stat = LocalStat(6, 0)
        assertEquals(EditStep.Skip, planEdit(entry(state = EditState.Creating), stat, "new", now, false, true))
        assertEquals(EditStep.Skip, planEdit(entry(state = EditState.Error), stat, "new", now, false, retryErrors = false))
        assertEquals(EditStep.Upload, planEdit(entry(state = EditState.Error), stat, "new", now, false, retryErrors = true))
    }

    @Test fun plannerRecoversAMarkerOnlyAfterTheDelay() {
        val stat = LocalStat(6, 0)
        assertEquals(EditStep.WaitRecovery, planEdit(entry(pending = marker(at = now - 1_000)), stat, null, now, false, true))
        assertEquals(EditStep.Recover, planEdit(entry(pending = marker(at = now - EDIT_RECOVERY_DELAY_MS)), stat, null, now, false, true))
        // Also when the explicit action asks: never right after an answer was lost.
        assertEquals(EditStep.WaitRecovery, planEdit(entry(pending = marker(at = now)), stat, null, now, true, true))
    }

    @Test fun plannerMissingAndExpiredCopies() {
        assertEquals(EditStep.Missing, planEdit(entry(), null, null, now, false, true))
        assertEquals(EditStep.Expire, planEdit(entry(retiredAt = now - 5), null, null, now, false, true))
        val stat = LocalStat(5, 0)
        assertEquals(EditStep.Unchanged, planEdit(entry(retiredAt = now - 5), stat, "base", now, false, true))
        assertEquals(EditStep.Expire, planEdit(entry(retiredAt = now - EDIT_RETIRED_KEEP_MS), stat, "base", now, false, true))
        // A late write into a finished copy is uploaded (as a conflict copy, see planTarget).
        assertEquals(EditStep.Upload, planEdit(entry(retiredAt = now - EDIT_RETIRED_KEEP_MS), stat, "late", now, false, true))
    }

    @Test fun targetIsAVersionOnlyForTheSameFile() {
        val same = Preflight.Found("f1", folder = false, primaryEntity = "e1")
        assertEquals(TargetPlan.Version, planTarget(entry(), same, decideGone = false))
        assertEquals(TargetPlan.Later, planTarget(entry(), Preflight.Unclear, decideGone = true))
        assertEquals(TargetPlan.PrivateConflict, planTarget(entry(), Preflight.NoAccess, decideGone = false))
        // Another file or a folder took the place, or it is gone: a conflict copy, but only after the listing.
        for (other in listOf(Preflight.Found("f2", false, "e9"), Preflight.Found("f1", true, ""), Preflight.Gone)) {
            assertEquals(TargetPlan.WaitForSync, planTarget(entry(), other, decideGone = false))
            assertEquals(TargetPlan.Conflict, planTarget(entry(), other, decideGone = true))
        }
        // Empty base (no version to name) and finished copies never become a version.
        assertEquals(TargetPlan.Conflict, planTarget(entry(baseEntity = ""), same, decideGone = false))
        assertEquals(TargetPlan.Conflict, planTarget(entry(retiredAt = 1), same, decideGone = false))
    }

    @Test fun preflightCodes() {
        val file = FileObject(id = "f1", primaryEntity = "e1")
        assertEquals(Preflight.Found("f1", false, "e1"), preflightOf(file, null))
        assertEquals(Preflight.Gone, preflightOf(null, null))
        assertEquals(Preflight.Gone, preflightOf(null, 404))
        assertEquals(Preflight.NoAccess, preflightOf(null, 40082))
        assertEquals(Preflight.NoAccess, preflightOf(null, 403))
        assertEquals(Preflight.NoAccess, preflightOf(null, 40058))
        assertEquals(Preflight.Unclear, preflightOf(null, 500))
        assertEquals(Preflight.Unclear, preflightOf(null, 401))
    }

    @Test fun refusalsOfAVersionUpload() {
        assertEquals(UploadOutcome.Conflict, classifyRefusal(40076))
        assertEquals(UploadOutcome.Conflict, classifyRefusal(40904))
        assertEquals(UploadOutcome.Conflict, classifyRefusal(40004))
        assertEquals(UploadOutcome.PrivateConflict, classifyRefusal(40082))
        assertEquals(UploadOutcome.Later, classifyRefusal(40073))
        assertEquals(UploadOutcome.ServerFull, classifyRefusal(40051))
        assertEquals(UploadOutcome.Replan, classifyRefusal(409))
        assertEquals(UploadOutcome.Later, classifyRefusal(502))
        assertEquals(UploadOutcome.Later, classifyRefusal(50001))
        assertEquals(UploadOutcome.Error, classifyRefusal(40001))
    }

    @Test fun cancelAnswers() {
        assertEquals(CancelOutcome.Check, classifyCancel(0))
        assertEquals(CancelOutcome.PrivateConflict, classifyCancel(40082))
        assertEquals(CancelOutcome.Keep, classifyCancel(null))
        assertEquals(CancelOutcome.Keep, classifyCancel(404))
        assertEquals(CancelOutcome.Keep, classifyCancel(40073))
    }

    @Test fun commitProofIsTheCurrentVersionOnly() {
        val file = FileObject(id = "f1", primaryEntity = "e3")
        // A newer version on top, or an unfinished upload listed among the versions: not proven.
        assertFalse(isCurrentVersion(file, "e2"))
        assertTrue(isCurrentVersion(file, "e3"))
        assertFalse(isCurrentVersion(file.copy(primaryEntity = ""), ""))
    }

    // ---- rebase, finish, sign-out ----

    @Test fun confirmedVersionBecomesTheBase() {
        val m = marker(sessionId = "s")
        val e = confirmVersion(entry(state = EditState.Uploading, pending = m).copy(stalls = 2, attempts = 1), m)
        assertEquals("e2", e.baseEntity)
        assertEquals(m.snapshot, e.confirmed)
        assertNull(e.pending)
        assertEquals(EditState.Synced, e.state)
        assertEquals(0, e.stalls)
        assertEquals(0, e.attempts)
        assertEquals("f1", e.fileId)
    }

    @Test fun rebaseMovesTheWorkingCopyOntoTheConflictCopy() {
        val m = EditMarker(UploadKind.Conflict, EditSnapshot(6, 2, "snap"), target = "cloudreve://my/Docs", name = "a (x).txt", sessionId = "s", entityId = "c1", at = 0)
        // Someone already added a newer version to the copy: the base stays ours, so the next save conflicts again.
        val copy = FileObject(id = "f9", path = "cloudreve://my/Docs/a (x).txt", primaryEntity = "c2")
        val e = rebaseOnto(entry(pending = m).copy(outdated = true), copy, m)
        assertEquals("f9", e.fileId)
        assertEquals(copy.path, e.uri)
        assertEquals("c1", e.baseEntity)
        assertEquals(m.snapshot, e.confirmed)
        assertEquals("a (x).txt", e.conflict)
        assertNull(e.pending)
        assertFalse(e.outdated)
        // The local file keeps its place.
        assertEquals("w1", e.id)
        assertEquals("a.txt", e.name)
    }

    @Test fun settledOnlyWhenTheServerHoldsTheBytes() {
        assertTrue(isSettled(entry(), "base"))
        assertFalse(isSettled(entry(), "changed"))
        assertFalse(isSettled(entry(), null))
        assertFalse(isSettled(entry(pending = marker()), "base"))
        assertTrue(isSettled(entry(confirmed = EditSnapshot(6, 2, "up")), "up"))
    }

    @Test fun signOutAsksForChangedCopiesInUse() {
        val changed = entry()
        val clean = entry().copy(id = "w2")
        val missing = entry().copy(id = "w3")
        val finished = entry(retiredAt = 5).copy(id = "w4")
        val creating = entry(state = EditState.Creating).copy(id = "w5")
        val pending = entry(pending = marker()).copy(id = "w6")
        val blockers = signOutBlockers(
            listOf(changed to "new", clean to "base", missing to null, finished to "late", creating to "x", pending to "base")
        )
        assertEquals(listOf("w1", "w6"), blockers.map { it.id })
    }

    @Test fun outdatedWhenUnchangedAndTheServerMovedOn() {
        val newer = Preflight.Found("f1", false, "e5")
        assertTrue(isOutdated(entry(), changed = false, server = newer))
        assertFalse(isOutdated(entry(), changed = true, server = newer))
        assertFalse(isOutdated(entry(), changed = false, server = Preflight.Found("f1", false, "e1")))
        assertFalse(isOutdated(entry(), changed = false, server = Preflight.Found("f2", false, "e5")))
        assertFalse(isOutdated(entry(pending = marker()), changed = false, server = newer))
        assertFalse(isOutdated(entry(), changed = false, server = Preflight.Unclear))
    }

    @Test fun retryDelays() {
        assertEquals(3 * 60_000L, retryDelay(0))
        assertEquals(15 * 60_000L, retryDelay(1))
        assertEquals(60 * 60_000L, retryDelay(2))
        assertNull(retryDelay(3))
    }

    @Test fun needsCheckWithoutHashing() {
        assertFalse(needsCheck(entry(), LocalStat(5, 1_000)))
        assertTrue(needsCheck(entry(), LocalStat(6, 1_000)))
        assertTrue(needsCheck(entry(), LocalStat(5, 1_001)))
        assertTrue(needsCheck(entry(state = EditState.Uploading), LocalStat(5, 1_000)))
        assertTrue(needsCheck(entry(pending = marker()), null))
        assertFalse(needsCheck(entry(), null))
        assertFalse(needsCheck(entry(state = EditState.Creating), LocalStat(9, 9)))
        assertFalse(needsCheck(entry(retiredAt = 1), LocalStat(9, 9)))
    }

    @Test fun attentionOnTheOfflineScreen() {
        assertFalse(needsAttention(entry()))
        assertTrue(needsAttention(entry(state = EditState.Error)))
        assertTrue(needsAttention(entry(state = EditState.Uploading).copy(problem = EditProblem.ServerFull)))
        assertTrue(needsAttention(entry().copy(conflict = "a (x).txt")))
        assertFalse(needsAttention(entry(retiredAt = 1).copy(conflict = "a (x).txt")))
        assertTrue(needsAttention(entry(state = EditState.Uploading, retiredAt = 1)))
        assertFalse(needsAttention(entry(state = EditState.Creating).copy(problem = EditProblem.Missing)))
    }

    // ---- conflict names ----

    @Test fun conflictNameKeepsTheExtension() {
        assertEquals("Report (Konflikt Handy 2026-11-21 10-30).docx", conflictName("Report.docx", "Konflikt Handy", "2026-11-21 10-30"))
        assertEquals("Report (phone conflict 2026-11-21 10-30).docx", conflictName("Report.docx", "phone conflict", "2026-11-21 10-30"))
        assertEquals("a.tar (L T).gz", conflictName("a.tar.gz", "L", "T"))
    }

    @Test fun conflictNameCounter() {
        assertEquals("a (L T).txt", conflictName("a.txt", "L", "T", counter = 1))
        assertEquals("a (L T 2).txt", conflictName("a.txt", "L", "T", counter = 2))
        assertEquals("a (L T 3).txt", conflictName("a.txt", "L", "T", counter = 3))
    }

    @Test fun conflictNameWithoutExtensionAndForDotFiles() {
        assertEquals("Makefile (L T)", conflictName("Makefile", "L", "T"))
        assertEquals(".bashrc (L T)", conflictName(".bashrc", "L", "T"))
        // A trailing dot counts as the (empty) extension.
        assertEquals("notes (L T).", conflictName("notes.", "L", "T"))
    }

    @Test fun conflictNameFitsTheByteLimit() {
        val label = "Konflikt Handy"
        val time = "2026-11-21 10-30"
        val long = "ä".repeat(200) + ".txt"
        val name = conflictName(long, label, time, counter = 20)
        assertTrue(name.toByteArray(Charsets.UTF_8).size <= NAME_MAX_BYTES)
        assertTrue(name.endsWith(" ($label $time 20).txt"))
        assertTrue(name.startsWith("ää"))
        // Characters outside the BMP are never cut in half.
        val emoji = "😀".repeat(80) + ".md"
        val cut = conflictName(emoji, label, time)
        assertTrue(cut.toByteArray(Charsets.UTF_8).size <= NAME_MAX_BYTES)
        // A half character would not survive the round trip through UTF-8.
        assertEquals(cut, String(cut.toByteArray(Charsets.UTF_8), Charsets.UTF_8))
        assertTrue(cut.endsWith(".md"))
    }

    @Test fun anExtensionTooLongToKeepGoesIntoTheBase() {
        val name = "a." + "x".repeat(300)
        val result = conflictName(name, "L", "T")
        assertTrue(result.toByteArray(Charsets.UTF_8).size <= NAME_MAX_BYTES)
        assertTrue(result.endsWith(" (L T)"))
    }

    @Test fun takenNamesTryTheNextCounter() {
        assertTrue(isNameTaken(40004))
        // Another spelling (case only) is taken.
        assertTrue(isNameTaken(40905))
        assertFalse(isNameTaken(40076))
    }

    @Test fun truncateCountsBytes() {
        assertEquals("ab", truncateUtf8("abc", 2))
        assertEquals("ä", truncateUtf8("ää", 3))
        assertEquals("", truncateUtf8("😀", 3))
    }

    @Test fun conflictFolderIsTheFilesFolderOrMyFiles() {
        assertEquals("cloudreve://my/Docs", conflictFolder(entry(), private = false))
        assertEquals("cloudreve://my", conflictFolder(entry(), private = true))
    }
}
