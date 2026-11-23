package io.github.svenbuild.fylvo.android.data

import org.junit.Assert.assertEquals
import org.junit.Test

/** Fylvo 1.6: two-factor setup helpers (design/android-parity-plan.md, 9). */
class SecurityTest {
    @Test fun otpAuthAddress() {
        assertEquals(
            "otpauth://totp/Fylvo:anna%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Fylvo",
            SecurityRepository.otpAuthUri("JBSWY3DPEHPK3PXP", "anna@example.com"),
        )
    }

    @Test fun secretInGroupsOfFour() {
        assertEquals("JBSW Y3DP EHPK 3PXP", SecurityRepository.grouped("JBSWY3DPEHPK3PXP"))
        assertEquals("ABCD E", SecurityRepository.grouped("ABCDE"))
    }
}
