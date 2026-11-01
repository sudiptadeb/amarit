package io.github.sudiptadeb.amarit

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.os.Build
import android.util.Log
import android.widget.Toast
import java.io.File

/** The hand-off to PackageInstaller: one session, one file, one commit. */
internal object Installer {
    const val ACTION_RESULT = "io.github.sudiptadeb.amarit.INSTALL_RESULT"
    const val EXTRA_VERSION = "version"

    fun install(context: Context, apk: File, version: String) {
        val pi = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
            setSize(apk.length())
            if (Build.VERSION.SDK_INT >= 31) setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_REQUIRED)
        }
        val id = pi.createSession(params)
        val session = pi.openSession(id)
        try {
            session.openWrite("base.apk", 0, apk.length()).use { out ->
                apk.inputStream().use { it.copyTo(out) }
                session.fsync(out)
            }
            val intent = Intent(context, InstallReceiver::class.java)
                .setAction(ACTION_RESULT)
                .putExtra(EXTRA_VERSION, version)
            var flags = PendingIntent.FLAG_UPDATE_CURRENT
            if (Build.VERSION.SDK_INT >= 31) flags = flags or PendingIntent.FLAG_MUTABLE // the system fills in the status
            val pending = PendingIntent.getBroadcast(context, id, intent, flags)
            Log.i(Amarit.TAG, "handing $version to the package installer")
            session.commit(pending.intentSender)
        } catch (e: Exception) {
            session.abandon()
            throw e
        } finally {
            session.close()
        }
    }
}

/**
 * Where PackageInstaller reports: the confirmation it needs the user for,
 * then success or a failure. Success means this process is about to be
 * replaced; the next [Amarit.start] sees the attempt and logs it.
 */
class InstallReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, -1)
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: ""
        val version = intent.getStringExtra(Installer.EXTRA_VERSION) ?: ""
        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                val confirm = if (Build.VERSION.SDK_INT >= 33) {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                } else {
                    @Suppress("DEPRECATION") intent.getParcelableExtra(Intent.EXTRA_INTENT)
                }
                if (confirm != null) {
                    confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    context.startActivity(confirm)
                }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                Log.i(Amarit.TAG, "$version installed")
                Notify.cancel(context)
            }
            PackageInstaller.STATUS_FAILURE_ABORTED -> {
                Log.i(Amarit.TAG, "$version: the user declined")
                Updater.prefs(context).edit().remove(Updater.KEY_ATTEMPT).apply()
            }
            else -> {
                val why = if (status == PackageInstaller.STATUS_FAILURE_INCOMPATIBLE) {
                    context.getString(R.string.amarit_incompatible)
                } else {
                    message.ifEmpty { "status $status" }
                }
                Log.w(Amarit.TAG, "$version failed to install: $status $message")
                Updater.prefs(context).edit().remove(Updater.KEY_ATTEMPT).apply()
                Toast.makeText(context, "${context.getString(R.string.amarit_failed)}: $why", Toast.LENGTH_LONG).show()
                Notify.failed(context, why)
            }
        }
    }
}
