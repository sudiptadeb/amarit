# Changelog

## v0.5.0

amarit uses amarit: the daemon keeps itself current. And a service can be
updated from the machine it is built on: a local release directory, one
command that brings every unit onto it, rollback when the restart fails.

- **A project may make auto-update its default.** `"auto_update": true` in
  `amarit.json` (or `amarit.AutoUpdate(true)` in code) turns the loop on for
  every install of that program; its users opt out with `--no-auto-update`
  or `AMARIT_AUTO_UPDATE=0`. Resolution follows the usual layers, later
  winning: project default or the daemon's `AMARIT_MANAGED=1`, then
  `AMARIT_AUTO_UPDATE=1|0`, then `--auto-update`/`--no-auto-update`. Nothing
  is detected. The loop logs that it is on.
- **The daemon updates itself.** amarit's own `amarit.json` declares
  auto-update, so a release build of `amarit daemon` checks amarit's
  releases hourly and execs the new version in place: same pid, launchd and
  systemd see nothing, the units stay its children and are adopted with
  their pids; three failed starts roll it back. `AMARIT_AUTO_UPDATE=0` in
  the job's environment or `amarit daemon --no-auto-update` turns it off.
  Only the daemon checks; a `go install` build has no stamp and never does.
  The startup line now carries the daemon's version.
- **A rolled-back version is not retried.** The rollback records the
  rejected version in the sidecar and later checks skip it until the channel
  points elsewhere; before, a bad release was downloaded again every
  interval.
- Fixed: after the daemon's in-place restart its units were still its
  children, and the adopted-process watcher polled `kill(pid, 0)`, which a
  zombie answers, so a unit that exited would have looked alive forever.
  The watcher reaps its own children and reports their real exit status.
- **`amarit update [-check] [-rolling] [unit|glob ...]`.** Runs each
  program's own `<binary> update` once per binary, however many units share
  it, then restarts the units whose binary changed; `-rolling` one instance
  at a time. No names means every unit whose program was built with amarit;
  `-check` only reports. If the restart fails, the previous binary is put
  back, the units are restarted, and the rejected binary is kept as
  `<bin>.amarit/bad`.
- **Local manifests.** `releases` in `amarit.json` (and `--update-url`) may
  be `file://…` or a plain path; an asset's `url` may be relative to the
  manifest, so `releases.json` can sit in a directory beside its assets.
  `release.url` is optional and defaults to the asset beside the manifest.
- **`<program> update --check`** says what is available and applies nothing.
- **One update at a time per binary.** Processes that share a binary (the
  instances of a unit) take turns on a lock in the sidecar; the later ones
  find the version in place and only restart into it. Before, the race could
  overwrite the previous binary kept for rollback.
- Fixed: `amarit stamp` printed the wrong linker variable, so a stamped
  build carried no configuration. Fixed: `-config` defaulted to
  `amarit.config`, and `release -out` to `amarit.json`, which would have
  overwritten the project file; they are `amarit.json` and `releases.json`.

## v0.4.0

The daemon grows up to a real service stack: a web server, identical workers,
a model server that owns a large child process.

- **Process-group stop.** A stop or restart sends SIGTERM to the unit's whole
  process group, waits the unit's `stop_timeout` (default `10s`), then
  SIGKILLs what is left. Children a crashed program leaves behind, and
  processes a new daemon adopted, are stopped the same way.
- **Resource limits.** `"limits": {"nofile": 65536}` raises the open-files
  limit for that program. The daemon starts it through `amarit __exec`, which
  sets the limit and execs the binary, so the pid stays the program's. The
  launchd job `amarit install` writes now raises its own soft limit to 65536.
- **Instances.** `"instances": 8` (or `amarit run -instances 8`) runs eight
  copies named `<name>@1` .. `<name>@8`, each with `AMARIT_INSTANCE=<i>` and
  `{instance}` expanded in args and env values. `stop`, `start`, `restart`
  and `logs` take the unit or one `<name>@<i>`.
- **`amarit restart <unit|glob>... [-rolling]`.** Restarts units by name or
  pattern through the daemon; the backoff is reset and the restart counter is
  not touched. `-rolling` does one at a time and waits for each to stay up.
- **Double-supervision guard.** `amarit run` and `amarit start` refuse a
  binary that a launchd job or systemd unit already runs, name it with the
  command that removes it, and take `-force` to override.
- **Value handoff.** `amarit.HandoffValues` carries small named strings
  across the in-place restart beside the descriptors; `amarit.InheritedValues`
  reads them once in the new process.
- **Log rotation while running.** A unit's log is rotated at 10 MB while the
  program runs (copy, then truncate in place), one previous file kept.
- Fixed: a unit with `"restart": "never"` was re-run every poll after it
  exited. Fixed: the Windows build of the CLI.

## Earlier

- v0.3.1: programs set their own update interval in `amarit.json`.
- v0.3.0: descriptor handoff across the restart; auto-update under the daemon.
- v0.2.1: `go install` instructions; version from module info.
- v0.2.0: renamed to amarit; flatter manifest; one-call API; the daemon.
- v0.1.2: `update` exits non-zero when the check or apply failed.
- v0.1.1: the one-shot `update` subcommand.
- v0.1.0: hash-tier updates, in-place exec, rollback; release CLI.
