package amarit

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

const goodConfig = `{
  "project": "termulaa",
  "releases": "https://github.com/sudiptadeb/termulaa/releases/latest/download/releases.json",
  "restart": "overlap",
  "interval": "5m",
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

func TestStampedConfig(t *testing.T) {
	stampedProject, stampedVersion = "", ""
	c, err := stampedConfig()
	if err != nil || c.Releases != "" {
		t.Fatalf("unstamped build: %+v %v", c, err)
	}

	stampedProject = base64.StdEncoding.EncodeToString([]byte(goodConfig))
	stampedVersion = "v0.5.3"
	defer func() { stampedProject, stampedVersion = "", "" }()
	c, err = stampedConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "termulaa" || c.Version != "0.5.3" || c.Restart != Overlap || c.Interval != 5*time.Minute || !strings.HasSuffix(c.Releases, "releases.json") {
		t.Fatalf("stamped config = %+v", c)
	}
	stampedProject = "not base64!"
	if _, err := stampedConfig(); err == nil {
		t.Fatal("a corrupt stamp must be an error, not silence")
	}
}

func TestCheckIntervalPrecedence(t *testing.T) {
	t.Setenv("AMARIT_UPDATE_INTERVAL", "")
	if got := checkInterval(Config{}, 0); got != time.Hour {
		t.Fatalf("default = %v", got)
	}
	if got := checkInterval(Config{Interval: 5 * time.Minute}, 0); got != 5*time.Minute {
		t.Fatalf("config = %v", got)
	}
	t.Setenv("AMARIT_UPDATE_INTERVAL", "2m")
	if got := checkInterval(Config{Interval: 5 * time.Minute}, 0); got != 2*time.Minute {
		t.Fatalf("env over config = %v", got)
	}
	if got := checkInterval(Config{Interval: 5 * time.Minute}, 30*time.Second); got != 30*time.Second {
		t.Fatalf("flag over env = %v", got)
	}
}
