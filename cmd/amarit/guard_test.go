package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardFindsOtherSupervisors(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "web")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	link := filepath.Join(dir, "web-link")
	os.Symlink(bin, link)

	launchd := filepath.Join(dir, "LaunchAgents")
	os.MkdirAll(launchd, 0o755)
	os.WriteFile(filepath.Join(launchd, "com.example.web.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>Label</key><string>com.example.web</string>
	<key>EnvironmentVariables</key><dict><key>Program</key><string>/not/this</string></dict>
	<key>ProgramArguments</key><array><string>`+link+`</string><string>-serve</string></array>
</dict></plist>`), 0o644)
	os.WriteFile(filepath.Join(launchd, "com.example.other.plist"), []byte(`<plist version="1.0"><dict>
	<key>Label</key><string>com.example.other</string>
	<key>Program</key><string>/usr/bin/true</string>
</dict></plist>`), 0o644)

	systemd := filepath.Join(dir, "systemd", "user")
	os.MkdirAll(systemd, 0o755)
	os.WriteFile(filepath.Join(systemd, "web.service"), []byte("[Service]\nExecStart=-"+bin+" -serve\n"), 0o644)
	os.WriteFile(filepath.Join(systemd, "other.service"), []byte("[Service]\nExecStart=/usr/bin/true\n"), 0o644)

	found := scanLaunchd([]string{launchd, filepath.Join(dir, "missing")}, bin, []string{"-serve"})
	if len(found) != 1 || !strings.Contains(found[0].what, "com.example.web") || !strings.Contains(found[0].remove, "launchctl bootout") {
		t.Fatalf("launchd scan found %+v", found)
	}
	found = scanSystemd([]string{systemd}, bin, nil)
	if len(found) != 1 || !strings.Contains(found[0].what, "web.service") || !strings.HasPrefix(found[0].remove, "systemctl --user disable") {
		t.Fatalf("systemd scan found %+v", found)
	}
	// Same interpreter, different script: not the same job.
	if found := scanLaunchd([]string{launchd}, bin, []string{"-other"}); len(found) != 0 {
		t.Fatalf("different arguments must not match, got %+v", found)
	}
	if guard(bin, nil, true) != nil {
		t.Fatal("-force must let it through")
	}
}
