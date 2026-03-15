//go:build !windows

package main

import "syscall"

// daemonSysProcAttr returns process attributes that detach the daemon
// into its own session so it survives the parent (TUI) exiting.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
