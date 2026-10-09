# amarit manifest spec, version 1 (draft)

Status: draft. Breaking changes are allowed until a 1.0 tag of this repository.
A client that sees a `spec` value it does not know must refuse the manifest.

## 1. Files

| File | Required | Purpose |
|---|---|---|
| `releases.json` | yes | the manifest: channels, versions, assets, policy |
| `releases.json.sig` | enterprise tier | detached signature of the manifest by the publisher's key |
| `<asset>.sig` | signed tier | detached signature of each asset by the author's key; may instead be inlined as `sig` |
| `amarit-key.json` | rotation | a successor key, signed by the current key |

All files are plain UTF-8 JSON or base64 text, fetched over HTTPS. A publisher
on GitHub attaches them to a release so
`https://github.com/<owner>/<repo>/releases/latest/download/releases.json` is a
stable URL. Any HTTPS host works; nothing in the spec depends on GitHub.

## 2. Manifest

```json
{
  "spec": 1,
  "project": "termulaa",
  "published": "2026-10-09T10:00:00Z",
  "expires": "2026-11-09T10:00:00Z",
  "min_version": "0.5.0",
  "notes": "https://github.com/sudiptadeb/termulaa/releases",
  "channels": { "stable": "0.5.2", "canary": "0.5.3" },
  "releases": {
    "0.5.3": {
      "rollout": 100, "critical": false,
      "assets": {
        "darwin/arm64": { "url": "https://…", "sha256": "…", "size": 11345920, "sig": "RWQ…" },
        "linux/amd64":  { "url": "https://…", "sha256": "…", "size": 11829248 },
        "android":      { "url": "https://…/termulaa.apk", "sha256": "…", "size": 4102144, "version_code": 4 }
      }
    }
  }
}
```

| Field | Type | Required | Meaning |
|---|---|---|---|
| `spec` | integer | yes | manifest format version; this document describes `1` |
| `project` | string | yes | must equal the client's compiled-in project name, or the manifest is refused |
| `published` | RFC 3339 | yes | when this manifest was written |
| `expires` | RFC 3339 | no | after this instant the client treats the manifest as stale: it still applies `min_version`, logs a warning, and does not take new versions from it. Freshness protection against a frozen mirror |
| `min_version` | version | no | nobody should run anything older. A client below it updates even in check-only mode; a client below it that cannot update logs loudly on every check. Raising it past a bad release is the kill switch |
| `notes` | URL | no | human-readable release notes |
| `channels` | object | yes | channel name → version. A client follows exactly one channel; every version named must be a key of `releases` |
| `releases` | object | yes | version → release |
| `releases.*.rollout` | integer 0–100 | no, default 100 | percentage of installs that take this release, keyed on the install ID (section 5) |
| `releases.*.critical` | boolean | no, default false | apply at the next check regardless of the interval; the program's `Drain` is still honoured |
| `releases.*.assets` | object | yes | target → asset |
| `assets.*.url` | URL | yes | where the asset is downloaded from |
| `assets.*.sha256` | hex | yes | digest of the asset bytes |
| `assets.*.size` | integer | yes | byte length; checked during streaming so an oversized response is cut off |
| `assets.*.sig` | base64 | signed tier | minisign-format ed25519 signature of the asset by the author's key |
| `assets.*.version_code` | integer | android | the APK `versionCode`, which Android compares instead of the version string |

### Targets

`<os>/<arch>` from Go's `GOOS/GOARCH` for binaries: `darwin/arm64`,
`darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`,
`windows/arm64`. `android` for an APK. A client picks its own target and ignores
the rest; a manifest with no asset for the client's target is not an error, it
means "nothing for you".

## 3. Versions

Two modes, chosen by the client at compile time:

