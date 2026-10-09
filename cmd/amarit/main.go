// Command amarit is the release side of the library: it stamps builds and
// writes the releases.json a project publishes next to its release assets.
//
//	amarit stamp   [-config amarit.json] -version 0.5.3
//	amarit release [-config amarit.json] -version 0.5.3 -dist dist [-channel stable] [-out releases.json]
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sudiptadeb/amarit"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "stamp":
		err = stamp(os.Args[2:])
	case "release":
		err = release(os.Args[2:])
	case "install":
		err = install(os.Args[2:])
	case "uninstall":
		err = uninstall(os.Args[2:])
	case "daemon":
		err = daemon(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "ls", "status":
		err = ls(os.Args[2:])
	case "stop":
		err = named(os.Args[2:], func(n string) error { return setEnabled(n, false) })
	case "start":
		err = named(os.Args[2:], func(n string) error { return setEnabled(n, true) })
	case "rm":
		err = named(os.Args[2:], amarit.DefaultBase().RemoveUnit)
	case "logs":
		err = logs(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Println(buildVersion())
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "amarit:", err)
		os.Exit(1)
	}
}

var version = "dev"

// buildVersion is the -X stamp when a release set one, else the module
// version `go install` recorded, else "dev".
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func usage() {
	fmt.Fprintln(os.Stderr, `amarit: keep it alive, keep it current.

keep it alive (this machine):
  amarit install [--system]        keep the amarit daemon itself running; --system needs sudo once,
                                   after which 'amarit run' never does
  amarit run <binary> [args...]    keep a program running (unit named after the binary)
  amarit ls                        every unit, its state, pid, restarts
  amarit logs [-f] <unit>          a unit's output
  amarit stop|start|rm <unit>
  amarit uninstall

keep it current (release side):
  amarit stamp   [-config amarit.json] -version X                print the -ldflags that stamp a build
  amarit release [-config amarit.json] -version X -dist DIR      write releases.json for the assets in DIR

A program built with amarit updates itself: '<program> update' or '<program> --auto-update'.`)
}

func named(args []string, fn func(string) error) error {
	if len(args) != 1 {
		return errors.New("one unit name is required")
	}
	return fn(args[0])
}

func loadConfig(path string) (*amarit.ProjectConfig, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	c, err := amarit.ParseProjectConfig(raw)
	return c, raw, err
}

// stamp prints the linker flags that put amarit.config and the version into
// the binary, so a build script can do: go build -ldflags "$(amarit stamp -version X)".
func stamp(args []string) error {
	fs := flag.NewFlagSet("stamp", flag.ContinueOnError)
	config := fs.String("config", "amarit.config", "project configuration")
	version := fs.String("version", "", "version being built")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		return fmt.Errorf("-version is required")
	}
	_, raw, err := loadConfig(*config)
	if err != nil {
		return err
	}
	fmt.Printf("-X github.com/sudiptadeb/amarit.stampedConfig=%s -X github.com/sudiptadeb/amarit.stampedVersion=%s\n",
		base64.StdEncoding.EncodeToString(raw), strings.TrimPrefix(*version, "v"))
	return nil
}

// release hashes the built assets and writes the manifest. The asset file
// name and its download URL come from patterns in amarit.config, with
// {version}, {os}, {arch} and {asset} filled in.
func release(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	config := fs.String("config", "amarit.config", "project configuration")
	version := fs.String("version", "", "version being released")
	dist := fs.String("dist", "dist", "directory holding the built assets")
	channel := fs.String("channel", "stable", "channel to point at this version")
	out := fs.String("out", "amarit.json", "manifest to write")
	expires := fs.Duration("expires", 0, "how long the manifest stays fresh (0 = no expiry)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		return fmt.Errorf("-version is required")
	}
	c, _, err := loadConfig(*config)
	if err != nil {
		return err
	}
	if c.Release == nil || len(c.Release.Targets) == 0 {
		return fmt.Errorf("%s: release.targets is empty", *config)
	}
	if c.Release.Asset == "" || c.Release.URL == "" {
		return fmt.Errorf("%s: release.asset and release.url patterns are required", *config)
	}
	v := strings.TrimPrefix(*version, "v")

	m := amarit.Manifest{
		Spec:      amarit.SpecVersion,
		Project:   c.Project,
		Published: time.Now().UTC().Truncate(time.Second),
		Channels:  map[string]string{},
		Releases:  map[string]amarit.Release{},
	}
	if *expires > 0 {
		e := m.Published.Add(*expires)
		m.Expires = &e
	}
	// Keep what an existing manifest already says about other channels and
	// versions, so promoting canary never forgets stable.
	if old, err := os.ReadFile(*out); err == nil {
		if om, err := amarit.ParseManifest(old); err == nil && om.Project == c.Project {
			m.Channels, m.Releases, m.MinVersion = om.Channels, om.Releases, om.MinVersion
		}
	}
	if c.Release.MinVersion != "" {
		m.MinVersion = c.Release.MinVersion
	}

	assets := map[string]amarit.Asset{}
	for _, target := range c.Release.Targets {
		goos, goarch, _ := strings.Cut(target, "/")
		fill := strings.NewReplacer("{version}", v, "{os}", goos, "{arch}", goarch)
		name := fill.Replace(c.Release.Asset)
		path := findAsset(*dist, name)
		if path == "" {
			return fmt.Errorf("%s: no file named %s under %s", target, name, *dist)
		}
		sum, size, err := digest(path)
		if err != nil {
			return err
		}
		url := strings.NewReplacer("{version}", v, "{os}", goos, "{arch}", goarch, "{asset}", name).Replace(c.Release.URL)
		assets[target] = amarit.Asset{URL: url, SHA256: sum, Size: size}
		fmt.Fprintf(os.Stderr, "  %-14s %s  %d bytes  %s\n", target, name, size, sum[:12])
	}
	m.Releases[v] = amarit.Release{Assets: assets}
	m.Channels[*channel] = v

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if _, err := amarit.ParseManifest(data); err != nil {
		return fmt.Errorf("refusing to write an invalid manifest: %w", err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %s %s -> %s\n", *out, c.Project, *channel, v)
	return nil
}

// findAsset looks for the file directly under dist or one level down
// (build scripts like dist/<os>/<file>).
func findAsset(dist, name string) string {
	for _, pattern := range []string{filepath.Join(dist, name), filepath.Join(dist, "*", name)} {
		if hits, _ := filepath.Glob(pattern); len(hits) > 0 {
			return hits[0]
		}
	}
	return ""
}

func digest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
