package amarit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Unit is one program the daemon keeps running: <base>/units/<name>.json.
type Unit struct {
	Name          string            `json:"name"`
	Binary        string            `json:"binary"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	EnvFile       string            `json:"env_file,omitempty"`
	Dir           string            `json:"dir,omitempty"`
	Restart       string            `json:"restart,omitempty"` // always (default), on-failure, never
	NoRestartExit []int             `json:"no_restart_exit,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"` // nil means true
}

// Status is what the daemon writes for each unit: <base>/run/<name>.json.
type Status struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid,omitempty"`
	State     string    `json:"state"` // running, stopped, backoff, exited
	Since     time.Time `json:"since"`
	Restarts  int       `json:"restarts"`
	LastExit  string    `json:"last_exit,omitempty"`
	NextStart time.Time `json:"next_start,omitempty"`
}

// Base is the daemon's directory: units, run state and logs.
type Base string

// DefaultBase is ~/.amarit, or $AMARIT_HOME when set.
func DefaultBase() Base {
	if p := os.Getenv("AMARIT_HOME"); p != "" {
		return Base(p)
	}
	home, _ := os.UserHomeDir()
	return Base(filepath.Join(home, ".amarit"))
}

func (b Base) units() string   { return filepath.Join(string(b), "units") }
func (b Base) run() string     { return filepath.Join(string(b), "run") }
func (b Base) logs() string    { return filepath.Join(string(b), "logs") }
func (b Base) pidFile() string { return filepath.Join(string(b), "daemon.pid") }

func (b Base) ensure() error {
	for _, d := range []string{b.units(), b.run(), b.logs()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// SaveUnit writes a unit file; the daemon picks it up within a few seconds.
func (b Base) SaveUnit(u Unit) error {
	if u.Name == "" || u.Binary == "" {
		return errors.New("unit needs a name and a binary")
	}
	if strings.ContainsAny(u.Name, "/\\ ") {
		return fmt.Errorf("unit name %q: no slashes or spaces", u.Name)
	}
	if err := b.ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(b.units(), "."+u.Name+".json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(b.units(), u.Name+".json"))
}

// RemoveUnit deletes a unit file; the daemon stops the process.
func (b Base) RemoveUnit(name string) error {
	return os.Remove(filepath.Join(b.units(), name+".json"))
}

// Units lists the unit files, sorted by name.
func (b Base) Units() ([]Unit, error) {
	entries, err := os.ReadDir(b.units())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var units []Unit
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.units(), e.Name()))
		if err != nil {
			continue
		}
		var u Unit
		if err := json.Unmarshal(data, &u); err != nil {
			log.Printf("amarit: unit %s: %v", e.Name(), err)
			continue
		}
		if u.Name == "" {
			u.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		units = append(units, u)
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Name < units[j].Name })
	return units, nil
}

// Statuses reads what the daemon last wrote for every unit.
func (b Base) Statuses() (map[string]Status, error) {
	out := map[string]Status{}
	entries, err := os.ReadDir(b.run())
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.run(), e.Name()))
		if err != nil {
			continue
		}
		var s Status
		if json.Unmarshal(data, &s) == nil {
			out[s.Name] = s
		}
	}
	return out, nil
}

