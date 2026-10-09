package upkeep

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// ProjectConfig is upkeep.config at a project's repository root. The release
// tool reads it to build the manifest and to stamp the binary; the binary
// reads the stamped copy at startup. One file, both sides.
type ProjectConfig struct {
	Project  string `json:"project"`
	Manifest string `json:"manifest"`
	Key      string `json:"key,omitempty"`
	// Versions is "semver" (default) or "exact".
	Versions string `json:"versions,omitempty"`
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

// ReleaseConfig is what `upkeep release` needs beyond the build itself.
type ReleaseConfig struct {
	Targets []string          `json:"targets"`
	Assets  map[string]string `json:"assets,omitempty"` // target -> built file path pattern
}

// Stamped by `upkeep release` (or any build) with
//
//	-ldflags "-X github.com/sudiptadeb/upkeep.stampedConfig=<base64 upkeep.config> -X github.com/sudiptadeb/upkeep.stampedVersion=0.5.3"
//
// so that the program itself needs no embed directive and no arguments.
var (
	stampedConfig  string
	stampedVersion string
)

// ParseProjectConfig decodes and validates an upkeep.config.
func ParseProjectConfig(data []byte) (*ProjectConfig, error) {
	var c ProjectConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("upkeep.config: %w", err)
	}
	if c.Project == "" {
		return nil, fmt.Errorf("upkeep.config: project is required")
	}
	if c.Manifest == "" {
		return nil, fmt.Errorf("upkeep.config: manifest is required")
	}
	switch c.Versions {
	case "", "semver", "exact":
	default:
		return nil, fmt.Errorf("upkeep.config: versions %q must be semver or exact", c.Versions)
	}
	switch c.Restart {
	case "", "in-place", "overlap":
	default:
		return nil, fmt.Errorf("upkeep.config: restart %q must be in-place or overlap", c.Restart)
	}
	return &c, nil
}

// Start is Run for a binary stamped by `upkeep release`: it decodes the
// stamped upkeep.config and version and hands them to Run. A binary built
// without the stamp gets an empty Options and a nil error: upkeep is simply
// not configured in that build, which is a plain `go build` during
// development.
func Start(hooks ...Hooks) (Options, error) {
	if stampedConfig == "" {
		return Options{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(stampedConfig)
	if err != nil {
		return Options{}, fmt.Errorf("upkeep: stamped config is not base64: %w", err)
	}
	c, err := ParseProjectConfig(raw)
	if err != nil {
		return Options{}, err
	}
	cfg := Config{
		Project:  c.Project,
		Version:  stampedVersion,
		Manifest: c.Manifest,
		Key:      c.Key,
		Exact:    c.Versions == "exact",
	}
	if c.Restart == "overlap" {
		cfg.Restart = Overlap
	}
	for _, h := range hooks {
		cfg.Handoff, cfg.Drain, cfg.Healthy = h.Handoff, h.Drain, h.Healthy
	}
	return Run(cfg), nil
}

// Hooks are the optional callbacks a stateful program gives Start.
type Hooks struct {
	Handoff func() []*os.File
	Drain   func(ctx context.Context)
	Healthy func() bool
}
