// Package selfupdate fetches origin, hard-resets to origin/master, and runs
// `go install ./...` against a configured Argus source clone so the running
// binary can be replaced with a freshly-built one. Callers (daemon RPC, web
// API) are responsible for triggering the daemon respawn after a successful
// run.
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

// Run fetches origin, hard-resets the source clone to `origin/master`, and
// runs `go install ./...` from sourceDir. The combined stdout+stderr is
// returned regardless of error so callers can surface the log to the user.
//
// The reset is best-effort: fetch/reset failures are logged but do not abort
// the run — the user may be offline, or origin/master may not exist (e.g.
// fork). A `go install` failure does abort.
//
// Hard-reset is intentional: this clone exists solely to produce binaries,
// not for development, so tracking-branch state and local commits are not
// preserved. Whatever's on origin/master is exactly what gets installed.
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
		log.WriteString("$ git fetch origin master\n")
		out, err := runCmd(dir, "git", "fetch", "origin", "master")
		log.WriteString(out)
		if err != nil {
			// Most common causes: offline, or origin uses `main` not
			// `master` (e.g. fork). Either way, the existing local source
			// is left untouched — `go install` will rebuild from whatever
			// the clone already contains.
			fmt.Fprintf(&log, "(git fetch failed: %v — leaving source clone untouched)\n\n", err)
		} else {
			log.WriteString("\n$ git reset --hard origin/master\n")
			out, err := runCmd(dir, "git", "reset", "--hard", "origin/master")
			log.WriteString(out)
			if err != nil {
				// `git reset --hard` is not atomic at the file level: a
				// mid-operation failure can leave the working tree mixing
				// origin/master files with the previous HEAD. `go install`
				// runs against that mixed state and will surface its own
				// errors if the result doesn't compile.
				fmt.Fprintf(&log, "(git reset failed: %v — working tree may be partially reset)\n", err)
			}
			log.WriteString("\n")
		}
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
