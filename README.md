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

> Status: early. The hash tier of updates and the daemon work end to end;
> descriptor handoff, signing, the enterprise tier and Windows follow the
> roadmap. [docs/design.md](docs/design.md) is the proposal,
> [docs/spec.md](docs/spec.md) the manifest format.

## Keep it alive

```sh
go install github.com/sudiptadeb/amarit/cmd/amarit@latest

amarit install                    # the daemon, as a LaunchAgent / systemd user unit, else detached
sudo amarit install --system      # or: at boot, nobody logged in; after this, no more sudo

amarit run termulaa -rc           # keep it running; restarted on failure, logs kept
amarit ls
amarit logs -f termulaa-rc
amarit stop termulaa-rc | start | rm
```

A unit is a JSON file in `~/.amarit/units/`; the daemon picks changes up
within seconds. Stopping the daemon never stops its units, and a new daemon
adopts them.

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
app --auto-update[=canary]        # the same on an hourly loop
app --update-url https://mirror/releases.json
```

A stateful program adds hooks: `amarit.Start(amarit.Handoff(fds), amarit.Drain(fn), amarit.Healthy(fn))`.
Three layers control the result, later ones winning: options in code, the
stamped file, the command line.

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

Descriptor handoff and overlap restart → signed and enterprise tiers →
`amarit install` adopting a program's own service → Windows → spec 1.0.

MIT. See [LICENSE](LICENSE).
