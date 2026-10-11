//go:build unix

package amarit

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

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

// watchPID waits for a process this daemon image did not spawn. After the
// daemon updated itself in place the pid is the same, so its units are
// still its children and must be reaped, or they would sit as zombies that
// kill(pid, 0) reports alive forever. A process that is not our child is
// polled instead.
func watchPID(pid int) chan error {
	ch := make(chan error, 1)
	go func() {
		var ws syscall.WaitStatus
		for {
			wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
			switch {
			case err == nil && wpid == pid:
				switch {
				case ws.Signaled():
					ch <- fmt.Errorf("signal: %v", ws.Signal())
				case ws.ExitStatus() != 0:
					ch <- fmt.Errorf("exit status %d", ws.ExitStatus())
				default:
					ch <- nil
				}
				return
			case err != nil && !errors.Is(err, syscall.EINTR):
				// Not our child: watch from outside.
				for processAlive(pid) {
					time.Sleep(time.Second)
				}
				ch <- errors.New("exited (adopted process; exit status unknown)")
				return
			}
			time.Sleep(time.Second)
		}
	}()
	return ch
}
