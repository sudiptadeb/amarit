//go:build unix

package amarit

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on path, waiting for whoever holds it.
// The lock lives on the descriptor, so it ends with the process, exec
// included; a process that dies mid-update cannot leave it behind.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
