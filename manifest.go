package amarit

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// SpecVersion is the manifest format this package understands. A manifest
// with any other "spec" value is refused rather than guessed at.
const SpecVersion = 1

// Manifest is one project's releases.json: which version each channel
// points at, and the assets of every version. See docs/spec.md.
type Manifest struct {
	Spec       int                `json:"spec"`
	Project    string             `json:"project"`
	Published  time.Time          `json:"published"`
	Expires    *time.Time         `json:"expires,omitempty"`
	MinVersion string             `json:"min_version,omitempty"`
	Notes      string             `json:"notes,omitempty"`
	Channels   map[string]string  `json:"channels"`
	Releases   map[string]Release `json:"releases"`
}

// Release is one published version: how it rolls out and what to download.
type Release struct {
	Rollout  *int             `json:"rollout,omitempty"`
	Critical bool             `json:"critical,omitempty"`
	Assets   map[string]Asset `json:"assets"`
}

// Asset is one downloadable file for one target.
type Asset struct {
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Sig         string `json:"sig,omitempty"`
	VersionCode int    `json:"version_code,omitempty"`
}

var (
	exactVersionRe = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
	sha256Re       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseManifest decodes and validates a manifest. Validation is strict: a
// channel that names a version with no release, a bad digest, or an unknown
// spec number all fail here, before anything is downloaded.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Spec != SpecVersion {
		return nil, fmt.Errorf("manifest: spec %d not supported (want %d)", m.Spec, SpecVersion)
	}
	if m.Project == "" {
		return nil, errors.New("manifest: project is required")
	}
	if m.Published.IsZero() {
		return nil, errors.New("manifest: published is required")
	}
	if len(m.Channels) == 0 {
		return nil, errors.New("manifest: at least one channel is required")
	}
	if m.MinVersion != "" && !exactVersionRe.MatchString(m.MinVersion) {
		return nil, fmt.Errorf("manifest: min_version %q has invalid characters", m.MinVersion)
	}
	for name, version := range m.Channels {
		if !exactVersionRe.MatchString(version) {
			return nil, fmt.Errorf("manifest: channel %q version %q has invalid characters", name, version)
		}
		if _, ok := m.Releases[version]; !ok {
			return nil, fmt.Errorf("manifest: channel %q points at version %q, which has no release", name, version)
		}
	}
	for version, r := range m.Releases {
		if !exactVersionRe.MatchString(version) {
			return nil, fmt.Errorf("manifest: version %q has invalid characters", version)
		}
		if r.Rollout != nil && (*r.Rollout < 0 || *r.Rollout > 100) {
			return nil, fmt.Errorf("manifest: %s: rollout %d is not within 0-100", version, *r.Rollout)
		}
		for target, a := range r.Assets {
			if a.URL == "" {
				return nil, fmt.Errorf("manifest: %s %s: url is required", version, target)
			}
			if !sha256Re.MatchString(a.SHA256) {
				return nil, fmt.Errorf("manifest: %s %s: sha256 must be 64 lowercase hex characters", version, target)
			}
			if a.Size <= 0 {
				return nil, fmt.Errorf("manifest: %s %s: size must be positive", version, target)
			}
		}
	}
	return &m, nil
}

// Stale reports whether the manifest has passed its expiry. A stale manifest
// still carries min_version, but a client takes no new version from it.
func (m *Manifest) Stale(now time.Time) bool {
	return m.Expires != nil && now.After(*m.Expires)
}

// Lookup resolves a channel to its version, release and the asset for a
// target such as "darwin/arm64" or "android". A manifest with nothing for
// the target is not an error; ok is false and the caller moves on.
func (m *Manifest) Lookup(channel, target string) (version string, r Release, a Asset, ok bool) {
	version, ok = m.Channels[channel]
	if !ok {
		return "", Release{}, Asset{}, false
	}
	r, ok = m.Releases[version]
	if !ok {
		return "", Release{}, Asset{}, false
	}
	a, ok = r.Assets[target]
	if !ok {
		return "", Release{}, Asset{}, false
	}
	return version, r, a, true
}

// InRollout decides whether an install takes a release, from the install's
// fixed random id and the release's rollout percentage. The same id stays
// selected as the percentage rises, so a rollout widens instead of
// reshuffling.
func InRollout(installID []byte, rollout *int) bool {
	pct := 100
	if rollout != nil {
		pct = *rollout
	}
	if pct >= 100 {
		return true
	}
	if pct <= 0 || len(installID) < 4 {
		return false
	}
	return binary.BigEndian.Uint32(installID[:4])%100 < uint32(pct)
}
