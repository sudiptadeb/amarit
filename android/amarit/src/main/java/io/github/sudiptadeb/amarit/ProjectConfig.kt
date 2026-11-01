package io.github.sudiptadeb.amarit

import org.json.JSONException
import org.json.JSONObject

/**
 * amarit.json at the project's repository root: the file `amarit release`
 * reads to write the manifest, carried into the APK as an asset so the app
 * hardcodes nothing. docs/spec.md section 8. Only the fields a consumer
 * needs are read; the service and release sections are the Go side's.
 */
class ProjectConfig(
    val project: String,
    val releases: String,
    val key: String?,
    /** `interval` in milliseconds, or null when the file does not say. */
    val intervalMs: Long?,
    /** `auto_update`: true makes the prompt the app's default; false or absent leaves the layer below. */
    val autoUpdate: Boolean,
) {
    companion object {
        /** Decodes and validates an amarit.json; throws [IllegalArgumentException] like ParseProjectConfig errors. */
        fun parse(text: String): ProjectConfig {
            val o = try {
                JSONObject(text)
            } catch (e: JSONException) {
                throw IllegalArgumentException("amarit.json: ${e.message}")
            }
            val project = o.optString("project", "")
            if (project.isEmpty()) throw IllegalArgumentException("amarit.json: project is required")
            val releases = o.optString("releases", "")
            if (releases.isEmpty()) throw IllegalArgumentException("amarit.json: releases is required")
            val interval = o.optString("interval", "")
            val intervalMs = if (interval.isEmpty()) null else parseDuration(interval)
                ?: throw IllegalArgumentException("amarit.json: interval \"$interval\" must be a positive duration like 5m or 6h")
            return ProjectConfig(
                project = project,
                releases = releases,
                key = o.optString("key", "").ifEmpty { null },
                intervalMs = intervalMs,
                autoUpdate = o.optBoolean("auto_update", false),
            )
        }

        private val durationPart = Regex("""(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)""")

        /**
         * A Go duration ("1h30m", "5m", "90s") in milliseconds, or null when the
         * string is not one or is not positive. Sub-millisecond units round down.
         */
        fun parseDuration(s: String): Long? {
            if (s.isEmpty()) return null
            var rest = s
            var total = 0.0
            while (rest.isNotEmpty()) {
                val m = durationPart.matchAt(rest, 0) ?: return null
                val n = m.groupValues[1].toDouble()
                total += when (m.groupValues[2]) {
                    "ns" -> n / 1_000_000
                    "us", "µs" -> n / 1_000
                    "ms" -> n
                    "s" -> n * 1_000
                    "m" -> n * 60_000
                    else -> n * 3_600_000
                }
                rest = rest.substring(m.range.last + 1)
            }
            val ms = total.toLong()
            return if (ms > 0) ms else null
        }
    }
}
