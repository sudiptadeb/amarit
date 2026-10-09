package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/sudiptadeb/amarit"
)

const label = "com.amarit.daemon"

// daemon is what the service manager (or a detached start) runs.
func daemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return amarit.NewDaemon(amarit.DefaultBase()).Run(ctx)
}

// install keeps the daemon itself running the strongest way the machine
// allows, and says which it took. Every later `amarit run` inherits it.
func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	system := fs.Bool("system", false, "install for the machine (sudo): starts at boot with nobody logged in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	base := amarit.DefaultBase()
	if *system {
		return installSystem(self, base)
	}
	switch runtime.GOOS {
	case "darwin":
		if launchctlOK("gui/" + uid()) {
			return installLaunchAgent(self, base)
		}
		fmt.Println("launchd cannot run per-user services for this account: it has no desktop login")
		fmt.Println("session (typical over SSH). Starting the daemon detached instead; it survives this")
		fmt.Println("terminal, not a reboot. For start-at-boot: sudo amarit install --system")
		return startDetached(self, base)
	case "linux":
		if systemdUserOK() {
			return installSystemdUser(self, base)
		}
		fmt.Println("no systemd user session for this account; starting the daemon detached instead.")
		fmt.Println("It survives this terminal, not a reboot. For start-at-boot: sudo amarit install --system")
		return startDetached(self, base)
	default:
		return fmt.Errorf("install is not supported on %s yet", runtime.GOOS)
	}
}

func uid() string { return fmt.Sprint(os.Getuid()) }

func launchctlOK(domain string) bool {
	return exec.Command("launchctl", "print", domain).Run() == nil
}

func systemdUserOK() bool {
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func installLaunchAgent(self string, base amarit.Base) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	plist := filepath.Join(dir, label+".plist")
	if err := os.WriteFile(plist, []byte(plistFor(self, base, "")), 0o644); err != nil {
		return err
	}
	dom := "gui/" + uid()
	_ = exec.Command("launchctl", "bootout", dom+"/"+label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", dom, plist).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("installed %s as a LaunchAgent (starts when you log in); units in %s\n", label, base)
	return nil
}

func installSystem(self string, base amarit.Base) error {
	if os.Geteuid() != 0 {
		return errors.New("--system needs sudo")
	}
	name := os.Getenv("SUDO_USER")
	if name == "" || name == "root" {
		return errors.New("--system installs for the user who ran sudo; run it as that user with sudo, not as root")
	}
	u, err := user.Lookup(name)
	if err != nil {
		return err
	}
	base = amarit.Base(filepath.Join(u.HomeDir, ".amarit"))
	switch runtime.GOOS {
	case "darwin":
		plist := "/Library/LaunchDaemons/" + label + ".plist"
		if err := os.WriteFile(plist, []byte(plistFor(self, base, name)), 0o644); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
		if out, err := exec.Command("launchctl", "bootstrap", "system", plist).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
		}
		fmt.Printf("installed %s as a LaunchDaemon running as %s (starts at boot); units in %s\n", label, name, base)
		fmt.Printf("from now on, 'amarit run ...' as %s needs no sudo\n", name)
		return nil
	case "linux":
		unit := "/etc/systemd/system/amarit-" + name + ".service"
		body := fmt.Sprintf(`[Unit]
Description=amarit daemon for %s
After=network.target

[Service]
User=%s
Environment=HOME=%s
Environment=AMARIT_HOME=%s
ExecStart=%s daemon
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`, name, name, u.HomeDir, base, self)
		if err := os.WriteFile(unit, []byte(body), 0o644); err != nil {
			return err
		}
		for _, c := range [][]string{{"systemctl", "daemon-reload"}, {"systemctl", "enable", "--now", "amarit-" + name}} {
			if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
				return fmt.Errorf("%s: %v: %s", strings.Join(c, " "), err, strings.TrimSpace(string(out)))
			}
		}
		fmt.Printf("installed amarit-%s.service (starts at boot); units in %s\n", name, base)
		return nil
	default:
		return fmt.Errorf("--system is not supported on %s yet", runtime.GOOS)
	}
}

func installSystemdUser(self string, base amarit.Base) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`[Unit]
Description=amarit daemon

[Service]
Environment=AMARIT_HOME=%s
ExecStart=%s daemon
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`, base, self)
	if err := os.WriteFile(filepath.Join(dir, "amarit.service"), []byte(body), 0o644); err != nil {
		return err
	}
	for _, c := range [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", "amarit"}} {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(c, " "), err, strings.TrimSpace(string(out)))
		}
	}
	if u, err := user.Current(); err == nil {
		if exec.Command("loginctl", "enable-linger", u.Username).Run() == nil {
			fmt.Println("lingering enabled: the daemon survives logout and starts at boot")
		} else {
			fmt.Printf("could not enable lingering; without it the daemon stops at logout: loginctl enable-linger %s\n", u.Username)
		}
	}
	fmt.Printf("installed amarit.service (systemd user unit); units in %s\n", base)
	return nil
}

