package amarit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	// StopTimeout is how long a stop waits after SIGTERM before SIGKILL, a
	// duration like "60s"; default 10s. Signals go to the whole process group.
	StopTimeout string `json:"stop_timeout,omitempty"`
	// Limits are resource limits for the process, by name: {"nofile": 65536}.
	Limits map[string]uint64 `json:"limits,omitempty"`
	// Instances runs that many copies, named <name>@1 .. <name>@N, each with
	// AMARIT_INSTANCE=<i> and {instance} expanded in args and env values.
	Instances int `json:"instances,omitempty"`
}

// InstanceNames lists the names the daemon runs this unit under: the unit's
// own name, or <name>@1 .. <name>@N when it has instances.
func (u Unit) InstanceNames() []string {
	var names []string
	for _, c := range u.instances() {
		names = append(names, c.Name)
	}
	return names
}

// instances is what the daemon runs for this unit: the unit itself, or one
// copy per instance named <name>@<i> with {instance} filled in.
func (u Unit) instances() []Unit {
	if u.Instances <= 0 {
		return []Unit{u}
	}
	out := make([]Unit, 0, u.Instances)
	for i := 1; i <= u.Instances; i++ {
		n := strconv.Itoa(i)
		c := u
		c.Name = u.Name + "@" + n
		c.Args = make([]string, len(u.Args))
		for j, a := range u.Args {
			c.Args[j] = strings.ReplaceAll(a, "{instance}", n)
		}
		c.Env = make(map[string]string, len(u.Env))
		for k, v := range u.Env {
			c.Env[k] = strings.ReplaceAll(v, "{instance}", n)
		}
		out = append(out, c)
	}
	return out
}

// splitInstance takes "worker@3" apart; a plain name has index 0.
func splitInstance(name string) (string, int) {
	if base, idx, ok := strings.Cut(name, "@"); ok {
		if i, err := strconv.Atoi(idx); err == nil {
			return base, i
		}
	}
	return name, 0
}

// knownLimits are the names Limits accepts.
var knownLimits = []string{"nofile"}

// The daemon starts a unit that has limits through its own executable as
// `<self> __exec <binary> [args...]`, with the limits in AMARIT_LIMITS.
const (
	execSubcommand = "__exec"
	limitsEnv      = "AMARIT_LIMITS"
)

const defaultStopTimeout = 10 * time.Second

// StopGrace is how long the daemon waits for the unit's process group to
// leave after SIGTERM before it kills what is left.
func (u Unit) StopGrace() time.Duration {
	if d, err := time.ParseDuration(u.StopTimeout); err == nil && d > 0 {
		return d
	}
	return defaultStopTimeout
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

// checkName refuses names that would be read as a path or as an instance
// (web@2), which would collide with another unit's copies.
func checkName(name string) error {
	if strings.ContainsAny(name, "/\\ @") {
		return fmt.Errorf("unit name %q: no slashes, spaces or @ (@ marks an instance, as in web@2)", name)
	}
	return nil
}

// reportedBadUnits holds the unit files already reported, so a bad file is
// announced once per process instead of on every poll.
var reportedBadUnits sync.Map

// SaveUnit writes a unit file; the daemon picks it up within a few seconds.
func (b Base) SaveUnit(u Unit) error {
	if u.Name == "" || u.Binary == "" {
		return errors.New("unit needs a name and a binary")
	}
	if err := checkName(u.Name); err != nil {
		return err
	}
	if u.Instances < 0 {
		return fmt.Errorf("unit %s: instances must not be negative", u.Name)
	}
	if u.StopTimeout != "" {
		if d, err := time.ParseDuration(u.StopTimeout); err != nil || d <= 0 {
			return fmt.Errorf("unit %s: stop_timeout %q must be a duration like 30s", u.Name, u.StopTimeout)
		}
	}
	for name := range u.Limits {
		if !slices.Contains(knownLimits, name) {
			return fmt.Errorf("unit %s: unknown limit %q (known: %s)", u.Name, name, strings.Join(knownLimits, ", "))
		}
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
	if err := os.Remove(filepath.Join(b.units(), name+".json")); err != nil {
		return err
	}
	stopped, _ := filepath.Glob(filepath.Join(b.run(), name+"@*.stopped"))
	for _, p := range stopped {
		_ = os.Remove(p)
	}
	return nil
}

// Enable starts or stops a unit with all its instances, or one instance
// given as <name>@<i>. A unit is stopped in its file; an instance by a
// marker in run/, so the daemon leaves it down until told otherwise.
func (b Base) Enable(name string, on bool) error {
	units, err := b.Units()
	if err != nil {
		return err
	}
	unitName, idx := splitInstance(name)
	for _, u := range units {
		switch {
		case u.Name == name:
			u.Enabled = &on
			if on {
				for _, n := range u.InstanceNames() {
					_ = os.Remove(b.stopMarker(n))
				}
			}
			return b.SaveUnit(u)
		case u.Name == unitName && idx >= 1 && idx <= u.Instances:
			if on {
				if err := os.Remove(b.stopMarker(name)); err != nil && !os.IsNotExist(err) {
					return err
				}
				return nil
			}
			if err := b.ensure(); err != nil {
				return err
			}
			return os.WriteFile(b.stopMarker(name), nil, 0o644)
		}
	}
	return fmt.Errorf("no unit named %s", name)
}

func (b Base) stopMarker(name string) string { return filepath.Join(b.run(), name+".stopped") }

// RequestRestart asks the daemon to restart one instance now. A deliberate
// restart resets the backoff and does not count as a failure.
func (b Base) RequestRestart(name string) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.run(), name+".restart"), nil, 0o644)
}

