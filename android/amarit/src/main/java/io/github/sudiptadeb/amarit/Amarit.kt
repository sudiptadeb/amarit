package io.github.sudiptadeb.amarit

import android.app.Activity
import android.content.Context
import android.content.SharedPreferences
import android.util.Log
import java.io.FileNotFoundException

/**
 * One call in onCreate is the whole integration:
 *
 *     Amarit.start(this)
 *
 * It reads the project's amarit.json from the APK's assets (the build put
 * it there, the way `amarit stamp` puts it into a binary), or the same facts
 * given as options; and does nothing unless the project declared
 * `"auto_update": true` or the user turned the app's update switch on
 * ([Updater.autoUpdate]). Nothing is detected, nothing is guessed: a build
 * with no project or releases URL is inert and says so in the log.
 *
 * The Android twin of the Go library's Start. The platform owns the swap, so
 * there is no exec, no `.prev` and no health rollback here: the library
 * checks, verifies the download and hands the APK to PackageInstaller, and
 * the user taps Install.
 */
object Amarit {
    const val TAG = "amarit"

    /** The asset the build copies amarit.json to. */
    const val PROJECT_ASSET = "amarit.json"

    /** Everything the app told amarit, resolved once; the twin of Config. */
    class Config(
        val project: String,
        val releases: String,
        val key: String?,
        val intervalMs: Long,
        /** The project's default: true means the prompt is on unless the user turns it off. */
        val autoUpdate: Boolean,
        /** The channel this build follows unless the user picks another. */
        val channel: String,
        /** Notification small icon; 0 means a platform drawable. */
        val icon: Int,
        /** The first amarit.json that did not parse, reported once at start. */
        val error: String?,
    )

    /** Mutable form of [Config] while the layers are applied. */
    class Builder {
        var project = ""
        var releases = ""
        var key: String? = null
        var intervalMs: Long = 0
        var autoUpdate = false
        var channel = DEFAULT_CHANNEL
        var icon = 0
        var error: String? = null
    }

    /** Adjusts the configuration; options apply in the order given, over the asset file. */
    fun interface Option {
        fun apply(b: Builder)
    }

    const val DEFAULT_CHANNEL = "stable"
    const val DEFAULT_INTERVAL_MS = 60L * 60 * 1000

    @Volatile
    var current: Updater? = null
        private set

    /** The project name (normally from amarit.json). */
    fun project(name: String) = Option { it.project = name }

    /** The releases.json URL (normally from amarit.json). */
    fun releases(url: String) = Option { it.releases = url }

    /** The author's public key: recorded, not yet verified against (the signed tier follows the roadmap). */
    fun key(k: String) = Option { it.key = k }

    /** How often the scheduled check runs, overriding `interval` in amarit.json. At least 15 minutes, which is what JobScheduler allows. */
    fun interval(ms: Long) = Option { it.intervalMs = ms }

    /** Whether the prompt is this app's default, overriding `auto_update` in amarit.json. */
    fun autoUpdate(on: Boolean) = Option { it.autoUpdate = on }

    /** The channel to follow; "stable" unless said. The user may pick another with [Updater.channel]. */
    fun channel(name: String) = Option { it.channel = name }

    /** The notification's small icon, a drawable of the app. */
    fun icon(resId: Int) = Option { it.icon = resId }

    /** The app's amarit.json as bytes, for an app that carries the file some other way than the asset. */
    fun projectFile(data: ByteArray) = Option { b ->
        try {
            ProjectConfig.parse(String(data, Charsets.UTF_8)).applyTo(b)
        } catch (e: IllegalArgumentException) {
            if (b.error == null) b.error = e.message
        }
    }

    private fun ProjectConfig.applyTo(b: Builder) {
        b.project = project
        b.releases = releases
        if (key != null) b.key = key
        if (intervalMs != null) b.intervalMs = intervalMs
        if (autoUpdate) b.autoUpdate = true
    }

    /**
     * Resolves the configuration and starts what it asks for. Layers, later
     * winning: assets/amarit.json when the build put one there; the options
     * in order; then, inside [Updater], the user's own choices (the update
     * switch, the channel), which are the Android form of the environment
     * and the command line.
     *
     * With auto-update on, a periodic check is scheduled and, when one is
     * due now, run: an available update is shown as a dialog when `context`
     * is an Activity, else as a notification. With it off, nothing runs
     * until the app calls [Updater.checkNow].
     */
    fun start(context: Context, vararg options: Option): Updater {
        val app = context.applicationContext
        val b = Builder()
        try {
            app.assets.open(PROJECT_ASSET).use { ProjectConfig.parse(it.readBytes().toString(Charsets.UTF_8)).applyTo(b) }
        } catch (e: FileNotFoundException) {
            // no asset: the options or nothing
        } catch (e: IllegalArgumentException) {
            b.error = e.message
        }
        for (o in options) o.apply(b)
        val cfg = Config(
            b.project, b.releases, b.key,
            if (b.intervalMs > 0) b.intervalMs else DEFAULT_INTERVAL_MS,
            b.autoUpdate, b.channel.ifEmpty { DEFAULT_CHANNEL }, b.icon, b.error,
        )
        if (cfg.error != null) Log.w(TAG, cfg.error)

        val u = Updater(app, cfg)
        current = u
        persist(u.prefs, cfg)
        u.reconcile()
        if (cfg.project.isEmpty() || cfg.releases.isEmpty()) {
            Log.i(TAG, "no project or releases URL in this build; updates are off")
            return u
        }
        if (cfg.key != null) Log.i(TAG, "a key is configured but signatures are not verified yet; hash tier only")
        if (u.autoUpdate) {
            u.schedule()
            if (u.due()) {
                if (context is Activity) u.promptIfAvailable(context) else u.notifyIfAvailable()
            }
        } else {
            u.cancelSchedule()
        }
        return u
    }

    /**
     * The updater in a process the app's own code has not started yet (the
     * scheduled check, the notification's activity), rebuilt from the
     * configuration the last [start] wrote down. Null when there was none.
     */
    fun restore(context: Context): Updater? {
        current?.let { return it }
        val prefs = Updater.prefs(context)
        val project = prefs.getString("cfg.project", "") ?: ""
        val releases = prefs.getString("cfg.releases", "") ?: ""
        if (project.isEmpty() || releases.isEmpty()) return null
        val cfg = Config(
            project, releases, prefs.getString("cfg.key", null),
            prefs.getLong("cfg.interval", DEFAULT_INTERVAL_MS),
            prefs.getBoolean("cfg.auto_update", false),
            prefs.getString("cfg.channel", DEFAULT_CHANNEL) ?: DEFAULT_CHANNEL,
            prefs.getInt("cfg.icon", 0), null,
        )
        return Updater(context.applicationContext, cfg).also { current = it }
    }

    private fun persist(prefs: SharedPreferences, cfg: Config) {
        prefs.edit()
            .putString("cfg.project", cfg.project)
            .putString("cfg.releases", cfg.releases)
            .putString("cfg.key", cfg.key)
            .putLong("cfg.interval", cfg.intervalMs)
            .putBoolean("cfg.auto_update", cfg.autoUpdate)
            .putString("cfg.channel", cfg.channel)
            .putInt("cfg.icon", cfg.icon)
            .apply()
    }
}
