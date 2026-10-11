//go:build darwin || linux

package amarit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLimitsReachTheUnitProcess(t *testing.T) {
	dir := t.TempDir()
	base := Base(filepath.Join(dir, "base"))
	seen := filepath.Join(dir, "nofile")
	probe := script(t, dir, "probe", `ulimit -n > `+seen+`; echo "limits=$AMARIT_LIMITS pid=$$" >> `+seen+`; exec sleep 60`)
	if err := base.SaveUnit(Unit{Name: "probe", Binary: probe, Limits: map[string]uint64{"nofile": 1234}}); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(base)
	d.tick = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	var pid int
	waitFor(t, "probe to report its limit", func() bool {
		s, _ := base.Statuses()
		pid = s["probe"].PID
		b, _ := os.ReadFile(seen)
		return pid != 0 && strings.Contains(string(b), "limits=")
	})
	// The limit applies, the variable is gone, and the pid the daemon
	// watches is the program's own, not a wrapper's.
	b, _ := os.ReadFile(seen)
	if got, want := strings.TrimSpace(string(b)), fmt.Sprintf("1234\nlimits= pid=%d", pid); got != want {
		t.Fatalf("the unit saw %q; want %q", got, want)
	}
	base.RemoveUnit("probe")
	waitFor(t, "probe to stop", func() bool { return !processAlive(pid) })
}
