package amarit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Updater is the engine behind Run: one instance per process.
type Updater struct {
	cfg    Config
	opts   Options
	client *http.Client
	exe    string   // the running executable, symlinks resolved
	argv   []string // os.Args as the process was started, before Run stripped its flags
	target string   // "darwin/arm64"
	execFn func(bin string, argv, env []string) error
}

// Decision is the outcome of one manifest check.
type Decision struct {
	Manifest *Manifest
	Version  string // the version the channel points at for this target
	Asset    Asset
	Update   bool   // true when Apply should run
	Reason   string // why, in one line, for the log
}

// state is <exe>.amarit/state.json: what the last attempt was and whether
// the running version has proven healthy. It is what makes rollback possible
// without a supervisor.
type state struct {
	Attempt string `json:"attempt,omitempty"` // version an exec was attempted for
	From    string `json:"from,omitempty"`    // version running before the attempt
	Starts  int    `json:"starts,omitempty"`  // starts since the attempt without a healthy mark
	Healthy string `json:"healthy,omitempty"` // last version that reported healthy
}

const (
	maxStartsBeforeRollback = 3
	defaultInterval         = time.Hour
	smokeTimeout            = 15 * time.Second
	drainTimeout            = 30 * time.Second
)

func newUpdater(cfg Config, opts Options, argv []string) (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return &Updater{
		cfg:    cfg,
		opts:   opts,
		client: &http.Client{Timeout: 30 * time.Minute},
		exe:    exe,
		argv:   argv,
		target: runtime.GOOS + "/" + runtime.GOARCH,
		execFn: execInPlace,
	}, nil
}

func (u *Updater) manifestURL() string {
	if u.opts.ManifestURL != "" {
		return u.opts.ManifestURL
	}
	return u.cfg.Releases
}