func plistFor(self string, base amarit.Base, userName string) string {
	userKey := ""
	if userName != "" {
		userKey = "\t<key>UserName</key>\n\t<string>" + userName + "</string>\n"
	}
	home := filepath.Dir(string(base))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
%s	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>%s</string>
		<key>AMARIT_HOME</key>
		<string>%s</string>
		<key>PATH</key>
		<string>%s/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>StandardOutPath</key>
	<string>%s/daemon.log</string>
	<key>StandardErrorPath</key>
	<string>%s/daemon.log</string>
</dict>
</plist>
`, label, userKey, self, home, base, home, base, base)
}

// startDetached is the floor: a daemon with no supervisor of its own.
func startDetached(self string, base amarit.Base) error {
	if pid := base.DaemonPID(); pid != 0 {
		fmt.Printf("daemon already running (pid %d)\n", pid)
		return nil
	}
	if err := os.MkdirAll(string(base), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(string(base), "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, "daemon")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = append(os.Environ(), "AMARIT_HOME="+string(base))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("daemon started detached (pid %d); units in %s\n", cmd.Process.Pid, base)
	return nil
}

func uninstall(args []string) error {
	base := amarit.DefaultBase()
	switch runtime.GOOS {
	case "darwin":
		for _, dom := range []string{"gui/" + uid(), "system"} {
			_ = exec.Command("launchctl", "bootout", dom+"/"+label).Run()
		}
		home, _ := os.UserHomeDir()
		_ = os.Remove(filepath.Join(home, "Library", "LaunchAgents", label+".plist"))
		if os.Geteuid() == 0 {
			_ = os.Remove("/Library/LaunchDaemons/" + label + ".plist")
		}
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "amarit").Run()
		home, _ := os.UserHomeDir()
		_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", "amarit.service"))
	}
	if pid := base.DaemonPID(); pid != 0 {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}
	fmt.Println("daemon removed; units it started keep running until stopped by hand, their files stay in", base)
	return nil
}

// run registers a binary as a unit and makes sure a daemon is there to run it.
func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("name", "", "unit name (default: binary name plus its first flag)")
	restart := fs.String("restart", "always", "always, on-failure or never")
	dir := fs.String("dir", "", "working directory")
	envFile := fs.String("env-file", "", "file of KEY=VALUE lines sourced at start")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: amarit run [-name N] [-restart always|on-failure|never] [-dir D] [-env-file F] <binary> [args...]")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return errors.New("a binary is required")
	}
	bin, err := exec.LookPath(fs.Arg(0))
	if err != nil {
		return err
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return err
	}
	rest := fs.Args()[1:]
	if *name == "" {
		*name = filepath.Base(bin)
		if len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			*name += "-" + strings.TrimLeft(rest[0], "-")
		}
	}
	base := amarit.DefaultBase()
	u := amarit.Unit{Name: *name, Binary: bin, Args: rest, Restart: *restart, Dir: *dir, EnvFile: *envFile}
	if err := base.SaveUnit(u); err != nil {
		return err
	}
	fmt.Printf("unit %s: %s %s\n", u.Name, bin, strings.Join(rest, " "))
	if base.DaemonPID() == 0 {
		fmt.Println("no daemon running; starting one detached (run 'amarit install' for a supervised one)")
		self, _ := os.Executable()
		if err := startDetached(self, base); err != nil {
			return err
		}
	}
	return waitRunning(base, u.Name)
}

func waitRunning(base amarit.Base, name string) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := base.Statuses()
		if st, ok := s[name]; ok && st.State == "running" {
			fmt.Printf("%s running, pid %d\n", name, st.PID)
			return nil
		}
		if st, ok := s[name]; ok && st.LastExit != "" {
			return fmt.Errorf("%s: %s (see amarit logs %s)", name, st.LastExit, name)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("%s did not report running; see amarit logs %s", name, name)
}

func ls(args []string) error {
	base := amarit.DefaultBase()
	units, err := base.Units()
	if err != nil {
		return err
	}
	statuses, _ := base.Statuses()
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if pid := base.DaemonPID(); pid != 0 {
		fmt.Fprintf(w, "daemon\tpid %d\t%s\n", pid, base)
	} else {
		fmt.Fprintf(w, "daemon\tnot running\t%s\n", base)
	}
	fmt.Fprintln(w, "UNIT\tSTATE\tPID\tRESTARTS\tSINCE\tCOMMAND")
	for _, u := range units {
		s := statuses[u.Name]
		state := s.State
		if state == "" {
			state = "pending"
		}
		if u.Enabled != nil && !*u.Enabled {
			state = "stopped"
		}
		since := ""
		if s.PID != 0 {
			since = time.Since(s.Since).Round(time.Second).String()
		}
		pid := ""
		if s.PID != 0 {
			pid = fmt.Sprint(s.PID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s %s\n", u.Name, state, pid, s.Restarts, since, filepath.Base(u.Binary), strings.Join(u.Args, " "))
	}
	return w.Flush()
}

func setEnabled(name string, on bool) error {
	base := amarit.DefaultBase()
	units, err := base.Units()
	if err != nil {
		return err
	}
	for _, u := range units {
		if u.Name == name {
			u.Enabled = &on
			return base.SaveUnit(u)
		}
	}
	return fmt.Errorf("no unit named %s", name)
}

func logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("f", false, "follow")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: amarit logs [-f] <unit>")
	}
	path := filepath.Join(string(amarit.DefaultBase()), "logs", fs.Arg(0)+".log")
	tail := exec.Command("tail", "-n", "50", path)
	if *follow {
		tail = exec.Command("tail", "-n", "50", "-f", path)
	}
	tail.Stdout, tail.Stderr = os.Stdout, os.Stderr
	return tail.Run()
}
