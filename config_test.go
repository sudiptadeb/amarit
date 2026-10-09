package amarit

import (
	"encoding/base64"
	"strings"
	"testing"
)

const goodConfig = `{
  "project": "termulaa",
  "releases": "https://github.com/sudiptadeb/termulaa/releases/latest/download/releases.json",
  "restart": "overlap",
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
		"no project":  `{"releases":"https://m"}`,
		"no releases": `{"project":"x"}`,
		"bad restart": `{"project":"x","releases":"https://m","restart":"fork"}`,
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
	if c.Project != "termulaa" || c.Version != "0.5.3" || c.Restart != Overlap || !strings.HasSuffix(c.Releases, "releases.json") {
		t.Fatalf("stamped config = %+v", c)
	}
	stampedProject = "not base64!"
	if _, err := stampedConfig(); err == nil {
		t.Fatal("a corrupt stamp must be an error, not silence")
	}
}
