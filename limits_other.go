//go:build !darwin && !linux

package amarit

import "errors"

// ExecLimited needs setrlimit and exec; see limits_unix.go.
func ExecLimited(args []string) error {
	return errors.New("amarit: resource limits are not supported on this platform yet")
}
