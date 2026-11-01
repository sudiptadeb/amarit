package io.github.sudiptadeb.amarit

import android.app.Activity
import android.app.job.JobInfo
import android.app.job.JobScheduler
import android.content.ComponentName
import android.content.Context
import android.content.SharedPreferences
import android.content.pm.PackageManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.security.MessageDigest
import java.security.SecureRandom
import java.time.Instant
import java.util.concurrent.Executors

/**
 * The engine behind [Amarit.start]: one per process. Checks the manifest,
 * decides, downloads and verifies, hands the APK to the installer. Every
 * step before the hand-off fails closed and leaves the installed app alone.
 *
 * State lives in the SharedPreferences file "amarit", the twin of the Go
 * sidecar: the install id, the user's choices, the attempted version.
 */
class Updater internal constructor(private val context: Context, val config: Amarit.Config) {
    internal val prefs: SharedPreferences = prefs(context)
    private val main = Handler(Looper.getMainLooper())
    private val worker = Executors.newSingleThreadExecutor { r -> Thread(r, "amarit") }

    /** The running version, from the package manager. */
    val installed: Installed = context.packageManager.getPackageInfo(context.packageName, 0).let {
        val code = if (Build.VERSION.SDK_INT >= 28) it.longVersionCode else @Suppress("DEPRECATION") it.versionCode.toLong()
        Installed(it.versionName ?: "", code)
    }

    /** The last check's outcome in this process, for the prompt to reuse. */
    @Volatile
    var lastDecision: Decision? = null
        internal set

    /**
     * Whether the scheduled check and the prompt are on. The project's
     * `auto_update` is the default; the user's switch, stored here, wins.
     * Setting it schedules or cancels the periodic check.
     */
    var autoUpdate: Boolean
        get() = when (prefs.getString(KEY_AUTO_UPDATE, "")) {
            "1" -> true
            "0" -> false
            else -> config.autoUpdate
        }
        set(on) {
            prefs.edit().putString(KEY_AUTO_UPDATE, if (on) "1" else "0").apply()
            if (on) schedule() else cancelSchedule()
        }

    /** The channel this install follows: the user's pick, else the build's. */
    var channel: String
        get() = prefs.getString(KEY_CHANNEL, null)?.ifEmpty { null } ?: config.channel
        set(name) {
            prefs.edit().putString(KEY_CHANNEL, name).apply()
            lastDecision = null
        }

    /** The fixed random id a rollout percentage is keyed on, created on first use. */
    val installId: ByteArray
        get() {
            prefs.getString(KEY_ID, null)?.let { hex -> if (hex.length == 32) return hex.chunked(2).map { it.toInt(16).toByte() }.toByteArray() }
            val b = ByteArray(16).also { SecureRandom().nextBytes(it) }
            prefs.edit().putString(KEY_ID, b.joinToString("") { "%02x".format(it) }).apply()
            return b
        }

    /** True when the configured interval has passed since the last check. */
    fun due(): Boolean = System.currentTimeMillis() - prefs.getLong(KEY_LAST_CHECK, 0) >= config.intervalMs

    /**
     * After an install the process is new; the version it attempted either
     * is the one running now, or the install did not go through. Either way
     * the attempt is over. The twin of engine.go's reconcile, minus rollback.
     */
    internal fun reconcile() {
        val attempt = prefs.getString(KEY_ATTEMPT, null) ?: return
        if (attempt == installed.versionName) {
            Log.i(Amarit.TAG, "updated to $attempt")
        } else {
            Log.i(Amarit.TAG, "attempted $attempt but running ${installed.versionName}; the install did not go through")
        }
        prefs.edit().remove(KEY_ATTEMPT).apply()
        File(context.filesDir, DIR).listFiles()?.forEach { it.delete() }
    }

