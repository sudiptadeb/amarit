package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sudiptadeb/amarit"
)

// amaritProgram builds a program that imports the library, the way a
// unit's binary would. The test binary itself cannot stand in: its main
// module is amarit, which builtWithAmarit deliberately does not count.
func amaritProgram(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.25\n\nrequire github.com/sudiptadeb/amarit v0.0.0\n\nreplace github.com/sudiptadeb/amarit => "+root+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"github.com/sudiptadeb/amarit\"\n\nfunc main() { amarit.Start() }\n"), 0o644)
	bin := filepath.Join(dir, "demo")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// Units that share a binary update once; a program not built with amarit
// is skipped when nothing is named and refused when it is; an instance
// name picks its unit.
func TestUpdateGroupsByBinary(t *testing.T) {
	demo := amaritProgram(t)
	units := []amarit.Unit{
		{Name: "web", Binary: demo},
		{Name: "worker", Binary: demo, Instances: 4},
		{Name: "shell", Binary: "/bin/sh"},
	}
	groups, err := byBinary(nil, units)
	if err != nil || len(groups) != 1 || len(groups[0].units) != 2 {
		t.Fatalf("all: %v %+v", err, groups)
	}
	if _, err := byBinary([]string{"shell"}, units); err == nil {
		t.Fatal("a named unit not built with amarit was accepted")
	}
	if groups, err := byBinary([]string{"worker@2"}, units); err != nil || len(groups) != 1 || groups[0].units[0].Name != "worker" {
		t.Fatalf("instance name: %v %+v", err, groups)
	}
}
