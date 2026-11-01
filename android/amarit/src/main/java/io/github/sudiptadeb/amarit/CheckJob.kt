package io.github.sudiptadeb.amarit

import android.app.job.JobParameters
import android.app.job.JobService
import android.util.Log

/**
 * The scheduled check. It runs in whatever process the system wakes, so
 * the updater is rebuilt from what the last start wrote down.
 */
class CheckJob : JobService() {
    override fun onStartJob(params: JobParameters): Boolean {
        val u = Amarit.restore(this) ?: return false
        if (!u.autoUpdate) {
            u.cancelSchedule()
            return false
        }
        Thread({
            try {
                val d = u.check()
                if (d.update) Notify.available(this, u, d)
            } catch (e: Exception) {
                Log.w(Amarit.TAG, "scheduled check: $e")
            }
            jobFinished(params, false)
        }, "amarit-check").start()
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean = false
}