    /**
     * One check: fetch the manifest, validate it, decide. Blocking; call it
     * off the main thread. Throws [IOException] when the manifest cannot be
     * fetched and [IllegalArgumentException] when it is not acceptable.
     */
    fun check(): Decision {
        prefs.edit().putLong(KEY_LAST_CHECK, System.currentTimeMillis()).apply()
        val body = open(config.releases).use { c ->
            c.inputStream.use { it.readLimited(MAX_MANIFEST) }
        }
        val m = Manifest.parse(String(body, Charsets.UTF_8))
        if (m.project != config.project) {
            throw IllegalArgumentException("manifest is for \"${m.project}\", this is \"${config.project}\"")
        }
        val d = decide(m, channel, installed, installId, prefs.getString(KEY_SKIPPED, null), Instant.now())
        Log.i(Amarit.TAG, d.reason)
        lastDecision = d
        return d
    }

    /** [check] on the worker thread, the result on the main thread. */
    fun checkAsync(onResult: (Result<Decision>) -> Unit) {
        worker.execute {
            val r = try {
                Result.success(check())
            } catch (e: Exception) {
                Log.w(Amarit.TAG, "check: $e")
                Result.failure(e)
            }
            main.post { onResult(r) }
        }
    }

    /** The app's own "Check for updates" button: a check now, and the dialog when there is something. */
    fun checkNow(activity: Activity, onResult: ((Result<Decision>) -> Unit)? = null) {
        checkAsync { r ->
            r.getOrNull()?.let { if (it.update && !activity.isFinishing) UpdateActivity.show(activity, it) }
            onResult?.invoke(r)
        }
    }

    /** Runs a check and, when an update is available, shows the dialog over `activity`. */
    fun promptIfAvailable(activity: Activity) {
        checkAsync { r -> r.getOrNull()?.let { if (it.update && !activity.isFinishing) UpdateActivity.show(activity, it) } }
    }

    /** Runs a check and, when an update is available, posts the notification. */
    fun notifyIfAvailable() {
        checkAsync { r -> r.getOrNull()?.let { if (it.update) Notify.available(context, this, it) } }
    }

    /** The user's "skip this version": not offered again unless it is critical or below min_version. */
    fun skip(version: String) {
        prefs.edit().putString(KEY_SKIPPED, version).apply()
        Notify.cancel(context)
    }