// takeRestartRequests returns the names restarts were asked for, and
// clears the requests.
func (b Base) takeRestartRequests() []string {
	entries, _ := os.ReadDir(b.run())
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".restart"); ok {
			names = append(names, name)
			_ = os.Remove(filepath.Join(b.run(), e.Name()))
		}
	}
	return names
}

// Stopped reports whether one instance was stopped by name.
func (b Base) Stopped(name string) bool {
	_, err := os.Stat(b.stopMarker(name))
	return err == nil
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
		// SaveUnit refuses these names; a hand-written file must not slip past.
		if err := checkName(u.Name); err != nil {
			path := filepath.Join(b.units(), e.Name())
			if _, seen := reportedBadUnits.LoadOrStore(path, true); !seen {
				log.Printf("amarit: WARNING: IGNORING unit file %s, it will not run: %v. Rename the file and its \"name\" field.", path, err)
			}
			continue
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

// child is one supervised process; its name is the daemon's key everywhere.
type child struct {
	unit     Unit
	pid      int // 0 while it is down
	started  time.Time
	restarts int
	backoff  time.Duration
	next     time.Time // earliest next start after an exit
	lastExit string
	done     chan error
	parked   bool // exited and its policy says not to restart; waits for the unit file to change
}

// Daemon keeps every unit in its base running. One per base.
type Daemon struct {
	base     Base
	children map[string]*child
	tick     time.Duration
	self     string     // this executable, which plays the exec helper for units with limits
	mu       sync.Mutex // held while reconcile runs; Drain takes it so a restart lands between passes
	// Version is what the daemon logs about itself at start; the program sets it.
	Version string
}

// NewDaemon prepares a daemon over base.
func NewDaemon(base Base) *Daemon {
	self, _ := os.Executable()
	return &Daemon{base: base, children: map[string]*child{}, tick: 2 * time.Second, self: self}
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
	log.Printf("amarit%s: daemon %d supervising %s", d.versionTag(), os.Getpid(), d.base)
	d.adopt()
	for {
		d.mu.Lock()
		d.reconcile()
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			log.Printf("amarit: daemon stopping; units keep running")
			return nil
		case <-time.After(d.tick):
		}
	}
}

func (d *Daemon) versionTag() string {
	if d.Version == "" {
		return ""
	}
	return " " + d.Version
}

// Preflight is the daemon's readiness check for amarit.Healthy: a daemon
// that can read its units and write its run state is one that can take
// over from the version before it.
func (d *Daemon) Preflight() bool {
	if err := d.base.ensure(); err != nil {
		return false
	}
	_, err := d.base.Units()
	return err == nil
}

// Drain is the daemon's hook for amarit.Drain: it waits for the current
// reconcile pass to finish and holds the next one back, so the exec that
// follows finds no stop half done. The exec replaces this image, so the lock
// is never released in that path; should the exec fail, it is released once
// the drain deadline has passed, and the daemon carries on.
func (d *Daemon) Drain(ctx context.Context) {
	d.mu.Lock()
	go func() {
		<-ctx.Done()
		time.Sleep(drainTimeout)
		d.mu.Unlock()
	}()
}

// adopt picks up processes that a previous daemon started and left alive.
func (d *Daemon) adopt() {
	statuses, _ := d.base.Statuses()
	for name, s := range statuses {
		if s.PID != 0 && processAlive(s.PID) {
			d.children[name] = &child{unit: Unit{Name: name}, pid: s.PID, started: s.Since, restarts: s.Restarts, done: watchPID(s.PID)}
			log.Printf("amarit: adopted %s (pid %d)", name, s.PID)
		}
	}
}

// instanceLess orders unit names with worker@2 before worker@10.
func instanceLess(a, b string) bool {
	an, ai := splitInstance(a)
	bn, bi := splitInstance(b)
	if an != bn {
		return an < bn
	}
	return ai < bi
}

func (d *Daemon) reconcile() {
	units, err := d.base.Units()
	if err != nil {
		log.Printf("amarit: units: %v", err)
		return
	}
	want := map[string]Unit{}
	for _, u := range units {
		if !u.enabled() {
			continue
		}
		for _, c := range u.instances() {
			if !d.base.Stopped(c.Name) {
				want[c.Name] = c
			}
		}
	}
	// Stop what is no longer wanted, the highest instance numbers first.
	var gone []*child
	for name, c := range d.children {
		if _, ok := want[name]; !ok {
			gone = append(gone, c)
			delete(d.children, name)
			_ = os.Remove(filepath.Join(d.base.run(), name+".json"))
		}
	}
	sort.Slice(gone, func(i, j int) bool { return instanceLess(gone[j].unit.Name, gone[i].unit.Name) })
	stopAll(gone)
	// `amarit restart`: stop those together; the loop below starts them
	// fresh, as the operator asked, with no backoff and no failure recorded.
	var again []*child
	for _, name := range d.base.takeRestartRequests() {
		if c, ok := d.children[name]; ok {
			log.Printf("amarit: %s: restart requested", name)
			again = append(again, c)
		}
	}
	sort.Slice(again, func(i, j int) bool { return instanceLess(again[i].unit.Name, again[j].unit.Name) })
	stopAll(again)
	for _, c := range again {
		c.backoff, c.next, c.lastExit, c.parked = time.Second, time.Time{}, "", false
	}
	// Start, restart, or notice exits.
	for name, u := range want {
		c, ok := d.children[name]
		if !ok {
			c = &child{backoff: time.Second}
			d.children[name] = c
		}
		if c.parked {
			// A changed unit file is the operator's word to try again.
			if reflect.DeepEqual(c.unit, u) {
				continue
			}
			c.parked = false
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
	for _, c := range d.children {
		if c.pid != 0 {
			rotateLog(filepath.Join(d.base.logs(), c.unit.Name+".log"))
		}
		d.writeStatus(c)
	}
}

func (d *Daemon) start(c *child) {
	u := c.unit
	env := os.Environ()
	if u.EnvFile != "" {
		if extra, err := readEnvFile(u.EnvFile); err == nil {
			env = append(env, extra...)
		} else {
			log.Printf("amarit: %s: env file: %v", u.Name, err)
		}
	}
	for k, v := range u.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, "AMARIT_MANAGED=1", "AMARIT_UNIT="+u.Name)
	if _, i := splitInstance(u.Name); i > 0 {
		env = append(env, "AMARIT_INSTANCE="+strconv.Itoa(i))
	}
	cmd := exec.Command(u.Binary, u.Args...)
	if len(u.Limits) > 0 && d.self != "" {
		// Limits can only be set from inside the process, so the unit is
		// started through amarit itself, which sets them and execs the binary.
		cmd = exec.Command(d.self, append([]string{execSubcommand, u.Binary}, u.Args...)...)
		var limits []string
		for _, name := range slices.Sorted(maps.Keys(u.Limits)) {
			limits = append(limits, name+"="+strconv.FormatUint(u.Limits[name], 10))
		}
		env = append(env, limitsEnv+"="+strings.Join(limits, ","))
	}
	cmd.Dir, cmd.Env, cmd.SysProcAttr = u.Dir, env, sysProcAttr()
	// The program gets its own descriptor to the log; the daemon keeps none.
	f, err := os.OpenFile(filepath.Join(d.base.logs(), u.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("amarit: %s: log: %v", u.Name, err)
		return
	}
	defer f.Close()
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		c.lastExit = err.Error()
		c.next = time.Now().Add(c.backoff)
		c.backoff = min(c.backoff*2, time.Minute)
		log.Printf("amarit: %s: start: %v (retry in %s)", u.Name, err, c.backoff)
		return
	}
	c.pid, c.started = cmd.Process.Pid, time.Now()
	c.done = make(chan error, 1)
	go func(done chan error) { done <- cmd.Wait() }(c.done)
	log.Printf("amarit: %s: started pid %d", u.Name, c.pid)
}

func (d *Daemon) exited(c *child, err error) {
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
	pid := c.pid
	c.pid = 0
	if groupAlive(pid) {
		// Whatever the program left behind still holds its memory and ports.
		log.Printf("amarit: %s: pid %d left processes behind; stopping them", c.unit.Name, pid)
		_ = signalGroup(pid, syscall.SIGTERM)
		go killAfterGrace(c.unit.Name, pid, c.unit.StopGrace())
	}
	if !c.unit.wantsRestart(code, err) {
		c.parked = true
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

// stopAll stops running children together: every process group is told to
// stop first, in the order given, then each is waited for, so eight workers
// with a 60 s grace take 60 s, not eight minutes. A child was started in its
// own session, so its pid is the group id and its own children go with it.
func stopAll(cs []*child) {
	var wg sync.WaitGroup
	for _, c := range cs {
		if c.pid == 0 {
			continue
		}
		log.Printf("amarit: %s: stopping pid %d", c.unit.Name, c.pid)
		_ = signalGroup(c.pid, syscall.SIGTERM)
		wg.Add(1)
		go func() {
			defer wg.Done()
			killAfterGrace(c.unit.Name, c.pid, c.unit.StopGrace())
			<-c.done
			c.pid = 0
		}()
	}
	wg.Wait()
}

// killAfterGrace gives a group that was sent SIGTERM the grace to leave,
// then kills what is left.
func killAfterGrace(name string, pid int, grace time.Duration) {
	deadline := time.Now().Add(grace)
	for groupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if groupAlive(pid) {
		log.Printf("amarit: %s: still running %s after SIGTERM; killing", name, grace)
		_ = signalGroup(pid, syscall.SIGKILL)
	}
}

func (d *Daemon) writeStatus(c *child) {
	s := Status{Name: c.unit.Name, PID: c.pid, Since: c.started, Restarts: c.restarts, LastExit: c.lastExit, NextStart: c.next}
	switch {
	case c.pid != 0:
		s.State = "running"
	case c.parked:
		s.State = "exited"
	default:
		s.State = "backoff"
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(filepath.Join(d.base.run(), c.unit.Name+".json"), data, 0o644)
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

// maxLogSize is where a unit's log is rotated, keeping one previous file.
const maxLogSize = 10 << 20

// rotateLog copies a log that has passed maxLogSize to <log>.1 and empties
// it in place. The program keeps its descriptor, opened with O_APPEND, so
// its next line lands at the start of the emptied file; a line written
// during the copy can be lost, which is the price of not restarting it.
func rotateLog(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= maxLogSize {
		return
	}
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()
	dst, err := os.Create(path + ".1")
	if err != nil {
		return
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		_ = os.Truncate(path, 0)
	}
}
