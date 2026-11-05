# The amarit daemon

The reference for running programs with amarit: what a unit can say, how
the daemon keeps itself current, and exactly who runs as whom. The
[README](../README.md) has the quick start.

## Units

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
The daemon never touches a unit's binary: updates are the program's own
business, below.

## The daemon updates itself

The daemon does keep *itself* current, using the library like any other
program: it declares auto-update its default (amarit's own `amarit.json`
says the same), so `amarit daemon` checks
[amarit's releases](https://github.com/sudiptadeb/amarit/releases) hourly,
verifies, swaps the binary and restarts in place. The pid does not change,
launchd or systemd notice nothing, and the units stay its children and are
adopted with their pids; a version that cannot come up three times is
rolled back. The log says so: `amarit v0.5.2: daemon 4745 supervising
...` and `auto-update on: amarit 0.5.2, channel stable, every 1h0m0s`.
`AMARIT_AUTO_UPDATE=0` in the job's environment, or `amarit daemon
--no-auto-update`, turns it off. Only the daemon checks; `amarit ls` and
the other short commands run whatever is installed. A `go install` build
knows its version from the module Go recorded in it and updates the same
way: it takes the next release, not the one it already is.

## Who runs as whom

amarit grants no privilege you did not have. The daemon and every unit run
as the user who installed it, never as root; root is used once, to register
the daemon with the init system, and not again.

```text
init system: launchd / systemd                        runs as root
  |
  +-- service file, root-owned, names a user
  |     /Library/LaunchDaemons/com.amarit.daemon.plist   UserName=you
  |     /etc/systemd/system/amarit-you.service           User=you
  |
  +--> amarit daemon                                    runs as you
         reads  ~/.amarit/units/      (wanted state)
         writes ~/.amarit/run/, logs/ (observed state)
         |
         +--> units: web, worker@1..4, tunnel            run as you

  amarit run | stop | restart | update   --writes-->   ~/.amarit/
  (the CLI, as you; no socket, no port, no RPC)
```

| | `install` writes | owner | daemon runs as | survives a reboot with nobody logged in | sudo afterwards |
|---|---|---|---|---|---|
| macOS, `sudo amarit install --system` | `/Library/LaunchDaemons/com.amarit.daemon.plist`, `UserName=<you>` | root:wheel, 0644 | you | yes | only to remove the plist |
| macOS, `amarit install` | `~/Library/LaunchAgents/com.amarit.daemon.plist` | you | you | no: starts at login. With no desktop session (SSH into a headless Mac) a detached daemon is started instead | no |
| Linux, `sudo amarit install --system` | `/etc/systemd/system/amarit-<you>.service`, `User=<you>` | root, 0644 | you | yes | only to remove the unit |
| Linux, `amarit install` | `~/.config/systemd/user/amarit.service`, then `loginctl enable-linger <you>` | you | you | yes if lingering could be enabled; else the daemon stops at logout and the command prints the `loginctl` line to run | no |
| other | nothing: `install` refuses; `amarit run` starts a detached daemon | | you | no | no |

**`sudo amarit install --system`.** On macOS it writes the plist above,
owned by root as launchd requires, with `UserName` set to the account that
ran sudo, `HOME` and `AMARIT_HOME` pointing at that account's home, and
`ProgramArguments` naming the amarit binary that ran the install, symlinks
resolved (normally `~/go/bin/amarit`, owned by you). On Linux it writes the
unit above with `User=`, the same two environment variables, `ExecStart=<that
binary> daemon`, `Restart=always`, enabled for `multi-user.target`. It needs
`SUDO_USER`: run as plain root, or with `SUDO_USER=root`, it refuses, because
there is no user to name. `sudo amarit uninstall` removes the LaunchDaemon;
on Linux the system unit is removed by hand today (`sudo systemctl disable
--now amarit-<you>`, then delete the file).

**`amarit install`** without sudo takes what the account has: a LaunchAgent
when launchd has a session for your uid, a systemd user unit when
`systemctl --user` answers, with `loginctl enable-linger` attempted after it.
When neither is there, and when `amarit run` finds no daemon, it starts one
detached in its own session: that survives the terminal, not a reboot.

**Who the daemon runs as.** The installing user, in every form above.
Nothing refuses a daemon started as root by hand today (`sudo amarit run`
would create `/var/root/.amarit` and a detached root daemon); issue
[#4](https://github.com/sudiptadeb/amarit/issues/4) adds that guard.

**Who units run as.** The daemon's user. A unit has no user field. Each
unit starts in its own session, so its pid is its process-group id and a
stop reaches its children; a unit with `limits` is started through
`amarit __exec`, which sets them and execs the program. `env` comes from the
unit file, `env_file` is read at each start and never copied anywhere.

**How the CLI talks to the daemon.** Through files in `~/.amarit`, nothing
else: `units/<name>.json` is wanted state, `run/<name>.json` observed
state, `run/<name>.restart` and `run/<name>.stopped` are requests,
`daemon.pid` says who is running, `logs/<name>.log` is the output. The
daemon polls every two seconds. There is no socket and no port, so the
access rule is: whoever can write `~/.amarit` controls the daemon.

**Self-update without sudo.** The daemon's binary and every unit's binary
are files you own, so the library swaps them without root. That is safe
because the root-owned service file runs the user-writable binary *as you*.
The user line is the whole guarantee: were `UserName` or `User=` missing,
launchd or systemd would run a binary you can write as root, and anything
running as you could become root. amarit always writes the line and refuses
to install without a user to put in it; #4 adds the second check, a daemon
that refuses uid 0 and a `status` that warns about a service file with no
user.

**What it implies.** Whoever can write `~/.amarit/units` can make any
program run persistently as you, across reboots, without sudo. That is the
same power as your crontab or your shell's rc files, and it has the same
owner. Secrets belong in `env_file`, which the daemon reads at exec; `env`
is stored in the unit JSON. Today the directories are 0755 and the unit,
run, pid and log files 0644, so another local account that can traverse
your home can read them; issue
[#5](https://github.com/sudiptadeb/amarit/issues/5) tightens them to
0700/0600.

**Other platforms.** On anything but macOS and Linux, `amarit install`
refuses (with or without `--system`); `amarit run` starts a detached daemon
as you, with no reboot survival. On Windows there are also no process
groups, no `limits`, and no in-place self-update. Windows is on the roadmap.
