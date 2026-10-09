// Package amarit lets a program keep itself current and keep itself
// running, whatever supervises it. Amar: undying. See README.md for the
// model and docs/spec.md for the manifest format.
//
// One call at the top of main is the whole integration:
//
//	amarit.Start()
//
// It reads the amarit.json stamped into the binary at build time, strips
// its own flags from os.Args, and does nothing unless the program was run
// with --auto-update, --update-check or the update subcommand.
package amarit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ErrNotImplemented is returned by entry points whose engine has not landed
// yet. See the roadmap in README.md.
var ErrNotImplemented = errors.New("amarit: not implemented yet")

// Config is everything a program tells amarit once. Start fills it from the
// stamped amarit.json; Options override single fields.
type Config struct {
	Project  string // must equal the manifest's "project"
	Version  string // the running version, usually stamped at build
	Releases string // the releases.json URL; --update-url overrides it
	Key      string // the author's minisign public key; empty = hash tier
	Exact    bool   // exact-match versions instead of semver order
	Restart  RestartStrategy

	Handoff func() []*os.File         // descriptors to carry across the restart
	Drain   func(ctx context.Context) // called before the restart, with a deadline
	Healthy func() bool               // gates rollback; nil means healthy on start
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
	// downtime; the PID changes, so the supervisor must be able to follow.
	Overlap
)

// Option adjusts the Config that Start builds from the stamp.
type Option func(*Config)

// Project sets the project name (normally stamped).
func Project(name string) Option { return func(c *Config) { c.Project = name } }

// Version sets the running version (normally stamped).
func Version(v string) Option { return func(c *Config) { c.Version = v } }

// Releases sets the manifest URL (normally stamped).
func Releases(url string) Option { return func(c *Config) { c.Releases = url } }

// Key sets the author's public key and enables the signed tier.
func Key(k string) Option { return func(c *Config) { c.Key = k } }

// ExactVersions switches from semver order to exact match.
func ExactVersions() Option { return func(c *Config) { c.Exact = true } }

// RestartWith chooses the restart strategy.
func RestartWith(s RestartStrategy) Option { return func(c *Config) { c.Restart = s } }

// Handoff registers the descriptors the new process must inherit.
func Handoff(fn func() []*os.File) Option { return func(c *Config) { c.Handoff = fn } }

// Drain registers the shutdown the engine runs before a restart.
func Drain(fn func(ctx context.Context)) Option { return func(c *Config) { c.Drain = fn } }

// Healthy registers the readiness check that gates rollback.
func Healthy(fn func() bool) Option { return func(c *Config) { c.Healthy = fn } }

// Options are what Start read from the command line.
type Options struct {
	AutoUpdate     bool
	Channel        string
	CheckOnce      bool
	ManifestURL    string
	Key            string
	AllowDowngrade bool
	Interval       time.Duration
	// Service is the "service <verb>" subcommand when one was given.
	Service string
	// Update is the "update" subcommand: check, apply, print the running
	// version, exit. The one-shot form for people and scripts.
	Update bool
}

var current *Updater

// Start is the one call a program makes. It builds the Config from the
// stamped amarit.json and the given options, parses amarit's own flags out
// of os.Args and strips them so the program's flag parsing never sees them,
// then does what the flags ask: nothing, one check (--update-check), a
// check-apply-exit (update), or a background loop (--auto-update). A
// successful update replaces the process image, so there is no "after" for
// the old version.
func Start(opts ...Option) Options {
	cfg, err := stampedConfig()
	if err != nil {
		log.Printf("amarit: %v", err)
	}
	for _, o := range opts {
		o(&cfg)
	}
	return run(cfg)
}

func run(cfg Config) Options {
	argv := append([]string(nil), os.Args...)
	opts, rest := parseArgs(os.Args[1:])
	os.Args = append(os.Args[:1], rest...)
	if opts.Channel == "" {
		opts.Channel = "stable"
	}
	if opts.Interval == 0 {
		opts.Interval = defaultInterval
	}
	if !opts.AutoUpdate && !opts.CheckOnce && !opts.Update {
		return opts
	}
	if cfg.Releases == "" && opts.ManifestURL == "" {
		log.Printf("amarit: no releases URL in this build; updates are off")
		if opts.Update {
			os.Exit(1)
		}
		return opts
	}
	u, err := newUpdater(cfg, opts, argv)
	if err != nil {
		log.Printf("amarit: %v", err)
		return opts
	}
	current = u
	ok := u.reconcile()
	if opts.Update {
		// A successful apply execs the new binary with this same command
		// line; the new process lands here again, reports "already on",
		// and exits. So the user sees the final version either way.
		fine := u.once(context.Background())
		fmt.Fprintf(os.Stderr, "%s %s\n", cfg.Project, strings.TrimPrefix(cfg.Version, "v"))
		if !fine {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if opts.CheckOnce {
		u.once(context.Background())
	}
	if opts.AutoUpdate && ok {
		go u.loop(context.Background(), opts.Interval)
	}
	return opts
}

// Apply updates to a version the program learned about through its own
// control channel. The version must be a release in the manifest; the
// channel pointer is not consulted. On success the process image is
// replaced and Apply never returns.
func Apply(ctx context.Context, version string) error {
	u := current
	if u == nil {
		return errors.New("amarit: Start was not called, or updates are not enabled")
	}
	d, err := u.Check(ctx)
	if err != nil {
		return err
	}
	r, ok := d.Manifest.Releases[version]
	if !ok {
		return fmt.Errorf("amarit: manifest has no release %s", version)
	}
	a, ok := r.Assets[u.target]
	if !ok {
		return fmt.Errorf("amarit: release %s has no asset for %s", version, u.target)
	}
	return u.Apply(ctx, &Decision{Manifest: d.Manifest, Version: version, Asset: a, Update: true, Reason: "requested"})
}

// Inherited returns the descriptors a previous process handed over through
// the restart, in the order its Handoff returned them. Empty on a fresh start.
func Inherited() []*os.File {
	return nil
}

// parseArgs separates amarit's flags from the program's own arguments.
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
		case "update":
			if i == 0 {
				o.Update = true
				continue
			}
			rest = append(rest, a)
		default:
			rest = append(rest, a)
		}
	}
	return o, rest
}
