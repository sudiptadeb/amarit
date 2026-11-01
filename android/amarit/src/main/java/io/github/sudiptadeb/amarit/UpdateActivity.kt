package io.github.sudiptadeb.amarit

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import android.util.Log
import android.view.Gravity
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import java.io.File

/**
 * The default prompt: a dialog over a translucent activity, started from the
 * notification or from [Updater.promptIfAvailable]. Install, Later, or Skip
 * this version; then the download with progress, then the installer's own
 * confirmation. An app that wants its own UI uses [Updater] directly and
 * never sees this.
 */
class UpdateActivity : Activity() {
    private lateinit var updater: Updater
    private var decision: Decision? = null
    private var dialog: AlertDialog? = null
    private var awaitingPermission = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val u = Amarit.restore(this)
        if (u == null) {
            finish()
            return
        }
        updater = u
        val wanted = intent.getStringExtra(EXTRA_VERSION)
        val last = u.lastDecision
        if (last != null && last.update && (wanted == null || last.version == wanted)) {
            decision = last
            offer(last)
        } else {
            u.checkAsync { r ->
                val d = r.getOrNull()
                if (d == null || !d.update) {
                    if (d != null) toast(getString(R.string.amarit_up_to_date, u.config.project, u.installed.versionName))
                    finish()
                } else {
                    decision = d
                    offer(d)
                }
            }
        }
    }

    override fun onResume() {
        super.onResume()
        if (awaitingPermission) {
            awaitingPermission = false
            if (updater.canInstall()) decision?.let { downloadAndInstall(it) } else finish()
        }
    }

    override fun onDestroy() {
        dialog?.dismiss()
        super.onDestroy()
    }

    private fun offer(d: Decision) {
        if (isFinishing) return
        val version = d.version ?: return finish()
        val asset = d.asset ?: return finish()
        val text = StringBuilder(getString(R.string.amarit_size, "%.1f".format(asset.size / 1048576.0)))
        if (d.release?.critical == true) text.append("\n\n").append(getString(R.string.amarit_critical))
        if (d.belowFloor) text.append("\n\n").append(getString(R.string.amarit_unsupported))
        d.manifest.notes?.let { text.append("\n\n").append(it) }
        val b = AlertDialog.Builder(this, android.R.style.Theme_DeviceDefault_Light_Dialog_Alert)
            .setTitle(getString(R.string.amarit_available, updater.config.project, version))
            .setMessage(text)
            .setPositiveButton(R.string.amarit_install) { _, _ -> startInstall(d) }
            .setNegativeButton(R.string.amarit_later) { _, _ -> finish() }
            .setOnCancelListener { finish() }
        if (d.release?.critical != true && !d.belowFloor) {
            b.setNeutralButton(R.string.amarit_skip) { _, _ ->
                updater.skip(version)
                finish()
            }
        }
        dialog = b.show()
    }

    private fun startInstall(d: Decision) {
        if (!updater.hasInstallPermission()) {
            fail("the app does not declare android.permission.REQUEST_INSTALL_PACKAGES")
            return
        }
        if (updater.canInstall()) {
            downloadAndInstall(d)
            return
        }
        val label = applicationInfo.loadLabel(packageManager)
        dialog = AlertDialog.Builder(this, android.R.style.Theme_DeviceDefault_Light_Dialog_Alert)
            .setTitle(R.string.amarit_allow_title)
            .setMessage(getString(R.string.amarit_allow_text, label))
            .setPositiveButton(R.string.amarit_continue) { _, _ ->
                awaitingPermission = true
                startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName")))
            }
            .setNegativeButton(R.string.amarit_later) { _, _ -> finish() }
            .setOnCancelListener { finish() }
            .show()
    }

    private fun downloadAndInstall(d: Decision) {
        val version = d.version ?: return finish()
        val pad = (20 * resources.displayMetrics.density).toInt()
        val bar = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max = 1000 }
        val label = TextView(this).apply { gravity = Gravity.CENTER_HORIZONTAL }
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, 0)
            addView(label)
            addView(bar)
        }
        dialog = AlertDialog.Builder(this, android.R.style.Theme_DeviceDefault_Light_Dialog_Alert)
            .setTitle(getString(R.string.amarit_downloading, version))
            .setView(box)
            .setCancelable(false)
            .show()
        Thread({
            try {
                val file: File = updater.download(d) { done, total ->
                    runOnUiThread {
                        bar.progress = (done * 1000 / total).toInt()
                        label.text = "%.1f / %.1f MB".format(done / 1048576.0, total / 1048576.0)
                    }
                }
                updater.install(file, d)
                runOnUiThread { finish() }
            } catch (e: Exception) {
                Log.w(Amarit.TAG, "update: $e")
                runOnUiThread { fail(e.message ?: e.toString()) }
            }
        }, "amarit-download").start()
    }

    private fun fail(why: String) {
        if (isFinishing) return
        dialog?.dismiss()
        dialog = AlertDialog.Builder(this, android.R.style.Theme_DeviceDefault_Light_Dialog_Alert)
            .setTitle(R.string.amarit_failed)
            .setMessage(why)
            .setPositiveButton(android.R.string.ok) { _, _ -> finish() }
            .setOnCancelListener { finish() }
            .show()
    }

    private fun toast(s: String) = android.widget.Toast.makeText(this, s, android.widget.Toast.LENGTH_SHORT).show()

    companion object {
        const val EXTRA_VERSION = "version"

        fun intent(context: Context, version: String): Intent =
            Intent(context, UpdateActivity::class.java)
                .putExtra(EXTRA_VERSION, version)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)

        /** Shows the prompt for a decision this process already has. */
        fun show(activity: Activity, d: Decision) {
            val v = d.version ?: return
            activity.startActivity(Intent(activity, UpdateActivity::class.java).putExtra(EXTRA_VERSION, v))
        }
    }
}
