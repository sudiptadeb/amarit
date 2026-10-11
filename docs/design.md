# amarit: design

This is the condensed project proposal. It records the survey that motivated the
project, the principles, the architecture and the order of work.

## The problem

Every service the author runs is kept alive and updated by hand, each in its own
way, and most do not survive a reboot. A read-only survey of one headless Mac,
one VPS and a few EC2 hosts on 2026-10-09 found eleven kinds of process across
six kinds of supervisor: system LaunchDaemons, tmux panes, `nohup` with a pid
file, systemd with a path unit, `setsid` on EC2, OpenRC without respawn, and
Android foreground services. Three facts set the design:

1. On a headless Mac no user LaunchAgent runs at all, because the account has no
   desktop session. Only system daemons with a `UserName` survive a reboot.
2. Restarting a stateful process has a cost. A terminal server restart closes
   every terminal it holds; a gateway once lost a live fleet to an unbounded
   drain during a re-exec.
3. "Installed is not running" already happens: a CLI's symlink moves to a new
   version while the long-running processes started from it keep the old one,
   and nothing restarts them.

## What exists

Survey as of 2026-10-09. Nothing maintained combines a verified download with a
correct restart under a service manager.

| Family | Examples | Verifies | Restarts the process | Service-aware | Health rollback |
|---|---|---|---|---|---|
| Go libraries | creativeprojects/go-selfupdate, minio/selfupdate, fynelabs/selfupdate | yes | fynelabs only, spawn and exit | no | no |
| Go restart libraries | jpillora/overseer, cloudflare/tableflip | overseer: no | listener fd handoff, Unix only | partial | no |
| In-tree | Syncthing, Tailscale, cloudflared, rclone, restic | yes | exit code + monitor, `systemctl`, MSI | hand-rolled each time | no |
| GUI frameworks | Sparkle, WinSparkle, Tauri, electron-updater, Velopack | yes | relaunch the app | no | no |
| Updater daemons | Google Omaha (archived 2025), Keystone (closed), Chromium updater (in-tree) | yes | app relaunches | yes | no |
| Security frameworks | TUF, go-tuf, tufup, cosign, minisign | metadata only | no | no | n/a |
| Embedded OTA | Mender, RAUC, SWUpdate, Balena | yes | reboot into the other slot | n/a | yes |

Gaps none of them fill: verified download plus service-aware restart in one
place; an open cross-platform updater for services; health-gated rollback
outside embedded Linux; zero-downtime handoff that verifies what it restarts
into and can carry PTYs, not only sockets; freshness against a replayed
manifest; Windows service update outside installers.

Two 2026 events shaped the trust model: a signing-capable credential pulled out
of a GitHub Actions runner was used to push a poisoned Trivy release in March,
and GitHub's immutable releases went GA in October, closing silent asset
replacement on unsigned manifests hosted there.

## Principles

1. Opt-in, no magic. Nothing updates without `--auto-update`. No detection of
   TTYs, tmux or package managers; an explicit flag is consent.
2. Works unsigned. A manifest over TLS with hashes is a complete configuration.
3. One engine for every run mode: exec in place, same PID, descriptors carried
   across. Windows gets a helper process.
4. Spec first. Small enough for work repositories that cannot take an MIT
   dependency to re-implement, and for an Android app to consume from Kotlin.
5. State is never touched by an update.
6. Rollback is automatic and needs nobody logged in.

## Architecture

| Part | What |
|---|---|
| spec | JSON manifest, detached signatures, service description |
| library (Go) | update engine, exec handoff, health rollback, `service` subcommands |
| amarit daemon | per-machine daemon and CLI for things that do not embed the library |
| release tooling | `amarit release` and a GitHub Action: build matrix in, manifest out, optional signing on the maintainer's machine |

No server is required. Static files on any HTTPS host are the default.

## Update engine

1. Fetch the manifest; check `expires`, channel, `min_version`, `rollout`, and
   that the target is newer than the running version.
2. Stream the asset into the binary's own directory while hashing. Same
   filesystem, so the swap is a rename. Long timeouts and a caller-supplied
   HTTP client; artifacts reach hundreds of MB and some hosts need auth.
3. Verify sha256 and size, then the signature when the tier requires it.
4. Smoke-test the new file as a subprocess (`<bin> -version`).
5. Write `attempt.json`. A process that finds itself on the old version with
   that file present holds and logs instead of looping.
6. Rename the running binary to `.prev`, the new file into place. Single-flight.
7. Ask the program whether it can restart now; run its drain with a hard
   deadline that also cuts reads blocked on the far side.
8. Exec in place with the same argv and environment. Registered descriptors
   have close-on-exec cleared and their numbers passed in the environment.
9. The new process writes `healthy` when its readiness check passes. Three
   starts without it swap `.prev` back.

### Restart strategies

Step 7 and 8 have two forms, chosen per program:

| Strategy | How | Downtime | Supervisor |
|---|---|---|---|
| overlap | the old process starts the new binary beside itself, passing its listeners (or both bind with `SO_REUSEPORT`); the new one reports ready; the old stops accepting, drains with a deadline, exits | none: connections are never refused, long-lived ones finish on the old process | the PID changes. systemd: `Type=notify` with `MAINPID=` from the new process. Detached or amarit daemon: the pid file is rewritten. launchd cannot follow a PID change, so overlap is unavailable there |
| in place | the old process execs the new binary with the same argv, environment and inherited descriptors | a pause while the old drains and the new starts; queued connections wait in the listen backlog, nothing is refused | the PID never changes: launchd, systemd and nohup all keep supervising |

