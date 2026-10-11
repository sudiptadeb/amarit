//go:build !unix

package amarit

// Updates do not run on this platform yet (see exec_other.go), so there is
// nothing to serialise.
func lockFile(path string) (func(), error) { return func() {}, nil }
