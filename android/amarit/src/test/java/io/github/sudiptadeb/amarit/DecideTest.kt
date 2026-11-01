package io.github.sudiptadeb.amarit

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class DecideTest {
    private val fresh = Instant.parse("2026-10-20T00:00:00Z")
    private val id = ByteArray(16) // first 4 bytes 0: inside every rollout above 0

    private fun manifest(latest: String = "0.2.1+gabc1234", latestCode: Int = 3, stable: String = "0.2.1", stableCode: Int = 3, extra: String = "") = Manifest.parse(
        """{"spec":1,"project":"etchosts","published":"2026-10-09T10:00:00Z",$extra
            "channels":{"stable":"$stable","latest":"$latest"},
            "releases":{
              "$stable":{"assets":{"android":{"url":"etchosts-v$stable.apk","sha256":"$zeros","size":10,"version_code":$stableCode}}},
              "$latest":{"assets":{"android":{"url":"etchosts-latest.apk","sha256":"$zeros","size":11,"version_code":$latestCode}}}
            }}""",
    )

    @Test
    fun higherCodeIsAnUpdate() {
        val d = decide(manifest(), "stable", Installed("0.2.0", 2), id, null, fresh)
        assertTrue(d.update)
        assertEquals("0.2.1", d.version)
        assertEquals("0.2.1 available (running 0.2.0)", d.reason)
    }

    @Test
    fun sameCodeSameStringIsCurrent() {
        val d = decide(manifest(), "stable", Installed("0.2.1", 3), id, null, fresh)
        assertFalse(d.update)
        assertEquals("already on 0.2.1", d.reason)
    }

    @Test
    fun sameCodeDifferentStringIsARebuild() {
        // The rolling channel: each push is a new string on the same versionCode.
        val d = decide(manifest(), "latest", Installed("0.2.1+g0000000", 3), id, null, fresh)
        assertTrue(d.update)
        assertEquals("0.2.1+gabc1234", d.version)
        // And a dev build following stable is offered the release build.
        assertTrue(decide(manifest(), "stable", Installed("0.2.1+g0000000", 3), id, null, fresh).update)
        // But a dev build at the channel's own string stays put.
        assertFalse(decide(manifest(), "latest", Installed("0.2.1+gabc1234", 3), id, null, fresh).update)
    }

    @Test
    fun lowerCodeIsNeverTaken() {
        val d = decide(manifest(stable = "0.2.1", stableCode = 3), "stable", Installed("0.3.0+g1111111", 4), id, null, fresh)
        assertFalse(d.update)
        assertTrue(d.reason, d.reason.contains("not downgrading"))
    }

    @Test
    fun nothingForAndroid() {
        val d = decide(Manifest.parse(goodManifest), "stable", Installed("0.5.0", 1), id, null, fresh)
        assertFalse(d.update)
        assertEquals("channel \"stable\" has nothing for android", d.reason)
    }

    @Test
    fun skippedWaitsForANewerRelease() {
        assertFalse(decide(manifest(), "stable", Installed("0.2.0", 2), id, "0.2.1", fresh).update)
        assertTrue(decide(manifest(stable = "0.2.2", stableCode = 4), "stable", Installed("0.2.0", 2), id, "0.2.1", fresh).update)
        // critical overrides a skip; so does min_version
        val critical = Manifest.parse(
            """{"spec":1,"project":"etchosts","published":"2026-10-09T10:00:00Z","channels":{"stable":"0.2.1"},
               "releases":{"0.2.1":{"critical":true,"assets":{"android":{"url":"a.apk","sha256":"$zeros","size":10,"version_code":3}}}}}""",
        )
        assertTrue(decide(critical, "stable", Installed("0.2.0", 2), id, "0.2.1", fresh).update)
        assertTrue(decide(manifest(extra = "\"min_version\":\"0.2.1\","), "stable", Installed("0.2.0", 2), id, "0.2.1", fresh).update)
    }

    @Test
    fun staleManifestOnlyServesTheFloor() {
        val stale = manifest(extra = "\"expires\":\"2026-10-10T00:00:00Z\",")
        val d = decide(stale, "stable", Installed("0.2.0", 2), id, null, fresh)
        assertFalse(d.update)
        assertTrue(d.reason, d.reason.contains("stale"))
        val floor = manifest(extra = "\"expires\":\"2026-10-10T00:00:00Z\",\"min_version\":\"0.2.1\",")
        val f = decide(floor, "stable", Installed("0.2.0", 2), id, null, fresh)
        assertTrue(f.update)
        assertTrue(f.belowFloor)
        assertTrue(f.reason, f.reason.endsWith("below min_version 0.2.1"))
    }

    @Test
    fun rolloutKeyedOnTheInstallId() {
        val m = Manifest.parse(
            """{"spec":1,"project":"etchosts","published":"2026-10-09T10:00:00Z","channels":{"stable":"0.2.1"},
               "releases":{"0.2.1":{"rollout":10,"assets":{"android":{"url":"a.apk","sha256":"$zeros","size":10,"version_code":3}}}}}""",
        )
        val outside = byteArrayOf(0, 0, 0, 99, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
        assertTrue(decide(m, "stable", Installed("0.2.0", 2), id, null, fresh).update)
        val d = decide(m, "stable", Installed("0.2.0", 2), outside, null, fresh)
        assertFalse(d.update)
        assertTrue(d.reason, d.reason.contains("outside the 10% rollout"))
    }

    @Test
    fun aVPrefixOnTheRunningVersionIsTolerated() {
        assertFalse(decide(manifest(), "stable", Installed("v0.2.1", 3), id, null, fresh).update)
    }
}
