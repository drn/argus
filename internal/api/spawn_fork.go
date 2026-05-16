package api

import (
	"os"
	"os/exec"
)

// spawnSuccessorDaemonFork is the test-hostile body of spawnSuccessorDaemon:
// it fork/execs the current binary as `argus daemon start`. Under `go test`,
// doing so would re-run the entire test package as an orphaned child — the
// exact fork bomb errSpawnFromTestBinary in selfupdate.go is designed to
// prevent. This file is listed in coverage-ignore.txt because the only way
// to exercise it from tests is to break that invariant.
func spawnSuccessorDaemonFork() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// exe comes from os.Executable() — i.e. the path the daemon itself was
	// started with, not user-supplied input.
	cmd := exec.Command(exe, "daemon", "start") //nolint:gosec // exe is os.Executable()
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach the child so we don't wait/reap it.
	return cmd.Process.Release()
}
