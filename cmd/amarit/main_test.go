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
