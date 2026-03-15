//go:build windows

package main

import "syscall"

// daemonSysProcAttr returns nil on Windows (no session detach needed).
func daemonSysProcAttr() *syscall.SysProcAttr {
	return nil
}
