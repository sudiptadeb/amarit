// Command upkeep is the release side of the library: it stamps builds and
// writes the manifest a project publishes next to its release assets.
//
//	upkeep stamp   [-config upkeep.config] -version 0.5.3
//	upkeep release [-config upkeep.config] -version 0.5.3 -dist dist [-channel stable] [-out upkeep.json]
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sudiptadeb/upkeep"
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
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "upkeep:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  upkeep stamp   [-config upkeep.config] -version X     print the -ldflags value that stamps a build
  upkeep release [-config upkeep.config] -version X -dist DIR [-channel stable] [-out upkeep.json]
                                                        write the manifest for the assets in DIR`)
}

func loadConfig(path string) (*upkeep.ProjectConfig, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	c, err := upkeep.ParseProjectConfig(raw)
	return c, raw, err
}

// stamp prints the linker flags that put upkeep.config and the version into
// the binary, so a build script can do: go build -ldflags "$(upkeep stamp -version X)".
func stamp(args []string) error {
	fs := flag.NewFlagSet("stamp", flag.ContinueOnError)
	config := fs.String("config", "upkeep.config", "project configuration")
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
	fmt.Printf("-X github.com/sudiptadeb/upkeep.stampedConfig=%s -X github.com/sudiptadeb/upkeep.stampedVersion=%s\n",
		base64.StdEncoding.EncodeToString(raw), strings.TrimPrefix(*version, "v"))
	return nil
}

// release hashes the built assets and writes the manifest. The asset file
// name and its download URL come from patterns in upkeep.config, with
// {version}, {os}, {arch} and {asset} filled in.
func release(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	config := fs.String("config", "upkeep.config", "project configuration")
	version := fs.String("version", "", "version being released")
	dist := fs.String("dist", "dist", "directory holding the built assets")
	channel := fs.String("channel", "stable", "channel to point at this version")
	out := fs.String("out", "upkeep.json", "manifest to write")
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

	m := upkeep.Manifest{
		Spec:      upkeep.SpecVersion,
		Project:   c.Project,
		Published: time.Now().UTC().Truncate(time.Second),
		Channels:  map[string]upkeep.Channel{},
		Assets:    map[string]map[string]upkeep.Asset{},
	}
	if *expires > 0 {
		e := m.Published.Add(*expires)
		m.Expires = &e
	}
	// Keep what an existing manifest already says about other channels and
	// versions, so promoting canary never forgets stable.
	if old, err := os.ReadFile(*out); err == nil {
		if om, err := upkeep.ParseManifest(old); err == nil && om.Project == c.Project {
			m.Channels, m.Assets, m.MinVersion = om.Channels, om.Assets, om.MinVersion
		}
	}
	if c.Release.MinVersion != "" {
		m.MinVersion = c.Release.MinVersion
	}

	assets := map[string]upkeep.Asset{}
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
		assets[target] = upkeep.Asset{URL: url, SHA256: sum, Size: size}
		fmt.Fprintf(os.Stderr, "  %-14s %s  %d bytes  %s\n", target, name, size, sum[:12])
	}
	m.Assets[v] = assets
	m.Channels[*channel] = upkeep.Channel{Version: v}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if _, err := upkeep.ParseManifest(data); err != nil {
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
