package amarit

import (
	"encoding/base64"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

const goodConfig = `{
  "project": "termulaa",
  "releases": "https://github.com/sudiptadeb/termulaa/releases/latest/download/releases.json",
  "restart": "overlap",
  "interval": "5m",
  "auto_update": true,
  "service": { "instances": 1, "restart": "on-failure", "no_restart_exit": [4, 5, 7], "health": "http://127.0.0.1:17380/health" },
  "release": { "targets": ["darwin/arm64", "linux/amd64"] }
}`

func TestParseProjectConfig(t *testing.T) {
	c, err := ParseProjectConfig([]byte(goodConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "termulaa" || c.Restart != "overlap" || c.Service.NoRestartExit[2] != 7 || len(c.Release.Targets) != 2 {
		t.Fatalf("parsed %+v", c)
	}
	for name, body := range map[string]string{
		"no project":   `{"releases":"https://m"}`,
		"no releases":  `{"project":"x"}`,
		"bad restart":  `{"project":"x","releases":"https://m","restart":"fork"}`,
		"bad interval": `{"project":"x","releases":"https://m","interval":"soon"}`,
	} {
		if _, err := ParseProjectConfig([]byte(body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// quiet clears the environment and the build facts for one test, so only
// the layer under test speaks.
func quiet(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AMARIT_AUTO_UPDATE", "AMARIT_MANAGED", "AMARIT_UPDATE_INTERVAL"} {
		t.Setenv(k, "")
	}
	stampedProject, stampedVersion = "", ""
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = debug.ReadBuildInfo })
}

func TestResolveStamp(t *testing.T) {
	quiet(t)
	c, err := resolve(nil, Options{})
	if err != nil || c.Releases != "" || c.Version != "" {
		t.Fatalf("unstamped build: %+v %v", c, err)
	}

	stampedProject = base64.StdEncoding.EncodeToString([]byte(goodConfig))
	stampedVersion = "v0.5.3"
	c, err = resolve(nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "termulaa" || c.Version != "0.5.3" || c.Restart != Overlap || c.Interval != 5*time.Minute || !c.AutoUpdate || !strings.HasSuffix(c.Releases, "releases.json") {
		t.Fatalf("stamped config = %+v", c)
	}
	stampedProject = "not base64!"
	if _, err := resolve(nil, Options{}); err == nil {
		t.Fatal("a corrupt stamp must be an error, not silence")
	}
}

// A program that carries its amarit.json itself (go:embed) hands it over
// with ProjectFile and gets the same Config a stamp would give.
func TestResolveProjectFile(t *testing.T) {
	quiet(t)
	c, err := resolve([]Option{ProjectFile([]byte(goodConfig))}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "termulaa" || c.Restart != Overlap || c.Interval != 5*time.Minute || !c.AutoUpdate {
		t.Fatalf("embedded config = %+v", c)
	}
	c, err = resolve([]Option{ProjectFile([]byte(`{"project":"x"}`))}, Options{})
	if err == nil || c.Project != "" {
		t.Fatalf("an invalid file must be reported and change nothing: %+v %v", c, err)
	}
}

// With no stamp and no option, the version is the module version Go
// recorded in the binary: a `go install` build knows what it is.
func TestResolveModuleVersion(t *testing.T) {
	quiet(t)
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v0.5.2"}}, true
	}
	c, _ := resolve(nil, Options{})
	if c.Version != "0.5.2" {
		t.Fatalf("module version: %q", c.Version)
	}
	stampedVersion = "0.5.3"
	if c, _ := resolve(nil, Options{}); c.Version != "0.5.3" {
		t.Fatalf("the stamp must win over the module: %q", c.Version)
	}
	if c, _ := resolve([]Option{Version("9.0.0")}, Options{}); c.Version != "9.0.0" {
		t.Fatalf("code must win over the stamp: %q", c.Version)
	}
}

// Options in code lay over the stamped file, and a program without any
// amarit.json configures itself through them alone.
func TestResolveOptionsOverStamp(t *testing.T) {
	quiet(t)
	stampedProject = base64.StdEncoding.EncodeToString([]byte(goodConfig))
	c, _ := resolve([]Option{Releases("https://mirror/releases.json"), Interval(time.Minute)}, Options{})
	if c.Project != "termulaa" || c.Releases != "https://mirror/releases.json" || c.Interval != time.Minute {
		t.Fatalf("options over stamp = %+v", c)
	}
	stampedProject = ""
	c, _ = resolve([]Option{Project("app"), Releases("https://m"), Version("1.0.0"), AutoUpdate(true)}, Options{})
	if c.Project != "app" || c.Releases != "https://m" || c.Version != "1.0.0" || !c.AutoUpdate {
		t.Fatalf("hardcoded options = %+v", c)
	}
}

func TestResolveIntervalPrecedence(t *testing.T) {
	quiet(t)
	if c, _ := resolve(nil, Options{}); c.Interval != time.Hour {
		t.Fatalf("default = %v", c.Interval)
	}
	in := []Option{Interval(5 * time.Minute)}
	if c, _ := resolve(in, Options{}); c.Interval != 5*time.Minute {
		t.Fatalf("config = %v", c.Interval)
	}
	t.Setenv("AMARIT_UPDATE_INTERVAL", "2m")
	if c, _ := resolve(in, Options{}); c.Interval != 2*time.Minute {
		t.Fatalf("env over config = %v", c.Interval)
	}
	if c, _ := resolve(in, Options{Interval: 30 * time.Second}); c.Interval != 30*time.Second {
		t.Fatalf("flag over env = %v", c.Interval)
	}
}

func TestResolveAutoUpdatePrecedence(t *testing.T) {
	quiet(t)
	on := []Option{AutoUpdate(true)}
	if c, _ := resolve(nil, Options{}); c.AutoUpdate {
		t.Fatal("nothing said: must be off")
	}
	if c, _ := resolve(on, Options{}); !c.AutoUpdate {
		t.Fatal("the project's default must turn it on")
	}
	if c, _ := resolve(nil, Options{AutoUpdate: true}); !c.AutoUpdate {
		t.Fatal("--auto-update must turn it on")
	}
	t.Setenv("AMARIT_MANAGED", "1")
	if c, _ := resolve(nil, Options{}); !c.AutoUpdate {
		t.Fatal("a unit under the daemon must be on")
	}
	t.Setenv("AMARIT_AUTO_UPDATE", "0")
	if c, _ := resolve(on, Options{}); c.AutoUpdate {
		t.Fatal("AMARIT_AUTO_UPDATE=0 must beat the project's default and the daemon")
	}
	if c, _ := resolve(nil, Options{AutoUpdate: true}); !c.AutoUpdate {
		t.Fatal("--auto-update must beat the environment")
	}
	t.Setenv("AMARIT_AUTO_UPDATE", "1")
	if c, _ := resolve(on, Options{NoAutoUpdate: true}); c.AutoUpdate {
		t.Fatal("--no-auto-update must beat everything")
	}
}

// --update-url and --update-key are command-line layers over the file.
func TestResolveCommandLineOverFile(t *testing.T) {
	quiet(t)
	c, _ := resolve([]Option{ProjectFile([]byte(goodConfig)), Key("author")}, Options{ManifestURL: "https://m", Key: "K"})
	if c.Releases != "https://m" || c.Key != "K" {
		t.Fatalf("command line over file = %+v", c)
	}
}