    /**
     * Downloads the decided asset into the app's private files, hashing as
     * it streams, cut off past the manifest's size, and verifies sha256 and
     * size before returning the file. Blocking. A file already there and
     * verified is returned as is.
     */
    fun download(d: Decision, progress: ((done: Long, total: Long) -> Unit)? = null): File {
        val asset = d.asset ?: throw IllegalStateException("nothing to download")
        val version = d.version ?: throw IllegalStateException("nothing to download")
        val dir = File(context.filesDir, DIR).also { it.mkdirs() }
        val file = File(dir, "$version.apk")
        if (file.length() == asset.size && sha256(file) == asset.sha256) return file
        dir.listFiles()?.forEach { it.delete() }

        val url = URI(config.releases).resolve(asset.url).toString()
        val tmp = File(dir, "$version.apk.part")
        val md = MessageDigest.getInstance("SHA-256")
        var total = 0L
        open(url).use { c ->
            c.inputStream.use { input ->
                tmp.outputStream().use { out ->
                    val buf = ByteArray(64 * 1024)
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        total += n
                        if (total > asset.size) {
                            tmp.delete()
                            throw IOException("$url is larger than the manifest says (${asset.size} bytes)")
                        }
                        md.update(buf, 0, n)
                        out.write(buf, 0, n)
                        progress?.invoke(total, asset.size)
                    }
                    out.fd.sync()
                }
            }
        }
        if (total != asset.size) {
            tmp.delete()
            throw IOException("$url: got $total bytes, manifest says ${asset.size}")
        }
        val sum = md.digest().joinToString("") { "%02x".format(it) }
        if (sum != asset.sha256) {
            tmp.delete()
            throw IOException("$url: sha256 $sum does not match the manifest")
        }
        if (!tmp.renameTo(file)) throw IOException("cannot rename ${tmp.name}")
        return file
    }

    /**
     * Hands a verified APK to PackageInstaller. Android asks the user to
     * confirm; the result arrives in [InstallReceiver]. The app must
     * declare REQUEST_INSTALL_PACKAGES and the user must have allowed it
     * ([canInstall]); otherwise this throws before a session is opened.
     */
    fun install(apk: File, d: Decision) {
        val version = d.version ?: throw IllegalStateException("nothing to install")
        if (!hasInstallPermission()) {
            throw IllegalStateException("the app does not declare android.permission.REQUEST_INSTALL_PACKAGES")
        }
        if (!canInstall()) throw IllegalStateException("installing from this app is not allowed yet; see Settings")
        prefs.edit().putString(KEY_ATTEMPT, version).apply()
        Installer.install(context, apk, version)
    }

    /** Whether the user has allowed this app to install packages (Settings, "Install unknown apps"). */
    fun canInstall(): Boolean = context.packageManager.canRequestPackageInstalls()

    internal fun hasInstallPermission(): Boolean {
        val info = context.packageManager.getPackageInfo(context.packageName, PackageManager.GET_PERMISSIONS)
        return info.requestedPermissions?.contains("android.permission.REQUEST_INSTALL_PACKAGES") == true
    }

    /**
     * Ensures the periodic check is scheduled with the configured interval;
     * leaves an existing one's clock alone. The updater is never the reason
     * an app cannot open: a scheduler that refuses is logged, and the check
     * on open still runs.
     */
    internal fun schedule() {
        try {
            val js = context.getSystemService(JobScheduler::class.java) ?: return
            val period = maxOf(config.intervalMs, JobInfo.getMinPeriodMillis())
            val pending = js.getPendingJob(JOB_ID)
            if (pending != null && pending.intervalMillis == period) return
            val job = JobInfo.Builder(JOB_ID, ComponentName(context, CheckJob::class.java))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .setPeriodic(period)
                .build()
            if (js.schedule(job) != JobScheduler.RESULT_SUCCESS) {
                Log.w(Amarit.TAG, "the scheduler refused the periodic check; checking on open only")
                return
            }
            Log.i(Amarit.TAG, "auto-update on: ${config.project} ${installed.versionName}, channel $channel, every ${period / 60000} min")
        } catch (e: RuntimeException) {
            Log.w(Amarit.TAG, "cannot schedule the periodic check: $e; checking on open only")
        }
    }

    internal fun cancelSchedule() {
        try {
            context.getSystemService(JobScheduler::class.java)?.cancel(JOB_ID)
        } catch (e: RuntimeException) {
            Log.w(Amarit.TAG, "cannot cancel the periodic check: $e")
        }
    }

    private fun open(url: String): HttpURLConnection {
        val c = URL(url).openConnection() as HttpURLConnection
        c.connectTimeout = 30_000
        c.readTimeout = 60_000
        c.instanceFollowRedirects = true
        c.setRequestProperty("User-Agent", "amarit/${config.project}/${installed.versionName}")
        val code = c.responseCode
        if (code != HttpURLConnection.HTTP_OK) {
            c.disconnect()
            throw IOException("$url returned $code ${c.responseMessage ?: ""}".trim())
        }
        return c
    }

    private inline fun <T> HttpURLConnection.use(block: (HttpURLConnection) -> T): T = try {
        block(this)
    } finally {
        disconnect()
    }

    private fun java.io.InputStream.readLimited(max: Int): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        val buf = ByteArray(16 * 1024)
        while (true) {
            val n = read(buf)
            if (n < 0) break
            if (out.size() + n > max) throw IOException("manifest is larger than $max bytes")
            out.write(buf, 0, n)
        }
        return out.toByteArray()
    }

    private fun sha256(f: File): String {
        if (!f.isFile) return ""
        val md = MessageDigest.getInstance("SHA-256")
        f.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buf)
                if (n < 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    companion object {
        internal const val PREFS = "amarit"
        internal const val DIR = "amarit"
        internal const val KEY_ID = "id"
        internal const val KEY_AUTO_UPDATE = "auto_update"
        internal const val KEY_CHANNEL = "channel"
        internal const val KEY_SKIPPED = "skipped"
        internal const val KEY_ATTEMPT = "attempt"
        internal const val KEY_LAST_CHECK = "last_check"
        /** 'AMAR', so it never meets an app's own job ids. */
        internal const val JOB_ID = 0x414D4152
        private const val MAX_MANIFEST = 1 shl 20

        internal fun prefs(context: Context): SharedPreferences =
            context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
    }
}
