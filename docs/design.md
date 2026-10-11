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

1. Opt-in, no magic. Nothing updates unless somebody wrote it down: the user
   with `--auto-update`, the operator by running the program under the amarit
   daemon, or the project by declaring `"auto_update": true` in its
   amarit.json, which turns the question around for its users
   (`--no-auto-update` opts out). No detection of TTYs, tmux or package
   managers; a written word is consent, a guess never is.
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

No server is required. Static files on any HTTPS host are the default; a
directory on the machine that runs the program works too.

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
   starts without it swap `.prev` back and record the version as rejected,
   so the next check does not take it again; the publisher's next release
   is taken as usual.

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
description (spec section 8). `amarit release` reads it to build the
manifest; the binary carries it either stamped in with two linker variables
(`amarit stamp`) or embedded by the program and handed to
`amarit.ProjectFile`, which is the way for a project installed with `go
install`, where no linker flags exist. A program may instead say the same
values as options in code. All of them meet in one function, `resolve`,
which lays the layers over each other in a fixed order: the module version
Go recorded in the binary, the stamp, the options as given, the
environment, the command line. The stamp is applied through `ProjectFile`,
so a file reaches the Config by one path whatever carried it. Nothing is
derived: the module path and the hosting convention could suggest a
releases URL, and the library refuses to guess one; a build that was given
no URL is inert, and says so only when asked.

The version has one more source than the rest: when neither the stamp nor
code sets it, `debug.ReadBuildInfo` supplies the module version Go
recorded (`v0.5.2` for `go install ...@v0.5.2`, a pseudo-version or a tag
with `+dirty` for a build inside a checkout). That is reading a fact the
toolchain wrote, not a guess, and it is what lets any `go install` build
know where it stands.

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

### `amarit update`

The program updates itself; the daemon restarts it. `amarit update` runs
`<binary> update` for each binary the chosen units run, then asks the
daemon to restart the units whose binary changed. The daemon never
downloads: the program's own update knows its manifest and target,
verifies, smoke-tests, swaps and keeps prev, and that one code path runs
whether a person, the auto-update loop or this command asks for it.
Teaching the daemon the manifest would be a second updater with the same
checks and a second home for the project configuration.

Once per binary: eight workers on one file are one update. Units are
grouped by their resolved binary path, and whether a program was built
with amarit is read from the binary's own build info, so no unit field has
to say so. Restart only on change: the binary's digest is taken before and
after, and an unchanged file restarts nothing, which makes the command
safe to run after every build.

