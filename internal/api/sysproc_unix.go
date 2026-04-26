//go:build !windows

package api

import "syscall"

// daemonSysProcAttr returns process attributes that detach a spawned
// successor daemon into its own session so it survives the current process.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
