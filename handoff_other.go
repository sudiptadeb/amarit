//go:build !unix

package amarit

import (
	"errors"
	"os"
)

func prepareHandoff(files []*os.File) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	return "", errors.New("amarit: descriptor handoff is not supported on this platform yet")
}

// Inherited returns nothing on platforms without the exec handoff.
func Inherited() []*os.File { return nil }
