package io.github.sudiptadeb.amarit

import java.time.Instant

/** Semantic version order, the twin of version.go. */
object Versions {
    /**
     * Orders two semantic versions without a leading "v": -1 when a < b, 0
     * when equal, 1 when a > b. Pre-release versions sort before the release
     * they precede; build metadata after "+" is ignored. Throws
     * [IllegalArgumentException] for anything that is not major.minor.patch.
     */
    fun compare(a: String, b: String): Int {
        val pa = parse(a)
        val pb = parse(b)
        for (i in 0 until 3) {
            if (pa.nums[i] != pb.nums[i]) return if (pa.nums[i] < pb.nums[i]) -1 else 1
        }
        return when {
            pa.pre.isEmpty() && pb.pre.isEmpty() -> 0
            pa.pre.isEmpty() -> 1
            pb.pre.isEmpty() -> -1
            else -> comparePrerelease(pa.pre, pb.pre)
        }
    }

    private class Semver(val nums: IntArray, val pre: String)

    private fun parse(s: String): Semver {
        var core = s
        core.indexOf('+').let { if (it >= 0) core = core.substring(0, it) }
        var pre = ""
        core.indexOf('-').let {
            if (it >= 0) {
                pre = core.substring(it + 1)
                core = core.substring(0, it)
            }
        }
        val parts = core.split(".")
        if (parts.size != 3) throw IllegalArgumentException("version \"$s\": want major.minor.patch")
        val nums = IntArray(3)
        for ((i, p) in parts.withIndex()) {
            val n = p.toIntOrNull()
            if (n == null || n < 0 || (p.length > 1 && p[0] == '0') || p.any { !it.isDigit() }) {
                throw IllegalArgumentException("version \"$s\": \"$p\" is not a valid number")
            }
            nums[i] = n
        }
        return Semver(nums, pre)
    }

    private fun comparePrerelease(a: String, b: String): Int {
        val xs = a.split(".")
        val ys = b.split(".")
        for (i in 0 until minOf(xs.size, ys.size)) {
            val xn = xs[i].toIntOrNull()
            val yn = ys[i].toIntOrNull()
            when {
                xn != null && yn != null -> if (xn != yn) return if (xn < yn) -1 else 1
                xn != null -> return -1 // numeric identifiers sort before alphanumeric ones
                yn != null -> return 1
                else -> xs[i].compareTo(ys[i]).let { if (it != 0) return if (it < 0) -1 else 1 }
            }
        }
        return xs.size.compareTo(ys.size).coerceIn(-1, 1)
    }
}

/** What the package manager says this app is: the running version, no stamp needed. */
class Installed(val versionName: String, val versionCode: Long)

/** The outcome of one manifest check, the twin of engine.go's Decision. */
class Decision(
    val manifest: Manifest,
    /** The version the channel points at for android, or null when it has nothing. */
    val version: String?,
    val release: Release?,
    val asset: Asset?,
    /** True when the app should offer the update. */
    val update: Boolean,
    /** Why, in one line, for the log. */
    val reason: String,
    /** The running version is below min_version: the update is offered even from a stale manifest or outside the rollout. */
    val belowFloor: Boolean,
)

/**
 * Decides whether an install takes what a channel points at. On Android the
 * platform compares versionCode, so: a higher code is an update, a lower
 * code never is, and an equal code with a different version string is an
 * update too, since Android installs a rebuild of the same code in place.
 * That last row is what carries a rolling channel whose builds are named
 * `0.2.1+g6c3c4d4` on one versionCode. min_version compares against
 * versionName in semver order. The clauses follow engine.go's Check.
 */
fun decide(
    m: Manifest,
    channel: String,
    installed: Installed,
    installId: ByteArray,
    /** The version the user skipped on this install; not offered again unless critical or below the floor. */
    skipped: String?,
    now: Instant,
): Decision {
    val t = m.lookup(channel, Manifest.TARGET)
        ?: return Decision(m, null, null, null, false, "channel \"$channel\" has nothing for ${Manifest.TARGET}", false)
    val running = installed.versionName.removePrefix("v")
    val belowFloor = m.minVersion?.let { floor ->
        try {
            Versions.compare(running, floor) < 0
        } catch (e: IllegalArgumentException) {
            false
        }
    } ?: false
    val codeCmp = installed.versionCode.compareTo(t.asset.versionCode.toLong())
    val rollout = t.release.rollout
    return when {
        codeCmp == 0 && t.version == running ->
            Decision(m, t.version, t.release, t.asset, false, "already on $running", belowFloor)
        codeCmp > 0 ->
            Decision(m, t.version, t.release, t.asset, false,
                "running $running (${installed.versionCode}) is newer than ${t.version} (${t.asset.versionCode}); not downgrading", belowFloor)
        t.version == skipped && !belowFloor && !t.release.critical ->
            Decision(m, t.version, t.release, t.asset, false, "${t.version} was skipped on this install; waiting for a newer release", belowFloor)
        m.isStale(now) && !belowFloor ->
            Decision(m, t.version, t.release, t.asset, false,
                "manifest expired ${m.expires}; ${t.version} available but not taken from a stale manifest", belowFloor)
        !belowFloor && !Manifest.inRollout(installId, rollout) ->
            Decision(m, t.version, t.release, t.asset, false, "${t.version} available; this install is outside the $rollout% rollout", belowFloor)
        else -> {
            var reason = "${t.version} available (running $running)"
            if (belowFloor) reason += "; below min_version ${m.minVersion}"
            Decision(m, t.version, t.release, t.asset, true, reason, belowFloor)
        }
    }
}
