package amarit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The daemon starts a unit with limits through its own executable, which in
// tests is this binary, so it plays the helper when started that way.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == execSubcommand {
		if err := ExecLimited(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

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
	if err := base.SaveUnit(Unit{Name: "x", Binary: "/bin/true", Limits: map[string]uint64{"nproc": 1}}); err == nil {
		t.Fatal("an unknown limit name must be refused")
	}
	envFile := filepath.Join(string(base), "app.env")
	os.WriteFile(envFile, []byte("# comment\nexport A=1\nB=\"two words\"\n\nC='3'\n"), 0o600)
	got, err := readEnvFile(envFile)
	if err != nil || strings.Join(got, ",") != "A=1,B=two words,C=3" {
		t.Fatalf("env file parsed as %v (%v)", got, err)
	}
}

func TestStopKillsTheProcessGroupAfterTheGrace(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	childPID := filepath.Join(dir, "child.pid")
	// Ignores SIGTERM and passes that on to its child, so only the kill of
	// the whole group after the grace period can end either of them.
	stubborn := script(t, dir, "stubborn", `trap '' TERM; sleep 300 & echo $! > `+childPID+`; wait`)
	if err := base.SaveUnit(Unit{Name: "stubborn", Binary: stubborn, StopTimeout: "1s"}); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(base)
	d.tick = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var pid, child int
	waitFor(t, "stubborn and its child to run", func() bool {
		s, _ := base.Statuses()
		pid = s["stubborn"].PID
		b, _ := os.ReadFile(childPID)
		child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return pid != 0 && child != 0
	})
	t0 := time.Now()
	if err := base.RemoveUnit("stubborn"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell and its child to be gone", func() bool { return !processAlive(pid) && !processAlive(child) })
	if took := time.Since(t0); took > 6*time.Second {
		t.Fatalf("stop took %s; stop_timeout 1s was not honoured", took)
	}
}

func TestNeverPolicyRunsOnce(t *testing.T) {
	// Regression: the parked unit was re-run on the next tick because the
	// unit file's "enabled" overwrote the park.
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	runs := filepath.Join(dir, "runs")
	once := script(t, dir, "once", `echo run >> `+runs)
	if err := base.SaveUnit(Unit{Name: "once", Binary: once, Restart: "never"}); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(base)
	d.tick = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	waitFor(t, "once to exit", func() bool {
		s, _ := base.Statuses()
		return s["once"].State == "exited"
	})
	time.Sleep(500 * time.Millisecond)
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "run") != 1 {
		t.Fatalf("a never unit ran %d times", strings.Count(string(b), "run"))
	}
}

func TestInstancesRunNamedCopies(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	worker := script(t, dir, "worker", `echo "$AMARIT_UNIT $AMARIT_INSTANCE $WORKER_ID $1" > `+dir+`/$1; exec sleep 60`)
	u := Unit{Name: "worker", Binary: worker, Args: []string{"w{instance}"}, Env: map[string]string{"WORKER_ID": "id-{instance}"}, Instances: 2}
	if err := base.SaveUnit(u); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(base)
	d.tick = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var pid1, pid2 int
	var seen string
	waitFor(t, "both instances to run and report", func() bool {
		s, _ := base.Statuses()
		pid1, pid2 = s["worker@1"].PID, s["worker@2"].PID
		b, _ := os.ReadFile(filepath.Join(dir, "w2"))
		seen = strings.TrimSpace(string(b))
		return pid1 != 0 && pid2 != 0 && seen != ""
	})
	if seen != "worker@2 2 id-2 w2" {
		t.Fatalf("instance 2 saw %q", seen)
	}

	// Fewer instances: the highest number goes, the rest keep their pid.
	u.Instances = 1
	if err := base.SaveUnit(u); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker@2 to be stopped", func() bool {
		s, _ := base.Statuses()
		_, still := s["worker@2"]
		return !still && !processAlive(pid2)
	})
	if s, _ := base.Statuses(); s["worker@1"].PID != pid1 {
		t.Fatalf("worker@1 was restarted (pid %d -> %d)", pid1, s["worker@1"].PID)
	}

	// One instance can be stopped and started on its own.
	if err := base.Enable("worker@1", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker@1 to be stopped", func() bool { return !processAlive(pid1) })
	if err := base.Enable("worker@1", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker@1 to run again", func() bool {
		s, _ := base.Statuses()
		return s["worker@1"].State == "running" && s["worker@1"].PID != pid1
	})
	if err := base.Enable("worker@7", false); err == nil {
		t.Fatal("an instance that does not exist must be refused")
	}
}

func TestRequestedRestartIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	sleeper := script(t, dir, "sleeper", `exec sleep 60`)
	if err := base.SaveUnit(Unit{Name: "sleeper", Binary: sleeper}); err != nil {
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
		return pid != 0
	})
	d.children["sleeper"].backoff = time.Minute // as if it had been failing
	if err := base.RequestRestart("sleeper"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sleeper to run with a new pid", func() bool {
		s, _ := base.Statuses()
		return s["sleeper"].State == "running" && s["sleeper"].PID != 0 && s["sleeper"].PID != pid
	})
	s, _ := base.Statuses()
	if s["sleeper"].Restarts != 0 || s["sleeper"].LastExit != "" || d.children["sleeper"].backoff != time.Second {
		t.Fatalf("after a requested restart: %+v, backoff %s", s["sleeper"], d.children["sleeper"].backoff)
	}
	if processAlive(pid) {
		t.Fatal("the old process must be gone")
	}
}

func TestLogRotatesWhileRunning(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	chatty := script(t, dir, "chatty", `while :; do echo tick; sleep 0.1; done`)
	if err := base.SaveUnit(Unit{Name: "chatty", Binary: chatty}); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(base)
	d.tick = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	logPath := filepath.Join(base.logs(), "chatty.log")
	waitFor(t, "chatty to write", func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), "tick")
	})
	// Fill the live log past the limit behind the program's back.
	if err := os.WriteFile(logPath, bytes.Repeat([]byte("x"), maxLogSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the log to be rotated and written to again", func() bool {
		prev, err := os.Stat(logPath + ".1")
		b, _ := os.ReadFile(logPath)
		return err == nil && prev.Size() > maxLogSize && len(b) < 4096 && strings.HasPrefix(string(b), "tick")
	})
}

// After the daemon updates itself in place its units are still its own
// children, so an adopted process that exits must be reaped and seen to
// have exited, not left as a zombie that looks alive.
func TestAdoptedOwnChildIsReaped(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	if err := base.ensure(); err != nil {
		t.Fatal(err)
	}
	sleeper := script(t, dir, "sleeper", `exec sleep 60`)
	if err := base.SaveUnit(Unit{Name: "sleeper", Binary: sleeper}); err != nil {
		t.Fatal(err)
	}
	// Spawned by this process and never waited for: what the previous
	// daemon image leaves behind when it execs.
	cmd := exec.Command(sleeper)
	cmd.SysProcAttr = sysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	status, _ := json.Marshal(Status{Name: "sleeper", PID: pid, State: "running", Since: time.Now()})
	os.WriteFile(filepath.Join(base.run(), "sleeper.json"), status, 0o644)

	d := NewDaemon(base)
	d.tick = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	waitFor(t, "daemon to adopt", func() bool {
		s, _ := base.Statuses()
		return s["sleeper"].PID == pid
	})
	_ = signalGroup(pid, syscall.SIGKILL)
	waitFor(t, "the exit to be noticed and the unit restarted", func() bool {
		s, _ := base.Statuses()
		return s["sleeper"].PID != 0 && s["sleeper"].PID != pid && s["sleeper"].Restarts == 1
	})
}
