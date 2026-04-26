//go:build !windows

package api

import "syscall"

// daemonSysProcAttr returns process attributes that detach a spawned
// successor daemon into its own session so it survives the current process.
//
// Identical to internal/daemon/client/sysproc_unix.go — duplicated to break
// an import cycle (daemon → api → daemon/client → daemon). Update both files
// together if the detach semantics ever change.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
