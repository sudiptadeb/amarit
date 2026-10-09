package upkeep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary is a script that answers -version, which is all the smoke test
// asks of it.
func fakeBinary(t *testing.T, dir, name, version string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func serve(t *testing.T, manifest func(assetURL string) string, asset []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/upkeep.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, manifest(srv.URL+"/asset"))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write(asset)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func manifestFor(version, running string, extra string) func(string) string {
	return func(assetURL string) string {
		return fmt.Sprintf(`{"spec":1,"project":"demo","published":"2026-10-09T00:00:00Z",%s
		  "channels":{"stable":{"version":"%s"}},
		  "assets":{"%s":{"%s/%s":{"url":"%s","sha256":"%s","size":%d}}}}`,
			extra, version, version, runtime.GOOS, runtime.GOARCH, assetURL, assetSHA, assetSize)
	}
}

var (
	assetBody = []byte("#!/bin/sh\necho v0.6.0\n")
	assetSHA  = func() string { h := sha256.Sum256(assetBody); return hex.EncodeToString(h[:]) }()
	assetSize = int64(len(assetBody))
)

func testUpdater(t *testing.T, srv *httptest.Server, exe, running string) *Updater {
	t.Helper()
	u := &Updater{
		cfg:    Config{Project: "demo", Version: running},
		opts:   Options{Channel: "stable"},
		client: http.DefaultClient,
		exe:    exe,
		argv:   []string{exe, "-serve"},
		target: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if srv != nil {
		u.cfg.Manifest = srv.URL + "/upkeep.json"
		u.client = srv.Client()
	}
	return u
}

func TestCheckDecisions(t *testing.T) {
	dir := t.TempDir()
	exe := fakeBinary(t, dir, "demo", "v0.5.0")
	cases := []struct {
		name, running, extra string
		opts                 Options
		update               bool
		reason               string
	}{
		{"newer available", "0.5.0", "", Options{Channel: "stable"}, true, "0.6.0 available"},
		{"leading v tolerated", "v0.5.0", "", Options{Channel: "stable"}, true, "0.6.0 available"},
		{"already current", "0.6.0", "", Options{Channel: "stable"}, false, "already on"},
		{"no downgrade", "0.7.0", "", Options{Channel: "stable"}, false, "not downgrading"},
		{"downgrade allowed", "0.7.0", "", Options{Channel: "stable", AllowDowngrade: true}, true, "available"},
		{"unknown channel", "0.5.0", "", Options{Channel: "beta"}, false, "nothing for"},
		{"stale manifest", "0.5.0", `"expires":"2026-01-01T00:00:00Z",`, Options{Channel: "stable"}, false, "expired"},
		{"stale but below floor", "0.5.0", `"expires":"2026-01-01T00:00:00Z","min_version":"0.5.5",`, Options{Channel: "stable"}, true, "below min_version"},
	}
	for _, c := range cases {
		srv := serve(t, manifestFor("0.6.0", c.running, c.extra), assetBody)
		u := testUpdater(t, srv, exe, c.running)
		u.opts = c.opts
		d, err := u.Check(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d.Update != c.update || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: update=%v reason=%q", c.name, d.Update, d.Reason)
		}
	}
}

func TestCheckRefusesOtherProject(t *testing.T) {
	srv := serve(t, func(a string) string {
		return `{"spec":1,"project":"other","published":"2026-10-09T00:00:00Z","channels":{"stable":{"version":"1.0.0"}},"assets":{"1.0.0":{}}}`
	}, nil)
	u := testUpdater(t, srv, fakeBinary(t, t.TempDir(), "demo", "v1"), "0.1.0")
	if _, err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), `for "other"`) {
		t.Fatalf("want a project mismatch error, got %v", err)
	}
}

