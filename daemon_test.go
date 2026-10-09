package amarit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDaemonStartsRestartsAndStops(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	sleeper := script(t, dir, "sleeper", `echo "up $$ $AMARIT_UNIT"; exec sleep 60`)
	flaky := script(t, dir, "flaky", `echo boom; exit 3`)

	if err := base.SaveUnit(Unit{Name: "sleeper", Binary: sleeper}); err != nil {
		t.Fatal(err)
	}
	if err := base.SaveUnit(Unit{Name: "flaky", Binary: flaky, Restart: "on-failure"}); err != nil {
		t.Fatal(err)
	}

	d := NewDaemon(base)
	d.tick = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var pid int
	waitFor(t, "sleeper to run", func() bool {
		s, _ := base.Statuses()
		pid = s["sleeper"].PID
		return s["sleeper"].State == "running" && pid != 0 && processAlive(pid)
	})
	waitFor(t, "sleeper log with the unit env", func() bool {
		b, _ := os.ReadFile(filepath.Join(base.logs(), "sleeper.log"))
		return strings.Contains(string(b), "up ") && strings.Contains(string(b), "sleeper")
	})
	waitFor(t, "flaky to be restarted with backoff", func() bool {
		s, _ := base.Statuses()
		return s["flaky"].Restarts >= 1 && strings.Contains(s["flaky"].LastExit, "exit status 3")
	})

	// A unit whose policy says never: one run, then parked.
	once := script(t, dir, "once", `exit 0`)
	if err := base.SaveUnit(Unit{Name: "once", Binary: once, Restart: "never"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "once to exit and stay down", func() bool {
		s, _ := base.Statuses()
		return s["once"].State == "exited" && s["once"].Restarts == 0
	})

	// Removing the unit stops the process.
	if err := base.RemoveUnit("sleeper"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sleeper to be stopped", func() bool {
		s, _ := base.Statuses()
		_, still := s["sleeper"]
		return !still && !processAlive(pid)
	})
}

func TestDaemonAdoptsAfterRestart(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	sleeper := script(t, dir, "sleeper", `exec sleep 60`)
	if err := base.SaveUnit(Unit{Name: "sleeper", Binary: sleeper}); err != nil {
		t.Fatal(err)
	}

	first := NewDaemon(base)
	first.tick = 100 * time.Millisecond
	ctx1, cancel1 := context.WithCancel(context.Background())
	go first.Run(ctx1)
	var pid int
	waitFor(t, "first daemon to start sleeper", func() bool {
		s, _ := base.Statuses()
		pid = s["sleeper"].PID
		return pid != 0
	})
	cancel1()
	waitFor(t, "first daemon to release its pid file", func() bool { return base.DaemonPID() == 0 })
	if !processAlive(pid) {
		t.Fatal("stopping the daemon must not stop its units")
	}

	second := NewDaemon(base)
	second.tick = 100 * time.Millisecond
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go second.Run(ctx2)
	waitFor(t, "second daemon to adopt", func() bool {
		s, _ := base.Statuses()
		return s["sleeper"].PID == pid && s["sleeper"].State == "running"
	})
	if err := base.RemoveUnit("sleeper"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "adopted process to be stopped", func() bool { return !processAlive(pid) })
}

func TestUnitValidationAndEnvFile(t *testing.T) {
	base := Base(t.TempDir())
	if err := base.SaveUnit(Unit{Name: "bad name", Binary: "/bin/true"}); err == nil {
		t.Fatal("a name with a space must be refused")
	}
	if err := base.SaveUnit(Unit{Name: "x"}); err == nil {
		t.Fatal("a unit without a binary must be refused")
	}
	envFile := filepath.Join(string(base), "app.env")
	os.WriteFile(envFile, []byte("# comment\nexport A=1\nB=\"two words\"\n\nC='3'\n"), 0o600)
	got, err := readEnvFile(envFile)
	if err != nil || strings.Join(got, ",") != "A=1,B=two words,C=3" {
		t.Fatalf("env file parsed as %v (%v)", got, err)
	}
}
