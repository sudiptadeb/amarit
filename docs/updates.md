# Self-updating programs

For developers: embed the amarit library so every installed copy of your
program can update itself from a release manifest, verify what it
downloads, swap in place and roll back on its own. The
[README](../README.md) has the short version, [spec.md](spec.md) the
manifest format.

## Make a program update itself

An `amarit.json` at the repository root is the project's configuration,
read by `amarit release` to write the manifest and carried by the binary
so it knows where to look:

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

The file reaches the binary one of two ways, the author's choice. Stamped
at build, so the program needs nothing but the one call:

```go
import "github.com/sudiptadeb/amarit"

func main() {
    amarit.Start() // reads the stamped amarit.json
    // the program as before
}
```

```sh
go build -ldflags "$(amarit stamp -version 0.5.3)" ./cmd/app
amarit release -version 0.5.3 -dist release        # writes releases.json
```

Or embedded by the program itself, for a project whose users install it
with `go install`, which passes no linker flags; the version is then the
module version Go records in every such build:

```go
//go:embed amarit.json           // a copy of the file next to main, or the file itself if main lives at the root
var projectFile []byte

func main() {
    amarit.Start(amarit.ProjectFile(projectFile))
}
```

A program with no `amarit.json` at all says the same things in code:
`amarit.Start(amarit.Project("app"), amarit.Releases(url), amarit.Version(v))`.
Whatever carried them, the values meet in one place; nothing is derived
from a module path or a hosting convention. A build that carries none of
them is inert and says so when asked (`app update --check`).

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

The layers, later ones winning: the module version Go recorded in the
binary (the version only), the stamped file, options in code in the order
given (`amarit.ProjectFile(data)`, `amarit.Interval(5*time.Minute)`,
`amarit.Key(k)`, ...), the environment, the command line. So `"interval":
"5m"` in `amarit.json` is the program's own cadence,
`AMARIT_UPDATE_INTERVAL=30m` in a unit's environment changes it for that
install, and `--update-interval` wins for one run. `amarit.BuildVersion()`
is the version the build carries, for the program's own banner.

Whether the loop runs at all follows the same layers. Auto-update is off
unless somebody said otherwise, and nothing is detected: a project that
wants its users to opt *out* instead of in puts `"auto_update": true` in
`amarit.json` (or `amarit.AutoUpdate(true)` in code); a program running as
an amarit unit has it on (the operator said "keep this current" by putting
it there); `AMARIT_AUTO_UPDATE=1` or `=0` in the environment overrides
both; `--auto-update` or `--no-auto-update` on the command line wins over
everything.

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

## Keep an Android app current

The same manifest, consumed from Kotlin: the library in [`android/`](android/)
checks it, compares against the installed `versionCode`, downloads and
verifies the APK, and hands it to the package installer, where the user taps
Install. Platform APIs only, minSdk 26.

```kotlin
// settings.gradle.kts: maven("https://jitpack.io") among the repositories
// build.gradle.kts:    implementation("com.github.sudiptadeb:amarit:v0.6.0")
// AndroidManifest.xml: <uses-permission android:name="android.permission.REQUEST_INSTALL_PACKAGES" />

override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)
    Amarit.start(this)          // reads assets/amarit.json; inert, and says so, without one
}
```

`assets/amarit.json` is the repository's `amarit.json`, copied in by the
build (a six-line Gradle task; see the etchosts project for one). With
`"auto_update": true` in it the app checks on a schedule and prompts; the
user's switch, `Amarit.current?.autoUpdate = false`, turns that off, and
`channel = "latest"` follows another channel. Without it, nothing runs
until the app's own button calls `checkNow(activity)`.

CI publishes the APK and the manifest together. With the amarit CLI
installed on the runner (`go install github.com/sudiptadeb/amarit/cmd/amarit@latest`):

```sh
amarit release -version 0.2.1 -version-code 3 -dist dist -out dist/releases.json
amarit release -version "0.2.1+g${sha::7}" -version-code 3 -channel latest \
    -asset app-latest.apk -url "https://github.com/o/r/releases/download/latest/{asset}" \
    -dist dist -out dist/releases.json -prune
```

The `android` target has no arch and carries the APK's `versionCode`; an
equal code with a new version string is a rebuild, which is how a rolling
channel updates phones on every push without bumping the code (spec,
section 3). On GitHub the manifest lives on the rolling pre-release
(`releases/download/latest/releases.json`), since `releases/latest/download/`
skips pre-releases. One signing key for every channel, or the platform
refuses the update.

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
