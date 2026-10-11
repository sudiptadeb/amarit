//go:build unix

package amarit

import "syscall"

// Children get their own session so a daemon restart or a closed terminal
// never reaches them with SIGHUP; the daemon stops them explicitly.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// signalGroup signals every process in the group whose id is pid.
func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

// groupAlive reports whether any process of the group is still there.
func groupAlive(pid int) bool {
	return syscall.Kill(-pid, 0) == nil
}
