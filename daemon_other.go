//go:build !unix

package amarit

import (
	"os"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr { return nil }

// Without process groups, the process itself is all there is to signal.
func signalGroup(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if sig == syscall.SIGKILL {
		return p.Kill()
	}
	return p.Signal(sig)
}

func groupAlive(pid int) bool { return processAlive(pid) }