// DaemonPID returns the running daemon's pid, or 0.
func (b Base) DaemonPID() int {
	data, err := os.ReadFile(b.pidFile())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid <= 0 || !processAlive(pid) {
		return 0
	}
	return pid
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func (u Unit) enabled() bool { return u.Enabled == nil || *u.Enabled }

func (u Unit) wantsRestart(exitCode int, err error) bool {
	for _, c := range u.NoRestartExit {
		if c == exitCode {
			return false
		}
	}
	switch u.Restart {
	case "never":
		return false
	case "on-failure":
		return err != nil
	default:
		return true
	}
}

// child is one supervised process.
type child struct {
	unit     Unit
	cmd      *exec.Cmd
	pid      int
	started  time.Time
	restarts int
	backoff  time.Duration
	next     time.Time // earliest next start after an exit
	lastExit string
	done     chan error
	log      *os.File
}

// Daemon keeps every unit in its base running. One per base.
type Daemon struct {
	base     Base
	children map[string]*child
	tick     time.Duration
}

// NewDaemon prepares a daemon over base.
func NewDaemon(base Base) *Daemon {
	return &Daemon{base: base, children: map[string]*child{}, tick: 2 * time.Second}
}

// Run supervises until ctx is cancelled. It adopts processes a previous
// daemon left running, so restarting the daemon does not restart its units.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.base.ensure(); err != nil {
		return err
	}
	if pid := d.base.DaemonPID(); pid != 0 && pid != os.Getpid() {
		return fmt.Errorf("daemon already running (pid %d)", pid)
	}
	if err := os.WriteFile(d.base.pidFile(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	defer os.Remove(d.base.pidFile())
	log.Printf("amarit: daemon %d supervising %s", os.Getpid(), d.base)
	d.adopt()
	for {
		d.reconcile()
		select {
		case <-ctx.Done():
			log.Printf("amarit: daemon stopping; units keep running")
			return nil
		case <-time.After(d.tick):
		}
	}
}

// adopt picks up processes that a previous daemon started and left alive.
func (d *Daemon) adopt() {
	statuses, _ := d.base.Statuses()
	for name, s := range statuses {
		if s.PID != 0 && processAlive(s.PID) {
			d.children[name] = &child{pid: s.PID, started: s.Since, restarts: s.Restarts, done: watchPID(s.PID)}
			log.Printf("amarit: adopted %s (pid %d)", name, s.PID)
		}
	}
}

// watchPID polls a process amarit did not spawn, since it cannot wait() on it.
func watchPID(pid int) chan error {
	ch := make(chan error, 1)
	go func() {
		for processAlive(pid) {
			time.Sleep(time.Second)
		}
		ch <- errors.New("exited (adopted process; exit status unknown)")
	}()
	return ch
}

func (d *Daemon) reconcile() {
	units, err := d.base.Units()
	if err != nil {
		log.Printf("amarit: units: %v", err)
		return
	}
	want := map[string]Unit{}
	for _, u := range units {
		if u.enabled() {
			want[u.Name] = u
		}
	}
	// Stop what is no longer wanted.
	for name, c := range d.children {
		if _, ok := want[name]; !ok {
			d.stop(name, c)
		}
	}
	// Start, restart, or notice exits.
	for name, u := range want {
		c, ok := d.children[name]
		if !ok {
			c = &child{unit: u, backoff: time.Second}
			d.children[name] = c
			d.start(c)
			continue
		}
		c.unit = u
		if c.pid == 0 {
			if time.Now().After(c.next) {
				d.start(c)
			}
			continue
		}
		select {
		case err := <-c.done:
			d.exited(c, err)
		default:
		}
	}
	for name, c := range d.children {
		d.writeStatus(name, c)
	}
}

func (d *Daemon) start(c *child) {
	u := c.unit
	cmd := exec.Command(u.Binary, u.Args...)
	cmd.Dir = u.Dir
	cmd.Env = os.Environ()
	if u.EnvFile != "" {
		if extra, err := readEnvFile(u.EnvFile); err == nil {
			cmd.Env = append(cmd.Env, extra...)
		} else {
			log.Printf("amarit: %s: env file: %v", u.Name, err)
		}
	}
	for k, v := range u.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "AMARIT_MANAGED=1", "AMARIT_UNIT="+u.Name)
	logPath := filepath.Join(d.base.logs(), u.Name+".log")
	rotateLog(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("amarit: %s: log: %v", u.Name, err)
		return
	}
	cmd.Stdout, cmd.Stderr = f, f
	cmd.Stdin = nil
	cmd.SysProcAttr = sysProcAttr()
	if err := cmd.Start(); err != nil {
		f.Close()
		c.lastExit = err.Error()
		c.next = time.Now().Add(c.backoff)
		c.backoff = min(c.backoff*2, time.Minute)
		log.Printf("amarit: %s: start: %v (retry in %s)", u.Name, err, c.backoff)
		return
	}
	c.cmd, c.pid, c.started, c.log = cmd, cmd.Process.Pid, time.Now(), f
	c.done = make(chan error, 1)
	go func(cmd *exec.Cmd, done chan error) { done <- cmd.Wait() }(cmd, c.done)
	log.Printf("amarit: %s: started pid %d", u.Name, c.pid)
}

func (d *Daemon) exited(c *child, err error) {
	if c.log != nil {
		c.log.Close()
		c.log = nil
	}
	code := 0
	if err != nil {
		c.lastExit = err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	} else {
		c.lastExit = "exit 0"
	}
	uptime := time.Since(c.started)
	c.pid, c.cmd = 0, nil
	if !c.unit.wantsRestart(code, err) {
		c.next = time.Time{}
		c.unit.Enabled = new(bool) // parked until the unit file changes
		log.Printf("amarit: %s: %s; not restarting (policy)", c.unit.Name, c.lastExit)
		return
	}
	if uptime > 5*time.Minute {
		c.backoff = time.Second
	}
	c.restarts++
	c.next = time.Now().Add(c.backoff)
	log.Printf("amarit: %s: %s after %s; restarting in %s", c.unit.Name, c.lastExit, uptime.Round(time.Second), c.backoff)
	c.backoff = min(c.backoff*2, time.Minute)
}

func (d *Daemon) stop(name string, c *child) {
	if c.pid != 0 {
		log.Printf("amarit: %s: stopping pid %d", name, c.pid)
		if p, err := os.FindProcess(c.pid); err == nil {
			_ = p.Signal(syscall.SIGTERM)
			select {
			case <-c.done:
			case <-time.After(10 * time.Second):
				_ = p.Kill()
				<-c.done
			}
		}
		if c.log != nil {
			c.log.Close()
		}
	}
	delete(d.children, name)
	_ = os.Remove(filepath.Join(d.base.run(), name+".json"))
}

func (d *Daemon) writeStatus(name string, c *child) {
	s := Status{Name: name, PID: c.pid, Since: c.started, Restarts: c.restarts, LastExit: c.lastExit, NextStart: c.next}
	switch {
	case c.pid != 0:
		s.State = "running"
	case !c.unit.enabled():
		s.State = "exited"
	default:
		s.State = "backoff"
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(filepath.Join(d.base.run(), name+".json"), data, 0o644)
}

func readEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		if k, v, ok := strings.Cut(line, "="); ok {
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			out = append(out, strings.TrimSpace(k)+"="+v)
		}
	}
	return out, sc.Err()
}

// rotateLog keeps one previous log once the current one passes 10 MB.
func rotateLog(path string) {
	if st, err := os.Stat(path); err == nil && st.Size() > 10<<20 {
		_ = os.Rename(path, path+".1")
	}
}
