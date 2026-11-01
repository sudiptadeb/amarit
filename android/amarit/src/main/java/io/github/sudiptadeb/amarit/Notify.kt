package io.github.sudiptadeb.amarit

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context

/** The "update available" notification, on its own channel so the app's own channels stay the app's. */
internal object Notify {
    const val CHANNEL = "amarit_updates"
    const val ID = 0x414D

    private fun manager(context: Context): NotificationManager {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, context.getString(R.string.amarit_channel_name), NotificationManager.IMPORTANCE_DEFAULT),
        )
        return nm
    }

    private fun icon(u: Updater?): Int = u?.config?.icon?.takeIf { it != 0 } ?: android.R.drawable.stat_sys_download_done

    fun available(context: Context, u: Updater, d: Decision) {
        val version = d.version ?: return
        val open = PendingIntent.getActivity(
            context, 0, UpdateActivity.intent(context, version),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val n = Notification.Builder(context, CHANNEL)
            .setSmallIcon(icon(u))
            .setContentTitle(context.getString(R.string.amarit_available, u.config.project, version))
            .setContentText(context.getString(R.string.amarit_tap))
            .setContentIntent(open)
            .setAutoCancel(true)
            .setOnlyAlertOnce(true)
            .build()
        manager(context).notify(ID, n)
    }

    fun failed(context: Context, why: String) {
        val n = Notification.Builder(context, CHANNEL)
            .setSmallIcon(icon(Amarit.current))
            .setContentTitle(context.getString(R.string.amarit_failed))
            .setContentText(why)
            .setStyle(Notification.BigTextStyle().bigText(why))
            .setAutoCancel(true)
            .build()
        manager(context).notify(ID, n)
    }

    fun cancel(context: Context) {
        context.getSystemService(NotificationManager::class.java)?.cancel(ID)
    }
}
