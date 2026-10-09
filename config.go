package amarit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ProjectConfig is amarit.json at a project's repository root. The release
// tool reads it to build releases.json and to stamp the binary; the binary
// reads the stamped copy at startup. One file, both sides.
type ProjectConfig struct {
	Project  string `json:"project"`
	Releases string `json:"releases"` // the releases.json URL
	Key      string `json:"key,omitempty"`
	// Restart is "in-place" (default) or "overlap".
	Restart string         `json:"restart,omitempty"`
	Service *ServiceConfig `json:"service,omitempty"`
	Release *ReleaseConfig `json:"release,omitempty"`
}

// ServiceConfig is how the program wants to be kept alive.
type ServiceConfig struct {
	Instances     int      `json:"instances,omitempty"`
	EnvFile       string   `json:"env_file,omitempty"`
	Args          []string `json:"args,omitempty"`
	RestartPolicy string   `json:"restart,omitempty"` // "always" (default) or "on-failure"
	NoRestartExit []int    `json:"no_restart_exit,omitempty"`
	Health        string   `json:"health,omitempty"` // URL or command the service answers when ready
}

// ReleaseConfig is what `amarit release` needs beyond the build itself.
// Asset and URL are patterns; {version}, {os}, {arch} and (in URL) {asset}
// are filled in per target.
type ReleaseConfig struct {
	Targets    []string `json:"targets"`
	Asset      string   `json:"asset,omitempty"`       // e.g. "termulaa-{os}-{arch}-v{version}"
	URL        string   `json:"url,omitempty"`         // e.g. "https://github.com/o/r/releases/download/v{version}/{asset}"
	MinVersion string   `json:"min_version,omitempty"` // floor to publish in every manifest
}

// Stamped by `amarit stamp` (or any build) with
//
//	-ldflags "-X github.com/sudiptadeb/amarit.stampedProject=<base64 amarit.json> -X github.com/sudiptadeb/amarit.stampedVersion=0.5.3"
//
// so that the program needs no embed directive and no arguments.
var (
	stampedProject string
	stampedVersion string
)

// ParseProjectConfig decodes and validates an amarit.json.
func ParseProjectConfig(data []byte) (*ProjectConfig, error) {
	var c ProjectConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("amarit.json: %w", err)
	}
	if c.Project == "" {
		return nil, fmt.Errorf("amarit.json: project is required")
	}
	if c.Releases == "" {
		return nil, fmt.Errorf("amarit.json: releases is required")
	}
	switch c.Restart {
	case "", "in-place", "overlap":
	default:
		return nil, fmt.Errorf("amarit.json: restart %q must be in-place or overlap", c.Restart)
	}
	return &c, nil
}

// stampedConfig is the Config a build carries. A binary built without the
// stamp (a plain `go build` during development) gets an empty Config and
// no error: amarit is simply not configured in that build.
func stampedConfig() (Config, error) {
	if stampedProject == "" {
		return Config{Version: stampedVersion}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(stampedProject)
	if err != nil {
		return Config{}, fmt.Errorf("stamped amarit.json is not base64: %w", err)
	}
	c, err := ParseProjectConfig(raw)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Project:  c.Project,
		Version:  strings.TrimPrefix(stampedVersion, "v"),
		Releases: c.Releases,
		Key:      c.Key,
	}
	if c.Restart == "overlap" {
		cfg.Restart = Overlap
	}
	return cfg, nil
}