The library's three-starts rollback cannot cover this path: `<binary>
update` execs the new version once, inside the command, where it reports
healthy before the daemon has run it at all. So when the restart fails
(an instance exits; a rolling restart stops) `amarit update` does the
rollback itself: prev back as the binary, the rejected one kept as `bad`,
the units restarted together, and a loud message. The same move the
library makes, done by whoever saw the failure.

### Shared binaries

Instances of a unit are N processes on one file, each with auto-update on
by default. Two of them applying the same release raced on the rename: the
second deleted prev, which was the old binary, and put the new one there,
so rollback would have restored the version being rolled back from. Apply
holds a flock in the sidecar for the whole swap; a process that gets the
lock after a sibling finds the attempt recorded and only restarts into
what is on disk. The lock is a descriptor, so a crash, or the exec itself,
releases it. The lock keeps the default safe; it does not make N
restarts within the hour, each on its own jitter, a good shape for a
service stack. There, `AMARIT_AUTO_UPDATE=0` in the unit and
`amarit update -rolling` after the build is the recommendation.

### The daemon updates itself

The daemon is a long-running program like any it supervises, so it uses the
library: it declares `auto_update` its default, as amarit's own
`amarit.json` does (how the two stay one is the next section). The restart
is the in-place exec, which is what makes it
safe under the one supervisor the daemon has: launchd on the headless Mac
cannot follow a pid change, and the exec keeps the pid. It also keeps the
units' parentage, which surfaced one bug: the adopted-process watcher polled
`kill(pid, 0)`, which a zombie answers, so a unit that exited after the
daemon's exec would have looked alive forever. The watcher now reaps with
`wait4(WNOHANG)` first and polls only for processes that are not its
children (a daemon restarted by launchd). Readiness is the preflight (the
base is usable and the units parse); a daemon that fails it exits, the
supervisor restarts it, and the library's three-start rollback does the
rest. The engine's drain waits for the current reconcile pass so the exec
never lands in the middle of a stop. Only `amarit daemon` runs the engine;
the short commands have nothing to keep current and no business marking a
binary healthy. The first installed daemon predates all this and is
replaced by hand once; after that the channel carries it.

### The daemon's own configuration

`go install github.com/sudiptadeb/amarit/cmd/amarit@latest` is the
documented install, and it cannot pass linker flags, so until v0.5.2 such a
daemon had no project, no releases URL, and never updated. The library's
answer for any project is to embed `amarit.json`; amarit itself cannot take
it as written. `amarit.json` belongs at the repository root, where
`amarit release` and every other project's file sit, and Go's `embed` only
reaches a package's own directory and below: `cmd/amarit` cannot embed
`../../amarit.json`, and the root package, which is the library, must not
carry amarit's project configuration into every program that imports it.

Four options were weighed. Moving the file into `cmd/amarit` puts amarit's
own file in a different place from the one it tells every other project to
use. A `go generate` copy into `cmd/amarit` is a second file in the tree
that a reader has to know is derived, and a step to forget. Deriving the
releases URL from the module path would work for amarit and is exactly the
magic the library refuses to do for anyone else. So the daemon repeats the
three facts the binary needs, project, releases URL and `auto_update`, as
options in code, and `TestSelfConfigMatchesProjectFile` compares them with
the parsed root file, every field, so a key or an interval added to the
file alone fails the build. A release build still stamps the file in, and
the two agree. The duplication is three constants and one test; that was
judged smaller than a second file or a guess.

The hash tier compares versions, never bytes. A `go install` build of
`v0.5.2` is not the release asset for 0.5.2 byte for byte, and the engine
does not care: same version, "already on", nothing swapped. An older
`go install` build takes the next release once and is a release build from
then on. Proven on a scratch daemon before v0.5.2 shipped: a `go install`
build at 0.5.2 against a channel at 0.5.2 checked and stayed; the channel
moved to 0.5.3 and it execd in place, same pid, its unit's pid unchanged,
and reported "already on 0.5.3" at every check after.

### Log rotation while running

The program holds the log descriptor, so the file cannot be renamed away
from it. Past 10 MB the daemon copies the file to `<log>.1` and truncates
it in place; the descriptor was opened with `O_APPEND`, so the program's
next line lands at the start of the emptied file. A line written during the
copy can be lost, which is the price of not asking programs to reopen their
logs. The check is one `stat` per running unit per poll.

## Root units (proposal, not built)

Some jobs have to run as root: a one-shot `sysctl` at boot (raising the
GPU wired-memory limit on a macOS model server), a listener on a port
below 1024, tun and routing setup for a tunnel, and a service that should
run under a dedicated low-privilege account, since switching user needs
root. Today these stay in raw launchd or systemd beside amarit, and the
double-supervision guard sees them there. The daemon itself never runs as
root (README, "Who runs as whom"), and nothing here changes that.

Three options were weighed.

**(A) One daemon, as root, dropping to a per-unit `user`.** Rejected. It
turns every machine's daemon into a root daemon, so the user-writable unit
files in `~/.amarit` and the user-writable binary in `~/go/bin` become
root's input: the hole issue #4 guards against, made the design. Self-update
without sudo, the reason the binary is user-owned, is incompatible with a
root daemon. And every statement in the README's trust section stops
being true.

**(C) Not supported; root jobs stay in launchd and systemd.** This is the
honest answer today and costs little for the `sysctl` case: a LaunchDaemon
with `RunAtLoad` or a systemd oneshot needs nothing from amarit. On Linux a
port below 1024 is `AmbientCapabilities=CAP_NET_BIND_SERVICE` in a unit
that runs as the user, no root at all. What is lost is one `ls`, one log
format, `instances`, `env_file`, backoff, and `amarit update` for the
service-account case, where the program is a real service.

**(B) A second, separate system daemon.** Recommended. Same binary, its own
root-owned base, its own service file, every command on it under sudo. The
per-user daemon stays exactly as it is, and the boundary is one sentence:
changing a root unit needs sudo. The rules that make it safe:

1. *The binary.* The root daemon's executable must be at a root-owned path
   that no other user can write, every parent directory included, checked
   by `stat` at start and refused otherwise, with the reason in the log.
   `~/go/bin/amarit` fails that check by design; the operator copies the
   binary to `/usr/local/sbin` or `/Library/Application Support/amarit/bin`
   first. Whether a path qualifies is decided by ownership and mode, not by
   name: `/opt/homebrew/bin` is user-writable on many Macs.
2. *The base.* macOS: `/Library/Application Support/amarit` with `units/`,
   `run/`, `logs/`. Linux: `/etc/amarit/units`, `/var/lib/amarit/run`,
   `/var/log/amarit`. Root-owned, 0700 and 0600 from the first version;
   reading needs sudo too, which keeps the rule to one sentence. The daemon
   refuses a unit file not owned by root or writable by group or others.
3. *What a unit points at.* The daemon refuses a unit whose binary, any
   parent directory of it, or `env_file` is owned by a non-root user or
   writable by group or others, symlinks resolved and each component
   checked. Otherwise a user swaps the binary and gets root at the next
   restart. `/tmp` fails as world-writable; so does a build directory in a
   home. A refused unit is parked with the reason in `ls` and the log.
4. *`user` and `group`.* Optional fields honoured only by the root daemon:
   the child does `initgroups`, `setgid`, `setuid` before exec
   (`SysProcAttr.Credential` with the supplementary groups filled in), so
   the program never runs as root, and a unit with `limits` runs
   `amarit __exec` already as that account, raising its soft limit up to
   the account's hard limit. Without `user` a root unit runs as root, which
   is what `sysctl` and a port below 1024 need. The per-user daemon rejects
   the field at `SaveUnit` and skips a unit file that carries it, with a
   log line, so a file copied between the two tiers cannot run as the
   wrong identity by silence.
5. *One-shot boot jobs.* `restart: never` covers them, with one caveat.
   The daemon runs the unit once, parks it as `exited`, and runs it again
   when the unit file changes. The parked flag lives in memory, so every
   daemon start runs the unit again: a reboot, a `systemctl restart`, and
   the daemon's own in-place update. For an idempotent `sysctl` that is
   "at boot, and a few more times", harmless. A job that must run exactly
   once per boot would need a marker keyed to the boot id; no example asks
   for it yet, so it is not proposed.
6. *Updates.* Auto-update is off for the whole root tier until the signed
   tier lands: the root daemon's service file passes `--no-auto-update`, and
   the daemon gives root units `AMARIT_AUTO_UPDATE=0` instead of relying on
   `AMARIT_MANAGED=1`. `sudo amarit update -root` applies releases on
   request, with the same prev-and-rollback as today; the root daemon's own
   binary is replaced the way it was installed, by copying it into place
   and restarting the service. Reason: the hash tier is integrity, not
   authenticity. The manifest and the asset come from the same host, so
   whoever can publish there ships a binary and its matching hash
   together; the Trivy release in March 2026 was exactly that. For the
   per-user tier the blast radius is the user, who opted in; for root it is
   the machine. A pinned signing key makes the publisher, not the host, the
   authority, and per-unit opt-in can follow it.
7. *CLI shape.* A `-root` flag on every unit command (`sudo amarit -root
   run ...`, `-root ls`, `-root logs`), which requires euid 0 and selects
   the fixed system base, never `AMARIT_HOME`. A command run under sudo
   without the flag is refused with the two choices spelled out, instead of
   what happens today, a detached root daemon over `/var/root/.amarit`.
   The alternative, sudo alone selecting the root tier, reads well but
   collides with `sudo amarit install --system`, which registers the
   *per-user* daemon and is the one command every README reader runs; it
   would also make `sudo amarit ls` typed from habit show a different list
   with no error. `--system` cannot be reused for the same reason, so the
   word is `-root`, which names the thing: a root unit. `install -root`
   writes the root daemon's service file: `com.amarit.root` in
   `/Library/LaunchDaemons` with no `UserName`, or
   `/etc/systemd/system/amarit.service` with no `User=`; both labels are
   distinct from the per-user ones, so the tiers coexist on one machine.
8. *The uid-0 guard.* Issue #4's `amarit daemon` refuses uid 0 and keeps
   doing so. The root tier is `amarit daemon -root`, a distinct mode
   entered only from its own service file: it refuses to run as anyone but
   root, refuses when rule 1 fails, and never reads `AMARIT_HOME`. Each
   mode refuses the other's uid, so a per-user service file that lost its
   user line exits non-zero and the supervisor's log says why, rather than
   a root daemon over a user's files.
9. *Platforms.* Linux and macOS as above; the only difference is the base
   layout, which follows each system's convention, and that systemd could
   do the user drop itself while launchd cannot, which is one more reason
   the drop lives in the daemon. Other platforms get nothing: there is no
   `install` there, so no root tier, and the detached daemon refuses uid 0
   under #4 like everything else.

Not taken: a setuid helper (a second trust boundary to get right, for no
gain over a daemon that already runs as root); a sudoers rule for `amarit`
(hands root to whoever can write the user's units); polkit (Linux-only,
and a policy language to maintain). Until (B) exists, (C) is the
documented path.

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
