package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
