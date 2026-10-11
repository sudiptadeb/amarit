package amarit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// ProjectConfig is amarit.json at a project's repository root. The release
// tool reads it to build releases.json and to stamp the binary; the binary
// reads its copy at startup, stamped in or handed over with ProjectFile.
// One file, both sides.
type ProjectConfig struct {
	Project  string `json:"project"`
	Releases string `json:"releases"` // the releases.json URL
	Key      string `json:"key,omitempty"`
	// Restart is "in-place" (default) or "overlap".
	Restart string `json:"restart,omitempty"`
	// Interval is how often --auto-update checks, a Go duration such as
	// "5m" or "6h". Default one hour.
	Interval string `json:"interval,omitempty"`
	// AutoUpdate true makes the update loop this program's default; its
	// users opt out with --no-auto-update or AMARIT_AUTO_UPDATE=0.
	AutoUpdate bool           `json:"auto_update,omitempty"`
	Service    *ServiceConfig `json:"service,omitempty"`
	Release    *ReleaseConfig `json:"release,omitempty"`
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

// readBuildInfo is debug.ReadBuildInfo; a variable so a test can hand in
// what `go install` would have recorded.
var readBuildInfo = debug.ReadBuildInfo

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
	if c.Interval != "" {
		if d, err := time.ParseDuration(c.Interval); err != nil || d <= 0 {
			return nil, fmt.Errorf("amarit.json: interval %q must be a positive duration like 5m or 6h", c.Interval)
		}
	}
	return &c, nil
}

// ProjectFile hands Start the program's amarit.json as bytes, for a program
// that carries the file itself:
//
//	//go:embed amarit.json
//	var projectFile []byte
//
//	amarit.Start(amarit.ProjectFile(projectFile))
//
// It is the same file `amarit stamp` puts in with -ldflags, for builds that
// get no linker flags (`go install`). An invalid file is reported once at
// Start and changes nothing.
func ProjectFile(data []byte) Option {
	return func(c *Config) {
		p, err := ParseProjectConfig(data)
		if err != nil {
			if c.err == nil {
				c.err = err
			}
			return
		}
		p.apply(c)
	}
}

// apply lays the file's fields over c. The file says only what it has a
// value for: an absent key, interval or auto_update leaves the layer below.
func (p *ProjectConfig) apply(c *Config) {
	c.Project, c.Releases = p.Project, p.Releases
	if p.Key != "" {
		c.Key = p.Key
	}
	switch p.Restart {
	case "in-place":
		c.Restart = InPlace
	case "overlap":
		c.Restart = Overlap
	}
	if p.Interval != "" {
		c.Interval, _ = time.ParseDuration(p.Interval) // validated by ParseProjectConfig
	}
	if p.AutoUpdate {
		c.AutoUpdate = true
	}
}

// resolve is the one place amarit's configuration comes together. Layers,
// later ones winning:
//
//  1. the module version Go recorded in the binary (`go install` and
//     builds from a checkout), as the version when nobody says otherwise;
//  2. the amarit.json and version stamped at build with -ldflags;
//  3. the program's options in the order given, ProjectFile among them;
//  4. the environment: AMARIT_MANAGED=1, AMARIT_AUTO_UPDATE=1|0,
//     AMARIT_UPDATE_INTERVAL;
//  5. the command line, already parsed into opts: --update-url,
//     --update-key, --update-interval, --auto-update, --no-auto-update.
//
// Nothing is derived: every value here was written down by the toolchain,
// the release, the author, the operator or the user. The error, when there
// is one, is an amarit.json that did not parse; the Config is still the
// best of the other layers.
func resolve(options []Option, opts Options) (Config, error) {
	var cfg Config
	if bi, ok := readBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		cfg.Version = bi.Main.Version
	}
	if stampedVersion != "" {
		cfg.Version = stampedVersion
	}
	if stampedProject != "" {
		if raw, err := base64.StdEncoding.DecodeString(stampedProject); err != nil {
			cfg.err = fmt.Errorf("stamped amarit.json is not base64: %w", err)
		} else {
			ProjectFile(raw)(&cfg)
		}
	}
	for _, o := range options {
		o(&cfg)
	}

	if os.Getenv("AMARIT_MANAGED") == "1" {
		cfg.AutoUpdate = true
	}
	switch os.Getenv("AMARIT_AUTO_UPDATE") {
	case "0":
		cfg.AutoUpdate = false
	case "1":
		cfg.AutoUpdate = true
	}
	if d, err := time.ParseDuration(os.Getenv("AMARIT_UPDATE_INTERVAL")); err == nil && d > 0 {
		cfg.Interval = d
	}

	if opts.ManifestURL != "" {
		cfg.Releases = opts.ManifestURL
	}
	if opts.Key != "" {
		cfg.Key = opts.Key
	}
	if opts.Interval > 0 {
		cfg.Interval = opts.Interval
	}
	if opts.AutoUpdate {
		cfg.AutoUpdate = true
	}
	if opts.NoAutoUpdate {
		cfg.AutoUpdate = false
	}

	cfg.Version = strings.TrimPrefix(cfg.Version, "v")
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	return cfg, cfg.err
}

// BuildVersion is the version this build carries before the program says
// anything: the stamp, else the module version Go recorded, else "". It
// starts nothing, so a program's version banner (which the smoke test of
// an update runs as `<bin> -version`) can print the same version the
// updater compares.
func BuildVersion() string {
	cfg, _ := resolve(nil, Options{})
	return cfg.Version
}
