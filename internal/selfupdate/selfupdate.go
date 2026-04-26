// Package selfupdate runs `git pull --ff-only` and `go install ./...` against
// a configured Argus source clone so the running binary can be replaced with
// a freshly-built one. Callers (daemon RPC, web API) are responsible for
// triggering the daemon respawn after a successful run.
package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrSourcePathUnset is returned when no source path is configured.
var ErrSourcePathUnset = errors.New("argus source path is not configured")

// Run executes `git pull --ff-only` (best-effort) followed by `go install ./...`
// from sourceDir. The combined stdout+stderr is returned regardless of error
// so callers can surface the log to the user.
//
// `git pull` failures are logged into the output but do not abort the run —
// the user may already have the latest commit local, or be on a branch with
// no upstream. A `go install` failure does abort.
func Run(sourceDir string) (string, error) {
	if strings.TrimSpace(sourceDir) == "" {
		return "", ErrSourcePathUnset
	}
	dir, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("source path %s is not a directory", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("source path %s is not a Go module (no go.mod)", dir)
	}

	var log strings.Builder

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		log.WriteString("$ git pull --ff-only\n")
		out, err := runCmd(dir, "git", "pull", "--ff-only")
		log.WriteString(out)
		if err != nil {
			fmt.Fprintf(&log, "(git pull failed: %v — continuing with local source)\n", err)
		}
		log.WriteString("\n")
	}

	log.WriteString("$ go install ./...\n")
	out, err := runCmd(dir, "go", "install", "./...")
	log.WriteString(out)
	if err != nil {
		return log.String(), fmt.Errorf("go install: %w", err)
	}
	log.WriteString("\nUpdate succeeded. Restart the daemon to pick up the new binary.\n")
	return log.String(), nil
}

// runCmd shells out to a fixed binary name (git or go in this package) with
// fixed argument vectors. The command name is parameterised so we can share
// one helper, but no caller-supplied data is interpolated.
func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // name and args are package-internal literals
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
