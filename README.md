# upkeep

**The updater for software that runs as a service.**

upkeep is a Go library, a small JSON spec and a daemon that let a program update
itself from a manifest URL and keep itself running, on macOS and Linux (Windows
later), whatever supervises it: launchd, systemd, a `nohup` from an SSH session,
or a tmux pane.

It does the two halves of the job that existing tools leave to you:

- **Replace the binary safely.** Verify, swap, restart in place with the same PID,
  and roll back by itself if the new version does not come up healthy.
- **Keep the process alive.** Install it as a launchd daemon, a systemd unit, or a
  detached process, in that order of preference, with one subcommand.

> Status: **design stage**. The spec and the API below are settled; the engine is
> being built. Nothing here is ready to run yet. See [docs/design.md](docs/design.md)
> for the full proposal and [docs/spec.md](docs/spec.md) for the manifest format.

## Why another one

Every library we surveyed does one half. Verification lives in a few Go
libraries, graceful restart in two others, and no maintained project combines
them with a correct restart under a service manager. Syncthing, Tailscale and
cloudflared each solved it in-tree because nothing reusable existed. Health-gated
rollback exists only in embedded Linux updaters. The open updater daemons are
either archived (Google Omaha), closed (Keystone) or in-tree (Chromium).

upkeep is the thing those projects would have imported.

## Integration

One call at the top of `main`:

```go
import "github.com/sudiptadeb/upkeep"

var Version = "dev" // -ldflags "-X main.Version=0.5.3"

func main() {
    upkeep.Run(upkeep.Config{
        Project:  "termulaa",
        Version:  Version,
        Manifest: "https://github.com/sudiptadeb/termulaa/releases/latest/download/upkeep.json",
        Key:      "RWQf6LRCGA9i...", // optional: enables the signed tier
    })
    // the program as before
}
```

`Run` reads its own flags from `os.Args`, strips them, starts the background
loop if `--auto-update` was given, registers the `service` subcommands, and
returns. **Without the flag it does nothing.** Updating is consent, and its
absence is the off switch; the library never guesses from a TTY, a tmux session
or a package manager what you meant.

A stateful program adds hooks so that an update keeps its connections and
terminals:

```go
upkeep.Run(upkeep.Config{
    // ...
    Handoff: func() []*os.File { return pty.Masters() }, // descriptors carried across the exec
    Drain:   func(ctx context.Context) { srv.Shutdown(ctx) }, // bounded by the engine
    Healthy: func() bool { return srv.Ready() },               // gates rollback
})
for _, f := range upkeep.Inherited() { pty.Adopt(f) } // in the new process, before serving
```

Flags added by `Run`, all off by default:

| Flag | Effect |
|---|---|
| `--auto-update[=stable\|canary]` | follow a channel on an interval with jitter |
| `--update-check` | one check now, then continue |
| `--update-url URL` | use this manifest instead of the compiled-in one (mirrors, enterprises) |
| `--update-key KEY`, `--update-key-url URL` | trust this key for manifests (enterprise tier) |
| `--allow-downgrade` | permit moving below the running version |
| `service install\|status\|stop\|uninstall` | the keep-alive tier |

## How an update runs

```
check manifest → download, hash while streaming → verify, smoke test → swap in, keep .prev
                                                                              │
          running the new version ◀── healthy? ◀── drain, then exec in place ◀┘
                                         │ no, three starts in a row
                                         ▼
          running the previous version ◀── restore .prev and exec
```

The new binary is `exec`'d in place on Unix: same PID, same argv, same
environment, same inherited descriptors. launchd, systemd and a `nohup` parent
keep supervising the process they started. Windows gets a helper process.

Every check before the swap fails closed and leaves the running binary
untouched. A version that does not write its `healthy` marker within three
starts is swapped back for the previous one without anyone logging in.

## The manifest

One `upkeep.json` per project, published as a release asset so
`https://github.com/<owner>/<repo>/releases/latest/download/upkeep.json` is a
stable URL with no API call:

```json
{
  "spec": 1,
  "project": "termulaa",
  "published": "2026-10-09T10:00:00Z",
  "expires": "2026-11-09T10:00:00Z",
  "min_version": "0.5.0",
  "channels": {
    "stable": { "version": "0.5.2", "rollout": 100 },
    "canary": { "version": "0.5.3", "rollout": 100 }
  },
  "assets": {
    "0.5.3": {
      "darwin/arm64": { "url": "https://…/termulaa-darwin-arm64-v0.5.3", "sha256": "…", "size": 11345920, "sig": "RWQ…" },
      "linux/amd64":  { "url": "…", "sha256": "…", "size": 11829248 },
      "android":      { "url": "https://…/termulaa.apk", "sha256": "…", "size": 4102144, "version_code": 4 }
    }
  }
}
```

`min_version` says nobody should run anything older, and doubles as the kill
switch. Channels are pointers to versions; promoting canary to stable is editing
one file. The full format is in [docs/spec.md](docs/spec.md).

## Trust, in three tiers

| Tier | For | What is checked | Where trust lives |
|---|---|---|---|
| **hash** | your own projects on GitHub | TLS, sha256 and size per asset, no downgrade, `expires` | the manifest URL, which on github.com is the same trust root as the install itself |
| **signed** | anything other people install | hash tier plus an ed25519 signature per asset against the author's compiled-in key | the author's private key, on the author's machine, never in CI |
| **enterprise-controlled** | fleets whose admin decides what runs | signed tier plus a signature on the manifest against a key the admin configured | the admin's own config channel |

Signing is an add-on, never a prerequisite. Assets are signed rather than only
the manifest, so an admin can compose their own manifest, hold a fleet on a
version and skip another, from binaries only the author could have produced.
Key rotation chains from a key the binary already trusts; a key fetched from a
URL without that chain is refused.

## Keep-alive tier

`<program> service install` keeps a binary running the strongest way the machine
allows, and says which it took:

| Platform | Preferred | Fallback | Last resort |
|---|---|---|---|
| macOS, headless or shared | LaunchDaemon with `UserName` (sudo once); starts at boot | LaunchAgent, only with a desktop session | detached with a pid file; survives the terminal, not a reboot |
| macOS, desktop user | LaunchAgent | detached | |
| Linux | systemd system unit (sudo) | systemd user unit with lingering | detached |
| Windows (later) | SCM service | scheduled task at logon | detached helper |

With: N instances of one binary with per-instance environment, an environment
file sourced at exec, restart-on-failure with "not on these exit codes", a
run-flag file so a non-root user can park a daemon, log rotation, and a `status`
that reports *running is older than installed*.

`upkeeper`, the standalone daemon, applies the same ladder and the same manifest
to things that cannot embed the library: scripts, third-party binaries pinned to
versioned directories, anything you did not write.

## Roadmap

1. Library core: manifest, hash tier, swap, exec in place, health rollback.
2. Descriptor handoff for listeners and PTYs.
3. `service install` ladder on macOS and Linux.
4. `upkeeper` daemon and CLI.
5. Signed and enterprise tiers with chained key rotation; `upkeep release` tooling and a GitHub Action.
6. Windows.
7. Spec 1.0; delta updates after.

## Non-goals

Model files and other content-addressed blobs, Python environments, Docker
images, host provisioning, OS code signing and notarization, and runtimes that
already update themselves. upkeep may supervise those; it will not pretend to
update them.

## License

MIT. See [LICENSE](LICENSE).
