# amarit

[![ci](https://github.com/sudiptadeb/amarit/actions/workflows/ci.yml/badge.svg)](https://github.com/sudiptadeb/amarit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sudiptadeb/amarit.svg)](https://pkg.go.dev/github.com/sudiptadeb/amarit)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Keep any program running. Through crashes, logouts and reboots.**

amarit is one small binary for macOS and Linux. Give it a command and it
keeps that command running: restarted when it dies, started again at boot,
logs kept. No config files to write, no root once it is set up, no
ports opened. *Amar*: undying.

```sh
curl -fsSL https://raw.githubusercontent.com/sudiptadeb/amarit/main/install.sh | sh
```

## Quick start

```sh
# 1. Once per machine: start amarit at boot (the only sudo you will need)
sudo "$(command -v amarit)" install --system

# 2. Keep something running
amarit run -name site python3 -m http.server 8000

# 3. Check on it
amarit ls
amarit logs -f site
```

That is it. `site` now restarts when it exits and comes back after a
reboot, with nobody logged in.

No sudo? Run `amarit install` instead: it uses a LaunchAgent on macOS or a
systemd user service on Linux. The table under
[Where it runs](#where-it-runs) shows what each choice survives.

## What you get

- **Restarts on exit.** It backs off from 1 s to 1 min when a program keeps
  failing, and you can choose `always`, `on-failure` or `never`.
- **Survives reboots.** launchd or systemd starts amarit, and amarit starts
  your programs.
- **Logs.** Each program writes to `~/.amarit/logs/<name>.log`, rotated
  at 10 MB.
- **Several copies.** `-instances 4` runs `worker@1` to `worker@4`. Restart
  them one at a time with `-rolling`.
- **Runs as you.** Root is used once, to register amarit with the system.
  The daemon and your programs never run as root.
- **No double supervision.** amarit refuses to run a program that launchd
  or systemd already manages, and tells you how to remove the other one.
- **Stays current.** The daemon updates itself from amarit's releases and
  rolls back if a new version does not start.

## Everyday commands

| Command | What it does |
|---|---|
| `amarit run [-name N] <cmd> [args...]` | keep a command running (also `-instances N`, `-restart on-failure`, `-dir D`, `-env-file F`) |
| `amarit ls` | every program, its state, pid and restarts |
| `amarit logs [-f] <name>` | its output; `-f` follows |
| `amarit restart <name\|glob> [-rolling]` | restart now; `-rolling` does one copy at a time |
| `amarit stop <name>` / `amarit start <name>` | take it down and keep it down, or bring it back |
| `amarit rm <name>` | stop managing it |
| `amarit update [-check]` | bring programs built with amarit onto their latest release |
| `amarit uninstall` | remove the daemon; your programs keep running |

Names take a single copy (`worker@2`) or a glob (`'web-*'`).

```sh
amarit run -name worker -instances 4 ./worker          # four copies: worker@1 .. worker@4
amarit run -name tunnel ssh -N -R 7900:127.0.0.1:7900 vps
amarit restart 'worker@*' -rolling                     # each must stay up 2 s before the next
```

## Configure a program

Each program is a JSON file in `~/.amarit/units/`. `amarit run` writes it
for you. Edit it to change anything, and the daemon picks the change up
within seconds (`amarit restart <name>` applies it to a program that is
already running):

```json
{
  "name": "worker",
  "binary": "/usr/local/bin/worker",
  "args": ["-id", "{instance}"],
  "env_file": "/etc/worker.env",
  "dir": "/var/lib/worker",
  "instances": 4,
  "restart": "on-failure",
  "stop_timeout": "60s",
  "limits": {"nofile": 65536}
}
```

Put secrets in `env_file`, which is read at start and never copied. Every
field is in [docs/daemon.md](docs/daemon.md#units).

## Where it runs

| You ran | macOS | Linux | Survives a reboot with nobody logged in |
|---|---|---|---|
| `sudo amarit install --system` | LaunchDaemon | systemd system service | yes |
| `amarit install` | LaunchAgent | systemd user service plus linger | Linux: yes, when linger can be enabled. macOS: no, it starts at login |
| neither, just `amarit run` | detached daemon | detached daemon | no: it survives closing the terminal, not a reboot |

In every case the daemon and your programs run as you. The CLI talks to the
daemon only through files in `~/.amarit`: there is no socket and no port.
The full trust model, including what is written where and who owns each
file, is in [docs/daemon.md](docs/daemon.md#who-runs-as-whom).

## Install options

The script detects macOS or Linux on amd64 or arm64, downloads the binary
from the latest [release](https://github.com/sudiptadeb/amarit/releases),
checks it against `SHA256SUMS`, and installs it to `/usr/local/bin`, or
`~/.local/bin` when that is not writable.

```sh
curl -fsSL https://raw.githubusercontent.com/sudiptadeb/amarit/main/install.sh | AMARIT_VERSION=v0.6.0 sh       # a specific release
curl -fsSL https://raw.githubusercontent.com/sudiptadeb/amarit/main/install.sh | AMARIT_INSTALL_DIR=~/bin sh     # a specific directory
go install github.com/sudiptadeb/amarit/cmd/amarit@latest                                                        # from source, into ~/go/bin
```

To remove it, stop your programs (`amarit stop <name>`), run `amarit
uninstall` (`sudo amarit uninstall` for a macOS `--system` install; on
Linux, `sudo systemctl disable --now amarit-$USER` and delete its unit
file), then delete the binary and `~/.amarit`.

## For developers: programs that update themselves

amarit is also a Go library. Embed it, and every installed copy of your
program can update itself from a release manifest. It verifies the
download, swaps the binary, restarts in place with the same PID, and rolls
back if the new version is not healthy.

```go
import "github.com/sudiptadeb/amarit"

func main() {
    amarit.Start() // reads the amarit.json stamped into the build
    // your program as before
}
```

```sh
go build -ldflags "$(amarit stamp -version 1.2.0)" ./cmd/app   # stamp the version and amarit.json in
amarit release -version 1.2.0 -dist release                    # write releases.json for the release
app update                                                     # what your users run
app --auto-update                                              # or: check hourly and update
```

Updates are opt-in. Nothing updates unless the user passes
`--auto-update`, the program runs under amarit, or the project makes
auto-update its default. Open connections and terminals can be handed
across the restart, and an Android library reads the same manifest.

The full guide is [docs/updates.md](docs/updates.md), the manifest format
is [docs/spec.md](docs/spec.md), and the reasoning behind the design is
[docs/design.md](docs/design.md).

## Status

Early. The daemon, hash-verified updates, descriptor
handoff and the Android library work end to end. Next on the roadmap:
overlap restarts, signed and enterprise tiers, unit health probes, adopting
existing services, Windows, and spec 1.0.

MIT. See [LICENSE](LICENSE).
