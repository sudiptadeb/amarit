//go:build !unix

package amarit

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }
