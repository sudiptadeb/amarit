//go:build !unix

package main

import "syscall"

func detachAttr() *syscall.SysProcAttr { return nil }
