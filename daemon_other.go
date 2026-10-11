//go:build !unix

package amarit

import (
	"errors"
	"os"
	"syscall"
	"time"
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

// watchPID polls a process amarit did not spawn, since it cannot wait() on it.
func watchPID(pid int) chan error {
	ch := make(chan error, 1)
	go func() {
		for processAlive(pid) {
			time.Sleep(time.Second)
		}
		ch <- errors.New("exited (adopted process; exit status unknown)")
	}()
	return ch
}