- **semver** (default): versions are [semantic versions](https://semver.org)
  without a leading `v`. Order is semver order. The client never applies a
  version lower than the one it runs unless started with `--allow-downgrade`.
- **exact**: versions are opaque strings matching `[A-Za-z0-9._+-]+`. A target
  is applied when it differs from the running version. `min_version` is ignored
  in this mode. This exists for projects whose versions are build timestamps.

## 4. Trust tiers

| Tier | Checks | Configuration |
|---|---|---|
| hash | TLS; `sha256` and `size` of each asset; `expires`; no downgrade | none |
| signed | hash tier plus `sig` on each asset against the author's public key | public key compiled into the client; may be overridden by `--update-key` |
| enterprise | signed tier plus `releases.json.sig` against a manifest key | manifest key from `--update-key`, `--update-key-url`, or the client's config file |

The author's asset key and the publisher's manifest key are separate. Assets are
signed by whoever built them; the manifest by whoever decides what a fleet runs.
Both are optional. A client configured with a key refuses anything that fails
against it; a client without a key never pretends to verify.

Signatures use the [minisign](https://jedisct1.github.io/minisign/) format
(ed25519, prehashed), so existing tooling can produce and check them.

## 5. Install ID and rollout

On first run with updates enabled the client generates 16 random bytes, stores
them beside its state, and never changes them. A channel with `rollout: N`
applies to an install when `uint32(first 4 bytes) % 100 < N`. A manifest can
therefore raise the percentage over several publishes and the same installs stay
selected.

## 6. Key rotation

`amarit-key.json`:

```json
{ "spec": 1, "project": "termulaa", "key": "RWT…new…", "valid_from": "2026-11-01T00:00:00Z", "sig": "…" }
```

`sig` is the signature of the document, with the `sig` field removed, by the key
the client currently trusts. The client accepts the new key only when that
signature verifies, then pins it and uses it for all later checks. A key
document that does not chain from a trusted key is refused. Revocation is
publishing a successor signed by the compromised key before an attacker does,
which is why the author's key must not live in CI.

## 7. Client state

One sidecar directory beside the binary, `<bin>.amarit/`:

| File | Purpose |
|---|---|
| `id` | the install id, section 5 |
| `state.json` | the attempted version, the version running before it, starts since the attempt, and the last version that reported healthy. If the process finds itself still on the old version with an attempt recorded, it holds and logs instead of looping; three starts without a healthy mark trigger rollback |
| `prev` | the previous binary, kept for rollback |

## 8. Project configuration: `amarit.json`

One JSON file at the repository root, read by `amarit release` to build the
manifest and stamped into the binary so the program hardcodes nothing:

```json
{
  "project": "termulaa",
  "releases": "https://github.com/sudiptadeb/termulaa/releases/latest/download/releases.json",
  "key": "RWQ…",
  "restart": "overlap",
  "interval": "5m",
  "service": {
    "instances": 1,
    "env_file": "data/app.env",
    "args": ["serve"],
    "restart": "on-failure",
    "no_restart_exit": [4, 5, 7],
    "health": "http://127.0.0.1:17380/health"
  },
  "release": { "targets": ["darwin/arm64", "linux/amd64"] }
}
```

| Field | Meaning |
|---|---|
| `project`, `releases` | required; the manifest's `project` must match |
| `key` | the author's minisign public key; present means the signed tier |
| `restart` | `in-place` (default) or `overlap` for zero-downtime handover |
| `interval` | how often `--auto-update` checks, a Go duration (default `1h`); `amarit.Interval()` in code, `AMARIT_UPDATE_INTERVAL` and `--update-interval` override it in that order. Exact-match versioning (section 3) is `amarit.ExactVersions()` in code |
| `service.*` | how `service install` runs the program: instance count, environment file, arguments, restart policy, exit codes that must not restart, readiness probe |
| `release.targets` | the `os/arch` list the release tool builds and lists in the manifest |

The stamp is two linker variables on the `amarit` package:
`stampedProject` (base64 of the file) and `stampedVersion`. A build without them
is a development build; `amarit.Start()` then does nothing and says so only
when asked (`--update-check`).
