//go:build darwin || linux

package amarit

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ExecLimited is what `amarit __exec <binary> [args...]` runs. The daemon
// starts a unit that has limits this way: the limits in AMARIT_LIMITS are
// applied to this process, the variable is removed, and the binary replaces
// the process image, so the limits reach that program alone and the pid the
// daemon watches is the program's own.
func ExecLimited(args []string) error {
	if len(args) == 0 {
		return errors.New("__exec: a binary is required")
	}
	for _, kv := range strings.Split(os.Getenv(limitsEnv), ",") {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			log.Printf("amarit: limit %s=%q is not a number", name, val)
			continue
		}
		if err := setLimit(name, n); err != nil {
			log.Printf("amarit: limit %s=%d: %v", name, n, err)
		}
	}
	os.Unsetenv(limitsEnv)
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return err
	}
	return syscall.Exec(bin, args, os.Environ())
}

// setLimit raises or lowers the soft limit. Above the hard limit it takes
// the hard limit and says so, since an unprivileged process can do no more.
func setLimit(name string, n uint64) error {
	var resource int
	switch name {
	case "nofile":
		resource = syscall.RLIMIT_NOFILE
	default:
		return fmt.Errorf("unknown limit")
	}
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(resource, &rl); err != nil {
		return err
	}
	if n > uint64(rl.Max) {
		log.Printf("amarit: limit %s %d is above the hard limit; using %d", name, n, rl.Max)
		n = uint64(rl.Max)
	}
	rl.Cur = n
	return syscall.Setrlimit(resource, &rl)
}