// sidecar is the one directory amarit keeps beside the binary: state.json,
// the install id, and the previous binary.
func (u *Updater) sidecar() string {
	dir := u.exe + ".amarit"
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func (u *Updater) statePath() string { return filepath.Join(u.sidecar(), "state.json") }
func (u *Updater) prevPath() string  { return filepath.Join(u.sidecar(), "prev") }

func (u *Updater) loadState() state {
	var s state
	if data, err := os.ReadFile(u.statePath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func (u *Updater) saveState(s state) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(u.statePath(), data, 0o644)
}

// Check fetches the manifest and decides whether this process should update.
func (u *Updater) Check(ctx context.Context) (*Decision, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.manifestURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "amarit/"+u.cfg.Project+"/"+u.cfg.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest: %s returned %s", u.manifestURL(), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	m, err := ParseManifest(body)
	if err != nil {
		return nil, err
	}
	if m.Project != u.cfg.Project {
		return nil, fmt.Errorf("manifest is for %q, this is %q", m.Project, u.cfg.Project)
	}
	d := &Decision{Manifest: m}
	version, rel, asset, ok := m.Lookup(u.opts.Channel, u.target)
	if !ok {
		d.Reason = fmt.Sprintf("channel %q has nothing for %s", u.opts.Channel, u.target)
		return d, nil
	}
	d.Version, d.Asset = version, asset
	running := strings.TrimPrefix(u.cfg.Version, "v")

	if u.cfg.Exact {
		if version == running {
			d.Reason = "already on " + running
			return d, nil
		}
		d.Update, d.Reason = true, "channel points at "+version
		return d, nil
	}

	belowFloor := false
	if m.MinVersion != "" {
		if c, err := CompareVersions(running, m.MinVersion); err == nil && c < 0 {
			belowFloor = true
		}
	}
	cmp, err := CompareVersions(running, version)
	if err != nil {
		return nil, fmt.Errorf("version: %w", err)
	}
	switch {
	case cmp == 0:
		d.Reason = "already on " + running
	case cmp > 0 && !u.opts.AllowDowngrade:
		d.Reason = fmt.Sprintf("running %s is newer than %s; not downgrading", running, version)
	case m.Stale(time.Now()) && !belowFloor:
		d.Reason = fmt.Sprintf("manifest expired %s; %s available but not taken from a stale manifest", m.Expires.Format(time.RFC3339), version)
	case !belowFloor && !InRollout(u.installID(), rel.Rollout):
		d.Reason = fmt.Sprintf("%s available; this install is outside the %d%% rollout", version, *rel.Rollout)
	default:
		d.Update = true
		d.Reason = fmt.Sprintf("%s available (running %s)", version, running)
		if belowFloor {
			d.Reason += "; below min_version " + m.MinVersion
		}
	}
	return d, nil
}

// installID is the fixed random id a rollout percentage is keyed on. It
// lives in the sidecar and is created on first use.
func (u *Updater) installID() []byte {
	p := filepath.Join(u.sidecar(), "id")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 16 {
		return b
	}
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	_ = os.WriteFile(p, b, 0o644)
	return b
}

// Apply downloads, verifies, smoke-tests and swaps in the decided version,
// then execs it in place. It returns only on failure; on success the
// process image has been replaced.
func (u *Updater) Apply(ctx context.Context, d *Decision) error {
	if !d.Update {
		return errors.New("nothing to apply")
	}
	tmp, err := u.download(ctx, d.Asset)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	if err := smokeTest(ctx, tmp); err != nil {
		return err
	}

	st := u.loadState()
	st.Attempt, st.From, st.Starts = d.Version, strings.TrimPrefix(u.cfg.Version, "v"), 0
	if err := u.saveState(st); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	prev := u.prevPath()
	_ = os.Remove(prev)
	if err := os.Rename(u.exe, prev); err != nil {
		return fmt.Errorf("swap: %w", err)
	}
	if err := os.Rename(tmp, u.exe); err != nil {
		_ = os.Rename(prev, u.exe)
		return fmt.Errorf("swap: %w", err)
	}

	if u.cfg.Drain != nil {
		dctx, cancel := context.WithTimeout(ctx, drainTimeout)
		u.cfg.Drain(dctx)
		cancel()
	}
	env := os.Environ()
	if u.cfg.Handoff != nil {
		files := u.cfg.Handoff()
		entry, err := prepareHandoff(files)
		if err != nil {
			// The swap already happened; the restart still goes ahead, the
			// new process just starts without the descriptors.
			log.Printf("amarit: %v; restarting without the handoff", err)
		} else if entry != "" {
			env = append(env, entry)
			log.Printf("amarit: handing %d descriptors to the new process", len(files))
		}
	}
	log.Printf("amarit: %s %s -> %s, restarting", u.cfg.Project, st.From, d.Version)
	return u.execFn(u.exe, u.argv, env)
}

// download streams the asset into the executable's own directory, hashing
// as it goes, so the later swap is a rename on the same filesystem.
func (u *Updater) download(ctx context.Context, a Asset) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s returned %s", a.URL, resp.Status)
	}
	f, err := os.CreateTemp(filepath.Dir(u.exe), filepath.Base(u.exe)+".new-*")
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	tmp := f.Name()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, a.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download: %w", err)
	}
	if n != a.Size {
		os.Remove(tmp)
		return "", fmt.Errorf("download: got %d bytes, manifest says %d", n, a.Size)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != a.SHA256 {
		os.Remove(tmp)
		return "", fmt.Errorf("download: sha256 %s does not match manifest %s", sum, a.SHA256)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// smokeTest runs the new binary once so a wrong architecture or a truncated
// file is caught before it replaces anything.
func smokeTest(ctx context.Context, bin string) error {
	sctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	out, err := exec.CommandContext(sctx, bin, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("smoke test: %s -version: %v: %s", filepath.Base(bin), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// reconcile runs once at startup: it settles the previous attempt, marks
// the running version healthy, or rolls back to the previous binary after
// repeated failures to come up. It returns false when auto-update must hold.
func (u *Updater) reconcile() bool {
	st := u.loadState()
	running := strings.TrimPrefix(u.cfg.Version, "v")
	if st.Attempt == "" {
		return true
	}
	if st.Attempt != running {
		// Exec happened but we are not the version we tried to become:
		// hold rather than loop, and leave the evidence on disk.
		log.Printf("amarit: attempted %s but running %s; holding auto-update until the state file is cleared", st.Attempt, running)
		return false
	}
	st.Starts++
	healthy := u.cfg.Healthy == nil || u.cfg.Healthy()
	if healthy {
		log.Printf("amarit: %s is healthy", running)
		_ = u.saveState(state{Healthy: running})
		return true
	}
	if st.Starts >= maxStartsBeforeRollback {
		prev := u.prevPath()
		if _, err := os.Stat(prev); err == nil {
			log.Printf("amarit: %s failed to come up %d times; rolling back to %s", running, st.Starts, st.From)
			bad := filepath.Join(u.sidecar(), "bad")
			_ = os.Remove(bad)
			if err := os.Rename(u.exe, bad); err == nil {
				if err := os.Rename(prev, u.exe); err == nil {
					_ = u.saveState(state{Healthy: st.From})
					if err := u.execFn(u.exe, u.argv, os.Environ()); err == nil {
						return true // only a stubbed exec returns
					}
					log.Printf("amarit: rollback exec failed; %s is back in place", st.From)
					return true
				}
				_ = os.Rename(bad, u.exe)
			}
		}
	}
	_ = u.saveState(st)
	return true
}

// loop checks on an interval with jitter and applies when the check says so.
func (u *Updater) loop(ctx context.Context, interval time.Duration) {
	for {
		var jitter time.Duration
		if interval >= 10 {
			jitter = time.Duration(rand.Int64N(int64(interval / 10)))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval + jitter):
		}
		u.once(ctx)
	}
}

// once is one check-and-apply; it reports whether that went without error.
func (u *Updater) once(ctx context.Context) bool {
	d, err := u.Check(ctx)
	if err != nil {
		log.Printf("amarit: check: %v", err)
		return false
	}
	log.Printf("amarit: %s", d.Reason)
	if d.Update {
		if err := u.Apply(ctx, d); err != nil {
			log.Printf("amarit: apply: %v", err)
			return false
		}
	}
	return true
}
