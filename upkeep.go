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
	"os"
	"strings"
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

// Options are what Run read from the command line.
type Options struct {
	AutoUpdate     bool
	Channel        string
	CheckOnce      bool
	ManifestURL    string
	Key            string
	KeyURL         string
	AllowDowngrade bool
	// Service is the "service <verb>" subcommand when one was given.
	Service string
}

// Run parses upkeep's own flags out of os.Args, strips them so the program's
// own flag parsing never sees them, and returns what it found. When the
// engine lands, Run will also start the background loop for --auto-update
// and handle the service subcommands before returning.
//
// Without --auto-update, --update-check or a service subcommand, Run does
// nothing: updating is consent, and its absence is the off switch.
func Run(cfg Config) Options {
	opts, rest := parseArgs(os.Args[1:])
	os.Args = append(os.Args[:1], rest...)
	if opts.Channel == "" {
		opts.Channel = "stable"
	}
	return opts
}

// Apply updates to a target the program learned about through its own
// control channel, running the same engine as the background loop.
func Apply(ctx context.Context, version string) error {
	return ErrNotImplemented
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
