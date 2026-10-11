package main

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/sudiptadeb/amarit"
)

// settle is how long a rolling restart watches each unit before moving on.
const settle = 2 * time.Second

// restart asks the daemon to restart every unit or instance a pattern names.
func restart(args []string) error {
	args, rolling := takeFlag(args, "rolling")
	if len(args) == 0 {
		return errors.New("usage: amarit restart <unit|glob>... [-rolling]")
	}
	base := amarit.DefaultBase()
	units, err := base.Units()
	if err != nil {
		return err
	}
	if base.DaemonPID() == 0 {
		return errors.New("no daemon running")
	}
	targets, err := matchUnits(args, units)
	if err != nil {
		return err
	}
	if !rolling {
		t0 := time.Now()
		for _, name := range targets {
			if err := base.RequestRestart(name); err != nil {
				return err
			}
		}
		for _, name := range targets {
			if _, err := waitRestarted(base, name, t0, graceOf(units, name)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range targets {
		t0 := time.Now()
		if err := base.RequestRestart(name); err != nil {
			return err
		}
		s, err := waitRestarted(base, name, t0, graceOf(units, name))
		if err != nil {
			return err
		}
		// Up is not enough; it has to stay up before the next one goes.
		time.Sleep(settle)
		now, _ := base.Statuses()
		if cur := now[name]; cur.State != "running" || cur.PID != s.PID {
			return fmt.Errorf("%s: exited after the restart (%s); stopping the roll", name, cur.LastExit)
		}
	}
	return nil
}

// takeFlag removes -name or --name wherever it appears, since
// `amarit restart web-* -rolling` reads naturally.
func takeFlag(args []string, name string) ([]string, bool) {
	var rest []string
	found := false
	for _, a := range args {
		if a == "-"+name || a == "--"+name {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

// matchUnits expands names and glob patterns to instance names, in the
// order given. A pattern matches a unit (all its instances) or instances.
func matchUnits(patterns []string, units []amarit.Unit) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, pat := range patterns {
		hit := false
		for _, u := range units {
			for _, name := range u.InstanceNames() {
				if ok, _ := path.Match(pat, u.Name); !ok {
					if ok, _ = path.Match(pat, name); !ok {
						continue
					}
				}
				hit = true
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
		if !hit {
			return nil, fmt.Errorf("nothing matches %s", pat)
		}
	}
	return out, nil
}

// graceOf is the stop timeout of the unit an instance belongs to.
func graceOf(units []amarit.Unit, name string) time.Duration {
	unit, _, _ := strings.Cut(name, "@")
	for _, u := range units {
		if u.Name == unit {
			return u.StopGrace()
		}
	}
	return amarit.Unit{}.StopGrace()
}

// waitRestarted waits for the daemon to have started the unit again after
// t0 and reports how it came up.
func waitRestarted(base amarit.Base, name string, t0 time.Time, grace time.Duration) (amarit.Status, error) {
	deadline := time.Now().Add(grace + 30*time.Second)
	for time.Now().Before(deadline) {
		statuses, _ := base.Statuses()
		s := statuses[name]
		if s.Since.After(t0) {
			if s.State == "running" {
				fmt.Printf("%s restarted, pid %d\n", name, s.PID)
				return s, nil
			}
			return s, fmt.Errorf("%s: %s after the restart (see amarit logs %s)", name, s.LastExit, name)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return amarit.Status{}, fmt.Errorf("%s: the daemon did not restart it within %s", name, grace+30*time.Second)
}
