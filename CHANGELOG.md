# Changelog

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
