//go:build !unix

package upkeep

import "errors"

// Windows has no exec(); the helper-process strategy is a later milestone.
func execInPlace(bin string, argv, env []string) error {
	return errors.New("upkeep: in-place restart is not supported on this platform yet")
}
