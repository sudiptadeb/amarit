package io.github.sudiptadeb.amarit

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File

class ProjectConfigTest {
    @Test
    fun readsTheRepositoryFile() {
        val c = ProjectConfig.parse(File("../../amarit.json").readText())
        assertEquals("amarit", c.project)
        assertTrue(c.releases.startsWith("https://github.com/sudiptadeb/amarit/"))
        assertTrue(c.autoUpdate)
        assertNull(c.intervalMs)
        assertNull(c.key)
    }

    @Test
    fun fields() {
        val c = ProjectConfig.parse("""{"project":"app","releases":"r.json","interval":"5m","key":"RWQ"}""")
        assertEquals(5 * 60_000L, c.intervalMs)
        assertEquals("RWQ", c.key)
        assertFalse(c.autoUpdate)
        for (bad in listOf("{", "{}", """{"project":"a"}""", """{"project":"a","releases":"r","interval":"soon"}""", """{"project":"a","releases":"r","interval":"0s"}""")) {
            try {
                ProjectConfig.parse(bad)
                fail("$bad: want an error")
            } catch (e: IllegalArgumentException) {
                assertTrue(e.message!!.startsWith("amarit.json:"))
            }
        }
    }

    @Test
    fun goDurations() {
        assertEquals(3_600_000L, ProjectConfig.parseDuration("1h"))
        assertEquals(5_400_000L, ProjectConfig.parseDuration("1h30m"))
        assertEquals(90_000L, ProjectConfig.parseDuration("90s"))
        assertEquals(1_500L, ProjectConfig.parseDuration("1.5s"))
        assertEquals(250L, ProjectConfig.parseDuration("250ms"))
        assertNull(ProjectConfig.parseDuration("5"))
        assertNull(ProjectConfig.parseDuration("5 m"))
        assertNull(ProjectConfig.parseDuration("-5m"))
        assertNull(ProjectConfig.parseDuration("0s"))
        assertNull(ProjectConfig.parseDuration(""))
    }
}
