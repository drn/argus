package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/profiles"
)

// runProfilesCommand handles: argus profiles <subcommand>
//
// Operator/documentation tooling only — like `validate`, this is NOT wired
// into the Go build, CI, or any Make gate.
func runProfilesCommand(args []string) {
	if len(args) == 0 || args[0] != "install-defaults" {
		fmt.Fprintln(os.Stderr, "usage: argus profiles install-defaults")
		os.Exit(2)
	}
	dir := filepath.Join(db.DataDir(), "profiles")
	os.Exit(runProfilesInstallDefaults(os.Stdout, dir))
}

// runProfilesInstallDefaults is the pure, testable core of `profiles
// install-defaults`: it installs the embedded seed profiles into dir and
// reports what was installed vs. already present.
func runProfilesInstallDefaults(w io.Writer, dir string) int {
	installed, skipped, err := profiles.InstallDefaults(dir)
	if err != nil {
		_, _ = fmt.Fprintf(w, "install failed: %v\n", err)
		return 1
	}
	if len(installed) == 0 {
		_, _ = fmt.Fprintf(w, "all %d default profiles already present in %s — nothing to do\n", len(skipped), dir)
		return 0
	}
	_, _ = fmt.Fprintf(w, "installed: %v\n", installed)
	if len(skipped) > 0 {
		_, _ = fmt.Fprintf(w, "already present (left untouched): %v\n", skipped)
	}
	return 0
}
