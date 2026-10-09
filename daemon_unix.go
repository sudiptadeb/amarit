//go:build unix

package amarit

import "syscall"

// Children get their own session so a daemon restart or a closed terminal
// never reaches them with SIGHUP; the daemon stops them explicitly.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
