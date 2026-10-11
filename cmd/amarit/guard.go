package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// finding is another supervisor that already runs a program.
type finding struct {
	what   string
	remove string
}

// guard refuses to add a program a second supervisor already keeps running,
// since the two restart it in a loop; -force overrides. The probe is best
// effort: when it cannot look, it finds nothing.
func guard(binary string, force bool) error {
	found := otherSupervisors(binary)
	if len(found) == 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s is already kept running by another supervisor:\n", binary)
	for _, f := range found {
		fmt.Fprintf(os.Stderr, "  %s\n    remove it: %s\n", f.what, f.remove)
	}
	if force {
		fmt.Fprintln(os.Stderr, "continuing anyway (-force)")
		return nil
	}
	return errors.New("two supervisors for one program restart it in a loop; remove the other first, or add -force")
}

func otherSupervisors(binary string) []finding {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return scanLaunchd([]string{"/Library/LaunchDaemons", "/Library/LaunchAgents", filepath.Join(home, "Library", "LaunchAgents")}, binary)
	case "linux":
		return scanSystemd([]string{
			"/etc/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system",
			"/etc/systemd/user", "/usr/lib/systemd/user", filepath.Join(home, ".config", "systemd", "user"),
		}, binary)
	}
	return nil
}

// sameProgram compares paths with symlinks resolved, so a versioned binary
// behind a stable link still matches.
func sameProgram(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// scanLaunchd reads the plists in dirs and reports jobs whose program is
// binary. Binary plists do not parse and are skipped.
func scanLaunchd(dirs []string, binary string) []finding {
	var out []finding
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".plist") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			jobLabel, program := plistProgram(data)
			if jobLabel == label || program == "" || !sameProgram(program, binary) {
				continue
			}
			domain, sudo := "gui/"+uid(), ""
			if strings.HasPrefix(dir, "/Library/LaunchDaemons") {
				domain = "system"
			}
			if strings.HasPrefix(dir, "/Library/") {
				sudo = "sudo "
			}
			out = append(out, finding{
				what:   fmt.Sprintf("launchd job %s (%s)", jobLabel, path),
				remove: fmt.Sprintf("%slaunchctl bootout %s/%s && %srm %s", sudo, domain, jobLabel, sudo, path),
			})
		}
	}
	return out
}

// plistProgram pulls Label and the program path (Program, else the first
// ProgramArguments entry) out of an XML plist. Only keys of the top-level
// dict count; EnvironmentVariables and the like nest their own.
func plistProgram(data []byte) (jobLabel, program string) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth, key := 0, ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return jobLabel, program
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "dict" || t.Name.Local == "array" {
				depth--
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "dict", "array":
				depth++
				if key != "ProgramArguments" || t.Name.Local != "array" {
					key = ""
				}
			case "key":
				var k string
				_ = dec.DecodeElement(&k, &t)
				if depth == 1 {
					key = k
				}
			case "string":
				var v string
				_ = dec.DecodeElement(&v, &t)
				switch {
				case depth == 1 && key == "Label":
					jobLabel = v
				case depth == 1 && key == "Program":
					program = v
				case depth == 2 && key == "ProgramArguments" && program == "":
					program = v
				}
				key = ""
			}
		}
	}
}

// scanSystemd reads the service files in dirs and reports units whose
// ExecStart runs binary.
func scanSystemd(dirs []string, binary string) []finding {
	var out []finding
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".service") || strings.HasPrefix(e.Name(), "amarit") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if program := execStart(path); program == "" || !sameProgram(program, binary) {
				continue
			}
			cmd := "sudo systemctl disable --now " + e.Name()
			if strings.Contains(dir, "/user") {
				cmd = "systemctl --user disable --now " + e.Name()
			}
			out = append(out, finding{what: fmt.Sprintf("systemd unit %s (%s)", e.Name(), path), remove: cmd})
		}
	}
	return out
}

// execStart returns the program of the unit file's ExecStart line, without
// systemd's prefix characters.
func execStart(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
			if fields := strings.Fields(strings.TrimLeft(v, "-@:+!")); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}
