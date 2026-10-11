package main

import (
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/sudiptadeb/amarit"
)

// settle is how long a rolling restart watches each unit before moving on.
const settle = 2 * time.Second

// target is one instance to restart, with the stop timeout of its unit.
type target struct {
	name  string
	grace time.Duration
}

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
	if rolling {
		for _, t := range targets {
			if err := restartOne(base, t); err != nil {
				return err
			}
		}
		return nil
	}
	t0 := time.Now()
	for _, t := range targets {
		if err := base.RequestRestart(t.name); err != nil {
			return err
		}
	}
	for _, t := range targets {
		if _, err := waitRestarted(base, t, t0); err != nil {
			return err
		}
	}
	return nil
}

// restartOne is one step of a rolling restart: up is not enough, the unit
// has to stay up before the next one goes.
func restartOne(base amarit.Base, t target) error {
	t0 := time.Now()
	if err := base.RequestRestart(t.name); err != nil {
		return err
	}
	s, err := waitRestarted(base, t, t0)
	if err != nil {
		return err
	}
	time.Sleep(settle)
	now, _ := base.Statuses()
	if cur := now[t.name]; cur.State != "running" || cur.PID != s.PID {
		return fmt.Errorf("%s: exited after the restart (%s); stopping the roll", t.name, cur.LastExit)
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

// matchUnits expands names and glob patterns to instances, in the order
// given. A pattern matches a unit (all its instances) or single instances.
func matchUnits(patterns []string, units []amarit.Unit) ([]target, error) {
	seen := map[string]bool{}
	var out []target
	for _, pat := range patterns {
		hit := false
		for _, u := range units {
			whole, _ := path.Match(pat, u.Name)
			for _, name := range u.InstanceNames() {
				if ok, _ := path.Match(pat, name); !ok && !whole {
					continue
				}
				hit = true
				if !seen[name] {
					seen[name] = true
					out = append(out, target{name, u.StopGrace()})
				}
			}
		}
		if !hit {
			return nil, fmt.Errorf("nothing matches %s", pat)
		}
	}
	return out, nil
}

// waitRestarted waits for the daemon to have started the unit again after
// t0 and reports how it came up.
func waitRestarted(base amarit.Base, t target, t0 time.Time) (amarit.Status, error) {
	limit := t.grace + 30*time.Second
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		statuses, _ := base.Statuses()
		s := statuses[t.name]
		if s.Since.After(t0) {
			if s.State == "running" {
				fmt.Printf("%s restarted, pid %d\n", t.name, s.PID)
				return s, nil
			}
			return s, fmt.Errorf("%s: %s after the restart (see amarit logs %s)", t.name, s.LastExit, t.name)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return amarit.Status{}, fmt.Errorf("%s: the daemon did not restart it within %s", t.name, limit)
}
