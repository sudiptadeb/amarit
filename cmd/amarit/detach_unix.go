//go:build unix

package main

import "syscall"

// A detached daemon gets its own session so closing the terminal never
// reaches it.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
