package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sudiptadeb/amarit"
)

// The daemon repeats amarit.json in code because embed cannot reach the
// repository root; this is what keeps the copy honest. Anything the root
// file says that the daemon does not (a key, an interval) fails it too.
func TestSelfConfigMatchesProjectFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "amarit.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := amarit.ParseProjectConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	got.Release, got.Service = nil, nil // the release side; the binary does not carry them
	want := amarit.ProjectConfig{Project: selfProject, Releases: selfReleases, AutoUpdate: selfAutoUpdate}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("amarit.json says %+v, cmd/amarit says %+v", *got, want)
	}
}

// The stamp must name the linker variables the library actually reads; a
// wrong name builds fine and leaves the binary with no configuration.
func TestStampNamesTheLibraryVariables(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "amarit.json")
	os.WriteFile(cfg, []byte(`{"project":"demo","releases":"releases.json"}`), 0o644)
	out := filepath.Join(dir, "out")
	f, _ := os.Create(out)
	stdout := os.Stdout
	os.Stdout = f
	err := stamp([]string{"-config", cfg, "-version", "v1.2.3"})
	os.Stdout = stdout
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), "amarit.stampedProject=") || !strings.Contains(string(got), "amarit.stampedVersion=1.2.3") {
		t.Fatalf("stamp printed %q", got)
	}
}

// The android target has no arch, carries the versionCode CI passes in,
// and a rolling channel names its asset by a fixed name with the run's
// -asset and -url; -prune forgets the builds no channel points at.
func TestReleaseAndroid(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "amarit.json")
	os.WriteFile(cfg, []byte(`{"project":"app","releases":"https://h/releases/download/latest/releases.json",
		"release":{"targets":["android"],"asset":"app-v{version}.apk","url":"https://h/releases/download/v{version}/{asset}"}}`), 0o644)
	dist := filepath.Join(dir, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, "app-v0.2.1.apk"), []byte("stable"), 0o644)
	os.WriteFile(filepath.Join(dist, "app-latest.apk"), []byte("rolling"), 0o644)
	out := filepath.Join(dist, "releases.json")

	if err := release([]string{"-config", cfg, "-version", "v0.2.1", "-dist", dist, "-out", out}); err == nil {
		t.Fatal("android without -version-code must be refused")
	}
	if err := release([]string{"-config", cfg, "-version", "v0.2.1", "-version-code", "3", "-dist", dist, "-out", out}); err != nil {
		t.Fatal(err)
	}
	args := []string{"-config", cfg, "-version", "0.2.1+gabc1234", "-version-code", "3", "-channel", "latest",
		"-asset", "app-latest.apk", "-url", "https://h/releases/download/latest/{asset}", "-dist", dist, "-out", out}
	if err := release(args); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	m, err := amarit.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	v, _, a, ok := m.Lookup("stable", "android")
	if !ok || v != "0.2.1" || a.VersionCode != 3 || a.URL != "https://h/releases/download/v0.2.1/app-v0.2.1.apk" || a.Size != 6 {
		t.Fatalf("stable/android = %s %+v %v", v, a, ok)
	}
	v, _, a, ok = m.Lookup("latest", "android")
	if !ok || v != "0.2.1+gabc1234" || a.VersionCode != 3 || a.URL != "https://h/releases/download/latest/app-latest.apk" || a.Size != 7 {
		t.Fatalf("latest/android = %s %+v %v", v, a, ok)
	}

	// A second rolling build with -prune: the old one goes, stable stays.
	args[3] = "0.2.1+gdef5678"
	if err := release(append(args, "-prune")); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(out)
	m, _ = amarit.ParseManifest(raw)
	if _, ok := m.Releases["0.2.1+gabc1234"]; ok {
		t.Fatal("-prune must drop a release no channel points at")
	}
	if _, ok := m.Releases["0.2.1"]; !ok || m.Channels["latest"] != "0.2.1+gdef5678" {
		t.Fatalf("after prune: %+v", m.Channels)
	}
}
