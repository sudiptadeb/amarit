# amarit

[![ci](https://github.com/sudiptadeb/amarit/actions/workflows/ci.yml/badge.svg)](https://github.com/sudiptadeb/amarit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sudiptadeb/amarit.svg)](https://pkg.go.dev/github.com/sudiptadeb/amarit)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Keep it alive. Keep it current.** *Amar*: undying.

amarit is two small things that belong together:

- **A library** a Go program embeds so it can update itself from a release
  manifest: verify, swap, restart in place with the same PID (or with zero
  downtime), and roll back by itself if the new version is not healthy.
  Opt-in only; nothing updates without `--auto-update`.
- **A daemon** you install once per machine that keeps any program running,
  with the best persistence the machine allows: LaunchDaemon, LaunchAgent,
  systemd, or detached. After `sudo amarit install --system` once, every
  `amarit run` is persistent without sudo.

The two never overlap: the library updates, the daemon keeps alive. A
program can use either without the other.

> Status: early. The hash tier of updates, descriptor and value handoff
> across the restart, and the daemon work end to end; signing, the
> enterprise tier and Windows follow the roadmap.
> [docs/design.md](docs/design.md) is the proposal and the reasoning,
> [docs/spec.md](docs/spec.md) the manifest format.

## Install amarit

```sh
go install github.com/sudiptadeb/amarit/cmd/amarit@latest
```

That puts the binary in `$(go env GOPATH)/bin`, normally `~/go/bin`. Make sure
it is on your PATH (add `export PATH="$HOME/go/bin:$PATH"` to `~/.zshrc` or
`~/.bashrc`), then:

```sh
amarit version
```

Prebuilt binaries for macOS and Linux are attached to each
[release](https://github.com/sudiptadeb/amarit/releases).

## Keep it alive

Install the daemon once per machine. It picks the strongest persistence the
machine allows and says which it took:

```sh
sudo "$(command -v amarit)" install --system    # recommended: starts at boot with nobody logged in;
                                                # after this, nothing below needs sudo
amarit install                                  # without sudo: LaunchAgent / systemd user unit when the
                                                # account has one, else a detached daemon (no reboot survival)
```

`sudo` has its own PATH, which is why the first form names the binary. The
daemon runs as you, not as root, and keeps its units in `~/.amarit/`.

Then keep anything running:

```sh
amarit run web -serve                          # unit "web-serve": restarted when it exits, logs kept, survives reboots
amarit run -name worker -instances 4 worker    # four copies: worker@1 .. worker@4
amarit run -name tunnel ssh -N -R 7900:127.0.0.1:7900 vps
amarit ls
amarit logs -f worker@2
amarit restart 'worker@*' -rolling             # one at a time; each must stay up 2 s before the next goes
amarit update [-rolling] [web-* ...]           # bring units built with amarit onto their latest release (below)
amarit stop worker@3 | start worker | rm web-serve
amarit uninstall
```

A unit is a JSON file in `~/.amarit/units/`; `amarit run` writes a plain
one, and you can edit it (then `amarit restart <name>` to apply). The
daemon picks changes up within seconds. Everything a unit can say:

```json
{
  "name": "worker",
  "binary": "/usr/local/bin/worker",
  "args": ["-id", "{instance}", "-model", "http://127.0.0.1:8090"],
  "env": {"WORKER_ID": "w{instance}", "AMARIT_AUTO_UPDATE": "0"},
  "env_file": "/etc/worker.env",
  "dir": "/var/lib/worker",
  "instances": 8,
  "restart": "on-failure",
  "no_restart_exit": [3],
  "stop_timeout": "60s",
  "limits": {"nofile": 65536}
}
```

| Field | Meaning |
|---|---|
| `instances` | run N copies, named `worker@1` .. `worker@N` in `ls`, logs and status. Each gets `AMARIT_INSTANCE=<i>`, and `{instance}` in `args` and `env` values becomes its number. Change the number and the daemon adds or removes copies, highest numbers first. `stop`, `start`, `restart` and `logs` take `worker` (every copy) or `worker@3` |
| `restart` | `always` (default), `on-failure`, or `never`; `no_restart_exit` lists exit codes that mean "leave it down". Restarts back off from 1 s to 1 min; a deliberate `amarit restart` resets that and is not counted |
| `stop_timeout` | a stop or restart sends SIGTERM to the program's whole process group, its own children included, waits this long (default `10s`), then SIGKILLs whatever is left. Children a crashed program leaves behind are stopped the same way |
| `limits` | resource limits for the program; `nofile` is the open-files limit. Set from inside the process by starting it through amarit itself, so the pid is still the program's. A value above the hard limit takes the hard limit and says so in the log |
| `env`, `env_file`, `dir` | extra environment, a file of `KEY=VALUE` lines read at start, the working directory |

Every unit also gets `AMARIT_MANAGED=1` and `AMARIT_UNIT=<name>`. Output
goes to `~/.amarit/logs/<name>.log`, rotated at 10 MB with one previous
file kept, while the program runs.

`amarit run` and `amarit start` look for a launchd job or systemd unit that
already runs the same binary and refuse, naming it and the command that
removes it: two supervisors for one program restart it in a loop. `-force`
overrides when you mean it.

Stopping the daemon never stops its units, and a new daemon adopts them.
The daemon never touches a binary: updates are the program's own business,
below.

## Keep it current

An `amarit.json` at the repository root is the only configuration; nothing
is hardcoded in the program:

```json
{
  "project": "termulaa",
  "releases": "https://github.com/sudiptadeb/termulaa/releases/latest/download/releases.json",
  "release": {
    "targets": ["darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"],
    "asset": "termulaa-{os}-{arch}-v{version}",
    "url": "https://github.com/sudiptadeb/termulaa/releases/download/v{version}/{asset}"
  }
}
```

```go
import "github.com/sudiptadeb/amarit"

func main() {
    amarit.Start() // reads the stamped amarit.json; inert in a plain `go build`
    // the program as before
}
```

Build with the stamp and publish the manifest beside the release assets:

```sh
go build -ldflags "$(amarit stamp -version 0.5.3)" ./cmd/app
amarit release -version 0.5.3 -dist release        # writes releases.json
```

Then every installed copy can update itself:

```sh
app update                        # check, verify, swap, restart, print the version, exit
app update --check                # say what is available, change nothing
app --auto-update[=canary]        # the same on a loop, hourly unless told otherwise
app --update-url https://mirror/releases.json
app --auto-update --update-interval 5m
```

A stateful program adds hooks: `amarit.Start(amarit.Handoff(fds), amarit.Drain(fn), amarit.Healthy(fn))`.
`Handoff` returns the descriptors (listeners, PTY masters) that must survive
the restart; the new process gets them back from `amarit.Inherited()` with
the names it gave them, so an update keeps every connection and terminal.
`amarit.HandoffValues(fn)` carries small named strings the same way, for
what is not a descriptor: the pid and port of a child process the program
re-adopts after the restart. The new process reads them once with
`amarit.InheritedValues()`; the variable is cleared so nothing it starts
sees them.

Four layers control the result, later ones winning: the stamped file,
options in code (`amarit.Interval(5*time.Minute)`, `amarit.Key(k)`, ...),
the environment, the command line. So `"interval": "5m"` in `amarit.json`
is the program's own cadence, `AMARIT_UPDATE_INTERVAL=30m` in a unit's
environment changes it for that install, and `--update-interval` wins for
one run. A program running as an amarit unit has auto-update on (the
operator said "keep this current" by putting it there);
`AMARIT_AUTO_UPDATE=0` in the unit's environment turns it off.

Several processes on one binary (a unit with `instances`, or units that
share a program) take turns through a lock beside the binary, so the file is
swapped once; but each then restarts on its own schedule, within the hour.
For a service stack, give such units `"AMARIT_AUTO_UPDATE": "0"` and update
them together with `amarit update`, below.

## Update a service you build locally

A program that runs as amarit units on the machine it is built on needs no
release host. Point `releases` at a directory and leave `release.url` out,
so each asset is named relative to the manifest:

```json
{
  "project": "app",
  "releases": "/srv/app/releases/releases.json",
  "release": { "targets": ["linux/amd64"], "asset": "app-{os}-{arch}-v{version}" }
}
```

Build with the stamp and release into that directory:

```sh
go build -ldflags "$(amarit stamp -version 1.4.0)" -o /srv/app/releases/app-linux-amd64-v1.4.0 ./cmd/app
amarit release -version 1.4.0 -dist /srv/app/releases -out /srv/app/releases/releases.json
```

Then bring the running units onto it:

```sh
amarit update -check              # what every unit built with amarit would do
amarit update                     # all of them
amarit update 'web-*' -rolling    # some of them, one instance at a time
```

`amarit update` runs each program's own `<binary> update` once per binary,
however many units share it, with the unit's working directory and
environment; then it restarts the units whose binary changed and leaves the
rest alone, so it is safe to run after every build. If a restart fails (an
instance exits, a rolling restart stops), it puts the previous binary back
from `<bin>.amarit/prev`, restarts the units, keeps the rejected binary as
`<bin>.amarit/bad`, and says so. A unit named outright whose program was not
built with amarit is an error; with no names such units are skipped and
listed.

## How an update runs

```
check manifest → download, hash → verify, smoke test → swap in, keep prev → restart
                                                                            │
                        running the new version ◀── healthy? ──── no ──▶ restore prev
```

Every check before the swap fails closed and leaves the running binary
untouched. A version that does not report healthy within three starts is
rolled back without anyone logging in. Restart is in place by default (same
PID, inherited descriptors); `"restart": "overlap"` starts the new process
beside the old one for zero downtime where the supervisor can follow a PID
change.

## The manifest

`releases.json`, published as a release asset:

```json
{
  "spec": 1, "project": "termulaa", "published": "2026-10-09T10:00:00Z",
  "min_version": "0.5.0",
  "channels": { "stable": "0.5.2", "canary": "0.5.3" },
  "releases": {
    "0.5.3": { "rollout": 25, "assets": { "darwin/arm64": { "url": "…", "sha256": "…", "size": 7908066 } } }
  }
}
```

`min_version` is the floor and the kill switch; channels point at versions.
Signing (ed25519, minisign format) is an add-on tier, never a prerequisite.
Full format: [docs/spec.md](docs/spec.md).

## Roadmap

Overlap restart → signed and enterprise tiers → unit health probes
(`service.health`) → `amarit install` adopting a program's own service →
Windows → spec 1.0.

MIT. See [LICENSE](LICENSE).
