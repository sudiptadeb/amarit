package io.github.sudiptadeb.amarit

import org.json.JSONException
import org.json.JSONObject
import java.time.Instant
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException

/** One downloadable file for one target. */
class Asset(
    val url: String,
    val sha256: String,
    val size: Long,
    val sig: String?,
    /** The APK versionCode; required on the `android` target, 0 elsewhere. */
    val versionCode: Int,
)

/** One published version: how it rolls out and what to download. */
class Release(
    /** Percentage of installs that take it; null means 100. */
    val rollout: Int?,
    val critical: Boolean,
    val assets: Map<String, Asset>,
)

/**
 * One project's releases.json: which version each channel points at and the
 * assets of every version. The Kotlin twin of manifest.go; docs/spec.md is
 * the contract for both.
 */
class Manifest(
    val spec: Int,
    val project: String,
    val published: Instant,
    val expires: Instant?,
    val minVersion: String?,
    val notes: String?,
    val channels: Map<String, String>,
    val releases: Map<String, Release>,
) {
    /** What a channel points at for one target. */
    class Target(val version: String, val release: Release, val asset: Asset)

    /** A stale manifest still carries min_version, but a client takes no new version from it. */
    fun isStale(now: Instant): Boolean = expires != null && now.isAfter(expires)

    /** Null when the channel is unknown or has nothing for the target: not an error, "nothing for you". */
    fun lookup(channel: String, target: String): Target? {
        val version = channels[channel] ?: return null
        val release = releases[version] ?: return null
        val asset = release.assets[target] ?: return null
        return Target(version, release, asset)
    }

    companion object {
        /** The manifest format this library understands; any other `spec` is refused. */
        const val SPEC = 1

        /** The target an APK is published under. */
        const val TARGET = "android"

        private val exactVersion = Regex("^[A-Za-z0-9._+-]+$")
        private val sha256Hex = Regex("^[0-9a-f]{64}$")

        /**
         * Decodes and validates a manifest, strictly: a channel that names a
         * version with no release, a bad digest, an android asset without
         * version_code or an unknown spec all fail here, before anything is
         * downloaded. Throws [IllegalArgumentException] with the reason.
         */
        fun parse(text: String): Manifest {
            val o = try {
                JSONObject(text)
            } catch (e: JSONException) {
                throw IllegalArgumentException("manifest: ${e.message}")
            }
            val spec = o.optInt("spec", 0)
            if (spec != SPEC) throw IllegalArgumentException("manifest: spec $spec not supported (want $SPEC)")
            val project = o.optString("project", "")
            if (project.isEmpty()) throw IllegalArgumentException("manifest: project is required")
            val published = time(o, "published") ?: throw IllegalArgumentException("manifest: published is required")
            val expires = time(o, "expires")
            val minVersion = o.optString("min_version", "").ifEmpty { null }
            if (minVersion != null && !exactVersion.matches(minVersion)) {
                throw IllegalArgumentException("manifest: min_version \"$minVersion\" has invalid characters")
            }

            val releases = LinkedHashMap<String, Release>()
            val rel = o.optJSONObject("releases") ?: JSONObject()
            for (version in rel.keys()) {
                if (!exactVersion.matches(version)) throw IllegalArgumentException("manifest: version \"$version\" has invalid characters")
                val r = rel.getJSONObject(version)
                val rollout = if (r.has("rollout")) r.getInt("rollout") else null
                if (rollout != null && (rollout < 0 || rollout > 100)) {
                    throw IllegalArgumentException("manifest: $version: rollout $rollout is not within 0-100")
                }
                val assets = LinkedHashMap<String, Asset>()
                val ao = r.optJSONObject("assets") ?: JSONObject()
                for (target in ao.keys()) {
                    val a = ao.getJSONObject(target)
                    val url = a.optString("url", "")
                    if (url.isEmpty()) throw IllegalArgumentException("manifest: $version $target: url is required")
                    val sha = a.optString("sha256", "")
                    if (!sha256Hex.matches(sha)) throw IllegalArgumentException("manifest: $version $target: sha256 must be 64 lowercase hex characters")
                    val size = a.optLong("size", 0)
                    if (size <= 0) throw IllegalArgumentException("manifest: $version $target: size must be positive")
                    val code = a.optInt("version_code", 0)
                    if (target == TARGET && code <= 0) throw IllegalArgumentException("manifest: $version android: version_code is required")
                    assets[target] = Asset(url, sha, size, a.optString("sig", "").ifEmpty { null }, code)
                }
                releases[version] = Release(rollout, r.optBoolean("critical", false), assets)
            }

            val channels = LinkedHashMap<String, String>()
            val ch = o.optJSONObject("channels") ?: JSONObject()
            for (name in ch.keys()) {
                val version = ch.getString(name)
                if (!exactVersion.matches(version)) throw IllegalArgumentException("manifest: channel \"$name\" version \"$version\" has invalid characters")
                if (version !in releases) throw IllegalArgumentException("manifest: channel \"$name\" points at version \"$version\", which has no release")
                channels[name] = version
            }
            if (channels.isEmpty()) throw IllegalArgumentException("manifest: at least one channel is required")

            return Manifest(spec, project, published, expires, minVersion, o.optString("notes", "").ifEmpty { null }, channels, releases)
        }

        private fun time(o: JSONObject, key: String): Instant? {
            val s = o.optString(key, "")
            if (s.isEmpty()) return null
            return try {
                OffsetDateTime.parse(s).toInstant()
            } catch (e: DateTimeParseException) {
                throw IllegalArgumentException("manifest: $key \"$s\" is not an RFC 3339 time")
            }
        }

        /**
         * Whether an install takes a release, from its fixed random id and the
         * release's rollout percentage. The same id stays selected as the
         * percentage rises, so a rollout widens instead of reshuffling.
         */
        fun inRollout(installId: ByteArray, rollout: Int?): Boolean {
            val pct = rollout ?: 100
            if (pct >= 100) return true
            if (pct <= 0 || installId.size < 4) return false
            val n = ((installId[0].toLong() and 0xff) shl 24) or
                ((installId[1].toLong() and 0xff) shl 16) or
                ((installId[2].toLong() and 0xff) shl 8) or
                (installId[3].toLong() and 0xff)
            return n % 100 < pct
        }
    }
}