Overlap is the gateway's zero-downtime upgrade as a library feature, with the
parts its script got wrong fixed: readiness is a probe the program answers,
not a port guess, and the drain deadline is hard. In place is the default,
because it works everywhere; a program opts into overlap with
`Restart: amarit.Overlap`.

Also: a target can be handed in by the program from its own control channel
(`amarit.Apply`), and the engine reports when the executable on disk is newer
than the running process.

## Consumer setup

A consumer hardcodes nothing. `amarit.json` at the repository root carries
the project name, manifest URL, optional key, restart strategy and service
description (spec section 8). `amarit release` reads it to build the manifest
and stamps it into the binary with two linker variables; the program calls
`amarit.Start()` and is done. A plain `go build` during development has no
stamp, so the same code path is inert there.

## Keep-alive tier

A ladder per platform, strongest first: LaunchDaemon with `UserName`, then
LaunchAgent, then detached; systemd system unit, then user unit with lingering,
then detached. Features taken from the surveyed projects: N instances with
per-instance environment, an environment file at exec, restart-on-failure with
excluded exit codes, a run-flag file, throttle, log rotation, stray detection
that replaces only processes amarit started itself, and a `status` that reports
installed, loaded, running, healthy, and running-older-than-installed.

## Daemon decisions

What a real stack asked for (a web server, identical workers, a model
server owning a large child process) and how the daemon answers, with the
alternatives that were not taken.

### Process groups, with a per-unit grace

Units already start in their own session (`Setsid`), so the unit's pid is its
process-group id. A stop signals the group, waits `stop_timeout` (default
10 s) for every member to leave, then kills the group. Before this only the
pid was signalled and a program that spawned its own children left them
behind holding memory and ports. The same treatment goes to children a
crashed program leaves behind, and to processes a new daemon adopted, since
only the group id is needed. cgroups would be exact but are Linux-only;
a process group is what every Unix has.

### Resource limits by re-exec

A process can change only its own limits, and Go cannot set them for a child
alone. Rather than a C wrapper or a shell `ulimit` (which needs a shell and
changes argv[0]), the daemon starts a unit that has `limits` through its own
binary: `amarit __exec <binary> [args...]` with the limits in
`AMARIT_LIMITS`. The helper applies them, clears the variable and `exec`s
the program, so the limit reaches that program alone and the pid the daemon
watches is the program's own. launchd gives services 256 open files, which
Go restores for children even though it raises its own, so the plist
`amarit install` writes raises the soft limit to 65536 and a unit's
`limits` goes higher. Only `nofile` is implemented; others follow when
someone needs one.

### Deliberate restarts

The CLI and the daemon talk through files, as everything else does: a unit
file is wanted state, a status file is observed state. `amarit restart`
drops `run/<name>.restart`; the daemon takes the requests on its next poll,
stops those groups together and starts them fresh with the backoff reset and
the restart counter untouched, since the operator asked. A rolling restart
is the CLI pacing its requests: one unit, wait until it runs and has stayed
up two seconds, then the next; a unit that exits instead ends the roll with
an error. No socket or RPC was added for this.

### Double-supervision guard

Two supervisors for one program restart it in turns, forever; a real
deployment logged 629 restarts that way. `amarit run` and `amarit start`
read the launchd plists and systemd unit files on disk and compare their
program path to the unit's binary, symlinks resolved. A hit names the job
and the command that removes it; `-force` overrides. Reading files is cheap
and works without a running service manager; querying `launchctl print`
for every job or `systemctl show` would be slower and would only see what
is loaded right now. The probe is best effort and never fails the command.

### Instances

`"instances": N` runs N copies named `<name>@1` .. `<name>@N`; each gets
`AMARIT_INSTANCE` and `{instance}` expanded in args and env values, which is
all a worker needs to tell itself apart. The names are the daemon's keys
everywhere (status, logs, restart requests), so no second notion of
identity exists. The unit file describes the set; stopping one member is a
marker in `run/`, not a per-instance field, so the file stays a description
of what you want. Scaling down stops the highest numbers first.

### Value handoff

Descriptor handoff carries what the kernel can carry. A program that owns a
child process (a model server and its engine) also needs the child's pid
and port after an in-place update, or it would start a second engine.
`HandoffValues` sends a map of small strings in one environment variable,
base64 JSON; `InheritedValues` reads it once and clears it so grandchildren
never see it. Same shape as `Handoff`/`Inherited`, and nothing more: the
values are strings, the program decides what they mean.

### Log rotation while running

The program holds the log descriptor, so the file cannot be renamed away
from it. Past 10 MB the daemon copies the file to `<log>.1` and truncates
it in place; the descriptor was opened with `O_APPEND`, so the program's
next line lands at the start of the emptied file. A line written during the
copy can be lost, which is the price of not asking programs to reopen their
logs. The check is one `stat` per running unit per poll.

## Consumers, in order

1. A stateless tunnel agent on a canary channel.
2. A reverse SSH tunnel and a vendor agent with no supervisor, via amarit daemon.
3. A terminal server, with PTY handoff.
4. A web service, replacing an SSH deploy pipeline.
5. A multi-process agent harness, replacing a 400-line service installer.
6. An Android app as a read-only manifest consumer.
7. Work repositories, re-implementing the spec in-tree.

## Out of scope

Model files and other content-addressed blobs, Python environments, Docker
images, host provisioning, OS code signing and notarization, runtimes with their
own update channels, delta updates in the first version.
