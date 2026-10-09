package upkeep

import (
	"encoding/base64"
	"testing"
)

const goodConfig = `{
  "project": "termulaa",
  "manifest": "https://github.com/sudiptadeb/termulaa/releases/latest/download/upkeep.json",
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
		"no project":   `{"manifest":"https://m"}`,
		"no manifest":  `{"project":"x"}`,
		"bad versions": `{"project":"x","manifest":"https://m","versions":"dates"}`,
		"bad restart":  `{"project":"x","manifest":"https://m","restart":"fork"}`,
	} {
		if _, err := ParseProjectConfig([]byte(body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestStartWithoutStampDoesNothing(t *testing.T) {
	stampedConfig, stampedVersion = "", ""
	o, err := Start()
	if err != nil || o.AutoUpdate || o.Channel != "" {
		t.Fatalf("unstamped build: %+v %v", o, err)
	}
}

func TestStartReadsStamp(t *testing.T) {
	stampedConfig = base64.StdEncoding.EncodeToString([]byte(goodConfig))
	stampedVersion = "0.5.3"
	defer func() { stampedConfig, stampedVersion = "", "" }()
	o, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	if o.Channel != "stable" {
		t.Fatalf("default channel = %q", o.Channel)
	}
	stampedConfig = "not base64!"
	if _, err := Start(); err == nil {
		t.Fatal("a corrupt stamp must be an error, not silence")
	}
}
