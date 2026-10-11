package main

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/sudiptadeb/amarit"
)

// amaritModule is how a binary built with the library shows in its build info.
const amaritModule = "github.com/sudiptadeb/amarit"

// update brings units onto the version their releases.json points at. Each
// program updates itself with its own `<binary> update`, which knows its
// manifest, verifies and swaps the file and keeps the previous one; that
// runs once per binary however many units share it. Then the daemon
// restarts the units whose binary changed, so a running process is never
// replaced under its feet. No names means every unit built with amarit.
func update(args []string) error {
	args, rolling := takeFlag(args, "rolling")
	args, check := takeFlag(args, "check")
	base := amarit.DefaultBase()
	units, err := base.Units()
	if err != nil {
		return err
	}
	groups, err := byBinary(args, units)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return errors.New("no unit runs a program built with amarit")
	}
	var failed []string
	for _, g := range groups {
		names := g.names()
		before, _, _ := digest(g.bin)
		sub := []string{"update"}
		if check {
			sub = append(sub, "--check")
		}
		fmt.Printf("==> %s (%s)\n", strings.Join(names, " "), g.bin)
		cmd := exec.Command(g.bin, sub...)
		cmd.Dir = g.units[0].Dir
		cmd.Env = os.Environ()
		for k, v := range g.units[0].Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("%s: update failed: %v\n", strings.Join(names, " "), err)
			failed = append(failed, names...)
			continue
		}
		if check {
			continue
		}
		if after, _, _ := digest(g.bin); after == before {
			fmt.Println("binary unchanged; nothing to restart")
			continue
		}
		if base.DaemonPID() == 0 {
			fmt.Println("no daemon running; the new binary runs from the next start")
			continue
		}
		restartArgs := names
		if rolling {
			restartArgs = append(restartArgs, "-rolling")
		}
		if err := restart(restartArgs); err != nil {
			fmt.Printf("%v\n", err)
			rollBack(g.bin, names)
			failed = append(failed, names...)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("not updated: %s", strings.Join(failed, " "))
	}
	return nil
}

// rollBack answers a restart that failed after an update: the previous
// binary goes back in place, the rejected one is kept as <bin>.amarit/bad,
// and every unit on it is restarted at once so the service is back fast.
// The program's own rollback cannot do this, since `<binary> update`
// already ran the new version once and found it healthy.
func rollBack(bin string, names []string) {
	side := bin + ".amarit"
	prev, bad := filepath.Join(side, "prev"), filepath.Join(side, "bad")
	fmt.Printf("ROLLING BACK %s to the previous binary\n", bin)
	if _, err := os.Stat(prev); err != nil {
		fmt.Printf("rollback failed: no previous binary at %s\n", prev)
		return
	}
	_ = os.Remove(bad)
	if err := os.Rename(bin, bad); err != nil {
		fmt.Printf("rollback failed: %v\n", err)
		return
	}
	if err := os.Rename(prev, bin); err != nil {
		_ = os.Rename(bad, bin)
		fmt.Printf("rollback failed: %v\n", err)
		return
	}
	if err := restart(names); err != nil {
		fmt.Printf("restart after the rollback: %v\n", err)
		return
	}
	fmt.Printf("%s run the previous version again; the rejected binary is %s.\n", strings.Join(names, " "), bad)
	fmt.Println("Publish a fixed release before updating again.")
}

// binGroup is one binary on disk and the units that run it.
type binGroup struct {
	bin   string
	units []amarit.Unit
}

func (g *binGroup) names() []string {
	var out []string
	for _, u := range g.units {
		out = append(out, u.Name)
	}
	return out
}

// byBinary picks the units the patterns name (all of them when there are
// none) and groups them by the file they run. A pattern matches a unit by
// its name or by any of its instances: the update is per binary, so one
// instance cannot be updated alone. A unit named outright whose program
// was not built with amarit is an error; with no names it is skipped and
// listed.
func byBinary(patterns []string, units []amarit.Unit) ([]*binGroup, error) {
	var picked []amarit.Unit
	if len(patterns) == 0 {
		picked = units
	}
	for _, pat := range patterns {
		hit := false
		for _, u := range units {
			if matchesUnit(pat, u) {
				hit = true
				picked = append(picked, u)
			}
		}
		if !hit {
			return nil, fmt.Errorf("no unit matches %q", pat)
		}
	}
	var groups []*binGroup
	index := map[string]*binGroup{}
	var skipped []string
	for _, u := range picked {
		bin, err := resolveBinary(u)
		if err == nil && !builtWithAmarit(bin) {
			err = fmt.Errorf("%s was not built with amarit, so it cannot update itself", bin)
		}
		if err != nil {
			if len(patterns) > 0 {
				return nil, fmt.Errorf("%s: %v", u.Name, err)
			}
			skipped = append(skipped, u.Name)
			continue
		}
		g := index[bin]
		if g == nil {
			g = &binGroup{bin: bin}
			index[bin] = g
			groups = append(groups, g)
		}
		if !containsUnit(g.units, u.Name) {
			g.units = append(g.units, u)
		}
	}
	if len(skipped) > 0 {
		fmt.Printf("skipping %s: not built with amarit\n", strings.Join(skipped, " "))
	}
	return groups, nil
}

func matchesUnit(pat string, u amarit.Unit) bool {
	if ok, _ := path.Match(pat, u.Name); ok {
		return true
	}
	for _, name := range u.InstanceNames() {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}

func containsUnit(units []amarit.Unit, name string) bool {
	for _, u := range units {
		if u.Name == name {
			return true
		}
	}
	return false
}

// resolveBinary is the file a unit runs, found the way the daemon finds it
// and with symlinks resolved, so units that share a binary share an update.
func resolveBinary(u amarit.Unit) (string, error) {
	bin := u.Binary
	if !strings.Contains(bin, "/") {
		p, err := exec.LookPath(bin)
		if err != nil {
			return "", err
		}
		bin = p
	} else if !filepath.IsAbs(bin) && u.Dir != "" {
		bin = filepath.Join(u.Dir, bin)
	}
	if r, err := filepath.EvalSymlinks(bin); err == nil {
		bin = r
	}
	return filepath.Abs(bin)
}

// builtWithAmarit reads the Go build info a binary carries.
func builtWithAmarit(bin string) bool {
	bi, err := buildinfo.ReadFile(bin)
	if err != nil {
		return false
	}
	if bi.Main.Path == amaritModule {
		return false // the amarit tool itself is not a unit's program
	}
	for _, d := range bi.Deps {
		if d.Path == amaritModule {
			return true
		}
	}
	return false
}
