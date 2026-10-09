package upkeep

import (
	"strings"
	"testing"
	"time"
)

const goodManifest = `{
  "spec": 1,
  "project": "termulaa",
  "published": "2026-10-09T10:00:00Z",
  "expires": "2026-11-09T10:00:00Z",
  "min_version": "0.5.0",
  "channels": {
    "stable": { "version": "0.5.2" },
    "canary": { "version": "0.5.3", "rollout": 25, "critical": true }
  },
  "assets": {
    "0.5.2": {
      "darwin/arm64": { "url": "https://example.com/t-0.5.2", "sha256": "` + zeros + `", "size": 10 }
    },
    "0.5.3": {
      "darwin/arm64": { "url": "https://example.com/t-0.5.3", "sha256": "` + zeros + `", "size": 11, "sig": "RWQ" },
      "android":      { "url": "https://example.com/t.apk", "sha256": "` + zeros + `", "size": 12, "version_code": 4 }
    }
  }
}`

const zeros = "0000000000000000000000000000000000000000000000000000000000000000"

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Project != "termulaa" || m.MinVersion != "0.5.0" {
		t.Fatalf("parsed %+v", m)
	}
	ch, a, ok := m.Lookup("canary", "darwin/arm64")
	if !ok || ch.Version != "0.5.3" || a.Size != 11 || a.Sig != "RWQ" || !ch.Critical {
		t.Fatalf("lookup canary/darwin/arm64 = %+v %+v %v", ch, a, ok)
	}
	if _, _, ok := m.Lookup("stable", "android"); ok {
		t.Fatal("stable has no android asset, lookup must say so")
	}
	if _, _, ok := m.Lookup("beta", "darwin/arm64"); ok {
		t.Fatal("unknown channel must not resolve")
	}
	if m.Stale(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("manifest is fresh before expires")
	}
	if !m.Stale(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("manifest is stale after expires")
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string]string{
		"unknown spec":           strings.Replace(goodManifest, `"spec": 1`, `"spec": 2`, 1),
		"missing project":        strings.Replace(goodManifest, `"project": "termulaa"`, `"project": ""`, 1),
		"channel without assets": strings.Replace(goodManifest, `"version": "0.5.2"`, `"version": "0.9.9"`, 1),
		"rollout out of range":   strings.Replace(goodManifest, `"rollout": 25`, `"rollout": 101`, 1),
		"bad digest":             strings.Replace(goodManifest, zeros, "abc", 1),
		"zero size":              strings.Replace(goodManifest, `"size": 10`, `"size": 0`, 1),
		"version with bad chars": strings.Replace(goodManifest, `"0.5.3"`, `"0.5.3 beta"`, -1),
		"no channels":            strings.Replace(goodManifest, `"channels": {`, `"channels": {}, "x": {`, 1),
	}
	for name, body := range cases {
		if _, err := ParseManifest([]byte(body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestInRollout(t *testing.T) {
	id := []byte{0, 0, 0, 42, 9, 9, 9, 9} // 42 % 100 = 42
	pct := func(n int) *int { return &n }
	if !InRollout(id, nil) || !InRollout(id, pct(100)) {
		t.Fatal("no rollout field or 100 means everyone")
	}
	if InRollout(id, pct(0)) {
		t.Fatal("0 means nobody")
	}
	if InRollout(id, pct(42)) {
		t.Fatal("42 is not below 42")
	}
	if !InRollout(id, pct(43)) {
		t.Fatal("42 is below 43")
	}
	if InRollout([]byte{1}, pct(50)) {
		t.Fatal("a short id is never selected rather than guessed")
	}
}

func TestParseArgs(t *testing.T) {
	o, rest := parseArgs([]string{"--auto-update=canary", "--update-url", "https://m", "--allow-downgrade", "-port", "17380", "--update-key=K"})
	if !o.AutoUpdate || o.Channel != "canary" || o.ManifestURL != "https://m" || !o.AllowDowngrade || o.Key != "K" {
		t.Fatalf("options %+v", o)
	}
	if len(rest) != 2 || rest[0] != "-port" || rest[1] != "17380" {
		t.Fatalf("program args %v", rest)
	}
	o, rest = parseArgs([]string{"service", "install", "--system"})
	if o.Service != "install" || len(rest) != 1 || rest[0] != "--system" {
		t.Fatalf("service %q rest %v", o.Service, rest)
	}
	o, rest = parseArgs([]string{"-rc", "service"})
	if o.Service != "" || len(rest) != 2 {
		t.Fatalf("a later word 'service' belongs to the program: %q %v", o.Service, rest)
	}
}
