//go:build unix

package amarit

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// handoffEnv carries "fd:name,fd:name" (names base64url) across the exec.
const handoffEnv = "AMARIT_HANDOFF"

func setCloexec(fd int, on bool) error {
	flag := uintptr(0)
	if on {
		flag = syscall.FD_CLOEXEC
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, flag); errno != 0 {
		return errno
	}
	return nil
}

// prepareHandoff makes the files survive the exec and returns the env entry
// that tells the new process which descriptor is which.
func prepareHandoff(files []*os.File) (string, error) {
	var parts []string
	for _, f := range files {
		fd := int(f.Fd()) // Fd() also puts the file in blocking mode, which the exec'd side expects
		if err := setCloexec(fd, false); err != nil {
			return "", fmt.Errorf("handoff %s: %w", f.Name(), err)
		}
		parts = append(parts, strconv.Itoa(fd)+":"+base64.RawURLEncoding.EncodeToString([]byte(f.Name())))
	}
	return handoffEnv + "=" + strings.Join(parts, ","), nil
}

// Inherited returns the descriptors a previous process handed over through
// the restart, with the names its Handoff gave them. Empty on a fresh start.
// Each adopted descriptor is made close-on-exec again so it does not leak
// into programs this process spawns.
func Inherited() []*os.File {
	spec := os.Getenv(handoffEnv)
	if spec == "" {
		return nil
	}
	os.Unsetenv(handoffEnv)
	var files []*os.File
	for _, part := range strings.Split(spec, ",") {
		fdStr, nameB64, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		fd, err := strconv.Atoi(fdStr)
		if err != nil || fd < 0 {
			continue
		}
		name, _ := base64.RawURLEncoding.DecodeString(nameB64)
		_ = setCloexec(fd, true)
		files = append(files, os.NewFile(uintptr(fd), string(name)))
	}
	return files
}
