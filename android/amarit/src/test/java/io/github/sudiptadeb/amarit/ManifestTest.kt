package io.github.sudiptadeb.amarit

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File
import java.time.Instant

/** The fixture is the repository's testdata/manifest.json, the one the Go tests parse. */
val goodManifest: String = File("../../testdata/manifest.json").readText()
const val zeros = "0000000000000000000000000000000000000000000000000000000000000000"

class ManifestTest {
    @Test
    fun parsesTheSharedFixture() {
        val m = Manifest.parse(goodManifest)
        assertEquals("termulaa", m.project)
        assertEquals("0.5.0", m.minVersion)
        val t = m.lookup("canary", "darwin/arm64")
        assertNotNull(t)
        assertEquals("0.5.3", t!!.version)
        assertEquals(11L, t.asset.size)
        assertEquals("RWQ", t.asset.sig)
        assertTrue(t.release.critical)
        assertEquals(25, t.release.rollout)
        val a = m.lookup("canary", "android")!!
        assertEquals(4, a.asset.versionCode)
        assertNull("stable has no android asset, lookup must say so", m.lookup("stable", "android"))
        assertNull("unknown channel must not resolve", m.lookup("beta", "darwin/arm64"))
        assertFalse(m.isStale(Instant.parse("2026-10-20T00:00:00Z")))
        assertTrue(m.isStale(Instant.parse("2026-12-01T00:00:00Z")))
    }

    @Test
    fun rejectsWhatTheGoParserRejects() {
        val cases = mapOf(
            "unknown spec" to goodManifest.replace("\"spec\": 1", "\"spec\": 2"),
            "missing project" to goodManifest.replace("\"project\": \"termulaa\"", "\"project\": \"\""),
            "channel without release" to goodManifest.replace("\"stable\": \"0.5.2\"", "\"stable\": \"0.9.9\""),
            "rollout out of range" to goodManifest.replace("\"rollout\": 25", "\"rollout\": 101"),
            "bad digest" to goodManifest.replaceFirst(zeros, "abc"),
            "zero size" to goodManifest.replace("\"size\": 10", "\"size\": 0"),
            "version with bad chars" to goodManifest.replace("\"0.5.3\"", "\"0.5.3 beta\""),
            "no channels" to goodManifest.replace("\"channels\": {", "\"channels\": {}, \"x\": {"),
            "android without code" to goodManifest.replace(", \"version_code\": 4", ""),
            "not json" to "{",
            "bad time" to goodManifest.replace("2026-10-09T10:00:00Z", "yesterday"),
        )
        for ((name, body) in cases) {
            try {
                Manifest.parse(body)
                fail("$name: want an error")
            } catch (e: IllegalArgumentException) {
                assertTrue(name, e.message!!.startsWith("manifest:"))
            }
        }
    }

    @Test
    fun rollout() {
        val low = byteArrayOf(0, 0, 0, 5, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9) // 5 % 100 = 5
        val high = byteArrayOf(0, 0, 0, 99, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1) // 99
        assertTrue(Manifest.inRollout(low, null))
        assertTrue(Manifest.inRollout(low, 100))
        assertTrue(Manifest.inRollout(low, 10))
        assertFalse(Manifest.inRollout(high, 10))
        assertFalse(Manifest.inRollout(low, 0))
        assertFalse(Manifest.inRollout(byteArrayOf(1, 2), 50))
    }
}
