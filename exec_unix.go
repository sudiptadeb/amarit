//go:build unix

package upkeep

import "syscall"

// execInPlace replaces the process image with the new binary: same PID, same
// argv, same environment, so whatever supervises this process notices nothing.
func execInPlace(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}