func TestApplySwapsAndExecs(t *testing.T) {
	dir := t.TempDir()
	exe := fakeBinary(t, dir, "demo", "v0.5.0")
	srv := serve(t, manifestFor("0.6.0", "0.5.0", ""), assetBody)
	u := testUpdater(t, srv, exe, "0.5.0")

	var execd []string
	u.execFn = func(bin string, argv, env []string) error {
		execd = append([]string{bin}, argv...)
		return nil
	}
	d, err := u.Check(context.Background())
	if err != nil || !d.Update {
		t.Fatalf("check: %v %+v", err, d)
	}
	if err := u.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if len(execd) != 3 || execd[0] != exe || execd[2] != "-serve" {
		t.Fatalf("exec'd %v", execd)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(assetBody) {
		t.Fatal("the new binary is not in place")
	}
	if got, _ := os.ReadFile(exe + ".prev"); !strings.Contains(string(got), "v0.5.0") {
		t.Fatal(".prev does not hold the old binary")
	}
	st := u.loadState()
	if st.Attempt != "0.6.0" || st.From != "0.5.0" {
		t.Fatalf("state %+v", st)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "demo.new-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestApplyRefusesBadDigestAndKeepsBinary(t *testing.T) {
	dir := t.TempDir()
	exe := fakeBinary(t, dir, "demo", "v0.5.0")
	tampered := []byte("#!/bin/sh\necho evil\n")
	srv := serve(t, manifestFor("0.6.0", "0.5.0", ""), tampered) // manifest hash is for assetBody
	u := testUpdater(t, srv, exe, "0.5.0")
	u.execFn = func(string, []string, []string) error { t.Fatal("must not exec"); return nil }
	d, _ := u.Check(context.Background())
	// size differs too; make the manifest size match so only the hash fails
	d.Asset.Size = int64(len(tampered))
	err := u.Apply(context.Background(), d)
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("want a digest error, got %v", err)
	}
	if got, _ := os.ReadFile(exe); !strings.Contains(string(got), "v0.5.0") {
		t.Fatal("running binary was touched")
	}
	if _, err := os.Stat(exe + ".prev"); err == nil {
		t.Fatal("no .prev should exist after a refused download")
	}
}

func TestApplyRefusesBinaryThatFailsSmokeTest(t *testing.T) {
	dir := t.TempDir()
	exe := fakeBinary(t, dir, "demo", "v0.5.0")
	broken := []byte("#!/bin/sh\nexit 3\n")
	h := sha256.Sum256(broken)
	srv := serve(t, func(a string) string {
		return fmt.Sprintf(`{"spec":1,"project":"demo","published":"2026-10-09T00:00:00Z","channels":{"stable":{"version":"0.6.0"}},"assets":{"0.6.0":{"%s/%s":{"url":"%s","sha256":"%s","size":%d}}}}`,
			runtime.GOOS, runtime.GOARCH, a, hex.EncodeToString(h[:]), len(broken))
	}, broken)
	u := testUpdater(t, srv, exe, "0.5.0")
	u.execFn = func(string, []string, []string) error { t.Fatal("must not exec"); return nil }
	d, _ := u.Check(context.Background())
	if err := u.Apply(context.Background(), d); err == nil || !strings.Contains(err.Error(), "smoke test") {
		t.Fatalf("want a smoke test error, got %v", err)
	}
	if got, _ := os.ReadFile(exe); !strings.Contains(string(got), "v0.5.0") {
		t.Fatal("running binary was touched")
	}
}

func TestReconcileHealthyThenRollback(t *testing.T) {
	dir := t.TempDir()
	exe := fakeBinary(t, dir, "demo", "v0.6.0")
	fakeBinary(t, dir, "demo.prev", "v0.5.0")
	u := testUpdater(t, nil, exe, "0.6.0")

	// A fresh start after an attempt for this very version: healthy.
	u.saveState(state{Attempt: "0.6.0", From: "0.5.0"})
	if !u.reconcile() || u.loadState().Healthy != "0.6.0" || u.loadState().Attempt != "" {
		t.Fatalf("state after healthy start: %+v", u.loadState())
	}

	// Three starts that never report healthy: roll back to .prev and exec.
	u.cfg.Healthy = func() bool { return false }
	var execd string
	u.execFn = func(bin string, argv, env []string) error { execd = bin; return nil }
	u.saveState(state{Attempt: "0.6.0", From: "0.5.0", Starts: 2})
	u.reconcile()
	if execd != exe {
		t.Fatal("rollback did not exec the restored binary")
	}
	if got, _ := os.ReadFile(exe); !strings.Contains(string(got), "v0.5.0") {
		t.Fatal(".prev was not restored")
	}
	if st := u.loadState(); st.Healthy != "0.5.0" || st.Attempt != "" {
		t.Fatalf("state after rollback: %+v", st)
	}

	// Exec'd for 0.7.0 but still running 0.6.0: hold.
	u.saveState(state{Attempt: "0.7.0", From: "0.6.0"})
	if u.reconcile() {
		t.Fatal("a version that never took must hold auto-update")
	}
}
