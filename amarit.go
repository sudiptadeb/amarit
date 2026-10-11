// Package amarit lets a program keep itself current and keep itself
// running, whatever supervises it. Amar: undying. See README.md for the
// model and docs/spec.md for the manifest format.
//
// One call at the top of main is the whole integration:
//
//	amarit.Start()
//
// It reads the project's amarit.json, stamped into the binary at build time
// or handed over with ProjectFile, or the same facts given as options;
// strips its own flags from os.Args; and does nothing unless the program
// was run with --auto-update, --update-check or the update subcommand, or
// the project declared auto-update its default ("auto_update": true) and
// the user did not say --no-auto-update.
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

// Config is everything a program tells amarit once. Start resolves it in
// one pass from the build (the stamped amarit.json, the module version Go
// recorded), the options, the environment and the command line, later
// layers winning; the engine then runs on the result.
type Config struct {
	Project  string // must equal the manifest's "project"
	Version  string // the running version: stamped, from the module, or set in code
	Releases string // the releases.json URL; --update-url overrides it
	Key      string // the author's minisign public key; empty = hash tier
	Exact    bool   // exact-match versions instead of semver order
	Restart  RestartStrategy
	Interval time.Duration // auto-update check cadence; zero means one hour
	// AutoUpdate makes the loop the program's default, so its users opt out
	// (--no-auto-update, AMARIT_AUTO_UPDATE=0) instead of in. False, the
	// default, means nothing updates unless asked.
	AutoUpdate bool

	Handoff func() []*os.File         // descriptors to carry across the restart
	Values  func() map[string]string  // small named values to carry across the restart
	Drain   func(ctx context.Context) // called before the restart, with a deadline
	Healthy func() bool               // gates rollback; nil means healthy on start

	err error // the first amarit.json that did not parse; Start reports it
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

// Option adjusts the Config Start resolves. Options are applied in the
// order given, over the stamp and under the environment and command line;
// a program with no amarit.json at all says everything through them.
type Option func(*Config)

// Project sets the project name (normally from amarit.json).
func Project(name string) Option { return func(c *Config) { c.Project = name } }

// Version sets the running version (normally stamped, or the module version
// Go recorded in the binary).
func Version(v string) Option { return func(c *Config) { c.Version = v } }

// Releases sets the manifest URL (normally from amarit.json).
func Releases(url string) Option { return func(c *Config) { c.Releases = url } }

// Key sets the author's public key and enables the signed tier.
func Key(k string) Option { return func(c *Config) { c.Key = k } }

// ExactVersions switches from semver order to exact match.
func ExactVersions() Option { return func(c *Config) { c.Exact = true } }

// RestartWith chooses the restart strategy.
func RestartWith(s RestartStrategy) Option { return func(c *Config) { c.Restart = s } }

// Interval sets how often the auto-update loop checks, overriding the
// `interval` in amarit.json. AMARIT_UPDATE_INTERVAL in the environment and
// --update-interval on the command line override both, in that order.
func Interval(d time.Duration) Option { return func(c *Config) { c.Interval = d } }

// AutoUpdate declares whether the update loop is this program's default,
// overriding `auto_update` in amarit.json. A program that says true updates
// unless its user says --no-auto-update or AMARIT_AUTO_UPDATE=0.
func AutoUpdate(on bool) Option { return func(c *Config) { c.AutoUpdate = on } }

// Handoff registers the descriptors the new process must inherit.
func Handoff(fn func() []*os.File) Option { return func(c *Config) { c.Handoff = fn } }

// HandoffValues registers small named values the new process must know
// (a child's pid, a port, a token) alongside the descriptors. The new
// process reads them once with InheritedValues.
func HandoffValues(fn func() map[string]string) Option { return func(c *Config) { c.Values = fn } }

// Drain registers the shutdown the engine runs before a restart.
func Drain(fn func(ctx context.Context)) Option { return func(c *Config) { c.Drain = fn } }

// Healthy registers the readiness check that gates rollback.
func Healthy(fn func() bool) Option { return func(c *Config) { c.Healthy = fn } }

// Options are what Start read from the command line.
type Options struct {
	AutoUpdate     bool
	NoAutoUpdate   bool // --no-auto-update: the user opting out of a program's default
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
	// UpdateCheck is "update --check": say what is available, apply nothing.
	UpdateCheck bool
}

var current *Updater

// Start is the one call a program makes. It parses amarit's own flags out
// of os.Args and strips them so the program's flag parsing never sees them,
// resolves the Config from the build, the given options, the environment
// and those flags, then does what the flags ask: nothing, one check
// (--update-check), a check-apply-exit (update), or a background loop
// (--auto-update). A successful update replaces the process image, so there
// is no "after" for the old version.
func Start(options ...Option) Options {
	argv := append([]string(nil), os.Args...)
	opts, rest := parseArgs(os.Args[1:])
	os.Args = append(os.Args[:1], rest...)
	cfg, err := resolve(options, opts)
	if err != nil {
		log.Printf("amarit: %v", err)
	}
	if opts.Channel == "" {
		opts.Channel = "stable"
	}
	opts.Interval = cfg.Interval
	if !opts.Update {
		opts.AutoUpdate = cfg.AutoUpdate
	}
	if !opts.AutoUpdate && !opts.CheckOnce && !opts.Update {
		return opts
	}
	if cfg.Releases == "" {
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
		check := u.once
		if opts.UpdateCheck {
			check = u.report
		}
		fine := check(context.Background())
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
		log.Printf("amarit: auto-update on: %s %s, channel %s, every %s", cfg.Project, strings.TrimPrefix(cfg.Version, "v"), opts.Channel, opts.Interval)
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
		case "--no-auto-update":
			o.NoAutoUpdate = true
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
		case "--check", "-check":
			if o.Update {
				o.UpdateCheck = true
				continue
			}
			rest = append(rest, a)
		default:
			rest = append(rest, a)
		}
	}
	return o, rest
}
