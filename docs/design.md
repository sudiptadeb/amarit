# upkeep: design

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
| upkeeper | per-machine daemon and CLI for things that do not embed the library |
| release tooling | `upkeep release` and a GitHub Action: build matrix in, manifest out, optional signing on the maintainer's machine |

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

Also: a target can be handed in by the program from its own control channel
(`upkeep.Apply`), and the engine reports when the executable on disk is newer
than the running process.

## Keep-alive tier

A ladder per platform, strongest first: LaunchDaemon with `UserName`, then
LaunchAgent, then detached; systemd system unit, then user unit with lingering,
then detached. Features taken from the surveyed projects: N instances with
per-instance environment, an environment file at exec, restart-on-failure with
excluded exit codes, a run-flag file, throttle, log rotation, stray detection
that replaces only processes upkeep started itself, and a `status` that reports
installed, loaded, running, healthy, and running-older-than-installed.

## Consumers, in order

1. A stateless tunnel agent on a canary channel.
2. A reverse SSH tunnel and a vendor agent with no supervisor, via upkeeper.
3. A terminal server, with PTY handoff.
4. A web service, replacing an SSH deploy pipeline.
5. A multi-process agent harness, replacing a 400-line service installer.
6. An Android app as a read-only manifest consumer.
7. Work repositories, re-implementing the spec in-tree.

## Out of scope

Model files and other content-addressed blobs, Python environments, Docker
images, host provisioning, OS code signing and notarization, runtimes with their
own update channels, delta updates in the first version.
