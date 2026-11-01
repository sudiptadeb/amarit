package io.github.sudiptadeb.amarit

import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test

class VersionsTest {
    @Test
    fun ordersLikeVersionGo() {
        val cases = listOf(
            Triple("0.5.2", "0.5.3", -1),
            Triple("0.5.3", "0.5.3", 0),
            Triple("1.0.0", "0.9.9", 1),
            Triple("0.10.0", "0.9.0", 1),
            Triple("1.0.0-rc.1", "1.0.0", -1),
            Triple("1.0.0-rc.1", "1.0.0-rc.2", -1),
            Triple("1.0.0-alpha", "1.0.0-alpha.1", -1),
            Triple("1.0.0-1", "1.0.0-alpha", -1),
            Triple("1.0.0+build.7", "1.0.0", 0),
            Triple("0.2.1+g6c3c4d4", "0.2.1", 0),
        )
        for ((a, b, want) in cases) assertEquals("$a vs $b", want, Versions.compare(a, b))
        for (bad in listOf("v1.0.0", "1.0", "1.01.0", "2026-10-09--11-17+sha", "", "1.a.0")) {
            try {
                Versions.compare(bad, "1.0.0")
                fail("$bad: want an error")
            } catch (e: IllegalArgumentException) {
            }
        }
    }
}
