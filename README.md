# upkeep

[![ci](https://github.com/sudiptadeb/upkeep/actions/workflows/ci.yml/badge.svg)](https://github.com/sudiptadeb/upkeep/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sudiptadeb/upkeep.svg)](https://pkg.go.dev/github.com/sudiptadeb/upkeep)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**The updater for software that runs as a service.**

A Go library, a small JSON spec and a daemon that let a program update itself
from a manifest URL and keep itself running, under launchd, systemd, `nohup`,
tmux or nothing at all.

- **Replace the binary safely.** Verify, swap, restart with zero downtime or in
  place with the same PID, and roll back by itself if the new version is not healthy.
- **Keep the process alive.** `service install` picks the strongest option the
  machine allows: LaunchDaemon, systemd unit, or detached.
- **Works unsigned.** A manifest with hashes is a complete setup. Signing and
  enterprise-controlled manifests are add-on tiers.
- **Opt-in, no magic.** Nothing updates without `--auto-update`.

> Status: design stage. The spec and API are settled; the engine is being built.
> [docs/design.md](docs/design.md) is the proposal, [docs/spec.md](docs/spec.md) the manifest format.

## Integration

Nothing is hardcoded in the program. An `upkeep.config` at the repository root
holds the project name, manifest URL, optional key and service description; the
release tool stamps it into the binary at build time, and the program reads the
stamp:

```json
{
  "project": "termulaa",
  "manifest": "https://github.com/sudiptadeb/termulaa/releases/latest/download/upkeep.json",
  "key": "RWQf6LRCGA9i...",
  "restart": "overlap",
  "service": { "restart": "on-failure", "no_restart_exit": [4, 5, 7], "health": "http://127.0.0.1:17380/health" },
  "release": { "targets": ["darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"] }
}
```

```go
import "github.com/sudiptadeb/upkeep"

func main() {
    upkeep.Start() // reads the stamped upkeep.config; a plain `go build` has no stamp and does nothing
    // the program as before
}
```

A stateful program passes `upkeep.Hooks{Handoff, Drain, Healthy}` to `Start`.
`upkeep.Run(upkeep.Config{...})` remains for programs that want to supply the
values themselves. Flags added either way: `--auto-update[=channel]`,
`--update-check`, `--update-url`, `--update-key`, `--update-key-url`,
`--allow-downgrade`, `service install|status|stop|uninstall`.

## How an update runs

```
check manifest → download, hash → verify, smoke test → swap in, keep .prev → restart
                                                                               │
                        running the new version ◀── healthy? ──── no ──▶ restore .prev
```

Two restart strategies. **Overlap** starts the new process beside the old one on
the same listeners, waits until it is ready, then drains the old one with a
deadline: zero downtime, no dropped connections. **In place** execs the new binary
with the same PID and inherited descriptors, for supervisors that cannot follow a
PID change. Every check before the swap fails closed; a version that does not
report healthy within three starts is rolled back without anyone logging in.

## The manifest

```json
{
  "spec": 1, "project": "termulaa", "published": "2026-10-09T10:00:00Z",
  "min_version": "0.5.0",
  "channels": { "stable": { "version": "0.5.2" }, "canary": { "version": "0.5.3", "rollout": 25 } },
  "assets": { "0.5.3": { "darwin/arm64": { "url": "…", "sha256": "…", "size": 11345920 } } }
}
```

Published as a release asset, so `releases/latest/download/upkeep.json` is a
stable URL. `min_version` is the floor and the kill switch; channels are pointers
to versions. Full format: [docs/spec.md](docs/spec.md).

## Roadmap

Library core → descriptor handoff and overlap restart → `service install` →
`upkeeper` daemon → signed and enterprise tiers, release tooling → Windows → spec 1.0.

MIT. See [LICENSE](LICENSE).
