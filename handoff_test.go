//go:build unix

package amarit

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

func cloexec(t *testing.T, fd int) bool {
	t.Helper()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	return flags&syscall.FD_CLOEXEC != 0
}

// Both sides of the handoff live in this one process, so each pipe end gets
// a second *os.File; collected, its finalizer would close a descriptor
// number that another test owns by then. Keep them reachable.
var handoffWrappers []*os.File

func TestHandoffRoundTrip(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if !cloexec(t, int(r.Fd())) {
		t.Fatal("Go opens pipes close-on-exec; the test premise is wrong")
	}

	named, listener := os.NewFile(r.Fd(), "pty:abc:123"), os.NewFile(w.Fd(), "listener")
	handoffWrappers = append(handoffWrappers, named, listener)
	entry, err := prepareHandoff([]*os.File{named, listener})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(entry, handoffEnv+"=") {
		t.Fatalf("entry %q", entry)
	}
	if cloexec(t, int(r.Fd())) {
		t.Fatal("prepareHandoff must clear close-on-exec so the fd survives the exec")
	}

	t.Setenv(handoffEnv, strings.TrimPrefix(entry, handoffEnv+"="))
	files := Inherited()
	handoffWrappers = append(handoffWrappers, files...)
	if len(files) != 2 || files[0].Name() != "pty:abc:123" || files[1].Name() != "listener" {
		t.Fatalf("inherited %v", files)
	}
	if int(files[0].Fd()) != int(r.Fd()) {
		t.Fatalf("fd %d, want %d", files[0].Fd(), r.Fd())
	}
	if !cloexec(t, int(r.Fd())) {
		t.Fatal("Inherited must set close-on-exec again so the fd does not leak into children")
	}
	if os.Getenv(handoffEnv) != "" {
		t.Fatal("Inherited must clear the env so children do not see it")
	}
	if Inherited() != nil {
		t.Fatal("a second call has nothing to return")
	}

	// The descriptor still works after the round trip.
	go w.Write([]byte("hi"))
	buf := make([]byte, 2)
	if n, _ := files[0].Read(buf); n != 2 || string(buf) != "hi" {
		t.Fatalf("read %q", buf[:n])
	}
}
