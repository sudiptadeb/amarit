// Package upkeep lets a program update itself from a manifest URL and keep
// itself running, whatever supervises it. See README.md for the model and
// docs/spec.md for the manifest format.
//
// The package is at the design stage: the manifest parser and version
// ordering are implemented and tested; the update engine, the exec handoff
// and the service tier are not yet. Run parses its flags and returns.
package upkeep

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ErrNotImplemented is returned by every entry point whose engine has not
// landed yet. The API shape is settled; see the roadmap in README.md.
var ErrNotImplemented = errors.New("upkeep: not implemented yet")

// Config is everything a program tells upkeep once, at startup.
type Config struct {
	// Project must equal the "project" field of the manifest.
	Project string
	// Version is the running version, usually set with -ldflags.
	Version string
	// Manifest is the compiled-in manifest URL; --update-url overrides it.
	Manifest string
	// Key is the author's minisign public key. Empty means the hash tier.
	Key string
	// Exact switches version comparison from semver order to exact match,
	// for projects whose versions are opaque build stamps.
	Exact bool
	// Restart selects in-place exec (default) or an overlapping handover
	// with zero downtime. See docs/design.md, "Restart strategies".
	Restart RestartStrategy

	// Handoff returns descriptors the new process must inherit across the
	// exec: listening sockets, PTY masters. Nil for a stateless program.
	Handoff func() []*os.File
	// Drain is called before the exec with a context that the engine
	// cancels at a hard deadline. Nil means no drain.
	Drain func(ctx context.Context)
	// Healthy gates rollback: the new process writes its healthy marker the
	// first time this returns true. Nil means healthy on start.
	Healthy func() bool
}

// RestartStrategy is how the new binary takes over from the running one.
type RestartStrategy int

const (
	// InPlace execs the new binary with the same PID and inherited
	// descriptors. Works under every supervisor; a short pause while the
	// old process drains.
	InPlace RestartStrategy = iota
	// Overlap starts the new process beside the old one on the same
	// listeners and drains the old one once the new is ready. Zero
	// downtime; the PID changes, so the supervisor must be able to follow
	// (systemd notify, a pid file, upkeeper). Not available under launchd.
	Overlap
)

// Options are what Run read from the command line.
type Options struct {
	AutoUpdate     bool
	Channel        string
	CheckOnce      bool
	ManifestURL    string
	Key            string
	KeyURL         string
	AllowDowngrade bool
	Interval       time.Duration
	// Service is the "service <verb>" subcommand when one was given.
	Service string
}

var current *Updater

// Run parses upkeep's own flags out of os.Args and strips them so the
// program's own flag parsing never sees them. With --update-check it checks
// the manifest once, right now, and applies what it finds; with
// --auto-update it does the same on an interval in the background. Either
// way it returns and the program carries on; a successful update replaces
// the process image, so there is no "after" for the old version.
//
// Without --auto-update, --update-check or a service subcommand, Run does
// nothing: updating is consent, and its absence is the off switch.
func Run(cfg Config) Options {
	argv := append([]string(nil), os.Args...)
	opts, rest := parseArgs(os.Args[1:])
	os.Args = append(os.Args[:1], rest...)
	if opts.Channel == "" {
		opts.Channel = "stable"
	}
	if opts.Interval == 0 {
		opts.Interval = defaultInterval
	}
	if !opts.AutoUpdate && !opts.CheckOnce {
		return opts
	}
	if cfg.Manifest == "" && opts.ManifestURL == "" {
		log.Printf("upkeep: no manifest URL configured; --auto-update ignored")
		return opts
	}
	u, err := newUpdater(cfg, opts, argv)
	if err != nil {
		log.Printf("upkeep: %v", err)
		return opts
	}
	current = u
	ok := u.reconcile()
	if opts.CheckOnce {
		u.once(context.Background())
	}
	if opts.AutoUpdate && ok {
		go u.loop(context.Background(), opts.Interval)
	}
	return opts
}

// Apply updates to a version the program learned about through its own
// control channel. The version must be listed in the manifest's assets;
// the channel pointer is not consulted. On success the process image is
// replaced and Apply never returns.
func Apply(ctx context.Context, version string) error {
	u := current
	if u == nil {
		return errors.New("upkeep: Run was not called, or updates are not enabled")
	}
	d, err := u.Check(ctx)
	if err != nil {
		return err
	}
	a, ok := d.Manifest.Assets[version][u.target]
	if !ok {
		return fmt.Errorf("upkeep: manifest has no %s asset for %s", u.target, version)
	}
	return u.Apply(ctx, &Decision{Manifest: d.Manifest, Version: version, Asset: a, Update: true, Reason: "requested"})
}

// Inherited returns the descriptors a previous process handed over through
// the exec, in the order its Handoff returned them. Empty on a fresh start.
func Inherited() []*os.File {
	return nil
}

// parseArgs separates upkeep's flags from the program's own arguments.
func parseArgs(args []string) (Options, []string) {
	var o Options
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		next := func() string {
			if hasValue {
				return value
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch name {
		case "--auto-update":
			o.AutoUpdate = true
			if hasValue {
				o.Channel = value
			}
		case "--update-check":
			o.CheckOnce = true
		case "--update-url":
			o.ManifestURL = next()
		case "--update-key":
			o.Key = next()
		case "--update-key-url":
			o.KeyURL = next()
		case "--allow-downgrade":
			o.AllowDowngrade = true
		case "--update-interval":
			if d, err := time.ParseDuration(next()); err == nil {
				o.Interval = d
			}
		case "service":
			if i == 0 && i+1 < len(args) {
				o.Service = args[i+1]
				i++
				continue
			}
			rest = append(rest, a)
		default:
			rest = append(rest, a)
		}
	}
	return o, rest
}
